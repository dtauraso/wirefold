// node_move_test.go — decentralized node-move path.
//
// Locks that a node-move handled WITHOUT a central coordinator reproduces the old
// applyNodeMove result per-goroutine: the moved node re-emits its node-geometry, and
// each incident edge recomputes its own segment/arc, re-emits its edge geometry,
// revises any in-flight bead, and updates the dest port's latency aggregate. The
// move is delivered exactly as the live bridge does — by mail-sorting each entry to
// the node's inbox and every incident edge's inbox via MoveDispatch.dispatch.

package Wiring

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
)

// deliver mail-sorts a node-move to the node's inbox + every incident edge's inbox,
// each with an ack the mover closes when done, then waits — mirroring the live
// stdin-reader dispatch but blocking so the test can assert deterministically.
// deliver sends "center" messages (the sphere-chain position model) to the node's
// inbox + every incident edge's inbox, blocking on acks.
func deliver(md *MoveDispatch, nodeID string, x, y, z float64) {
	center := &vec3{X: x, Y: y, Z: z}
	var keys []string
	keys = append(keys, nodeID)
	for edgeID, em := range md.edgeMovers {
		if em.srcID == nodeID || em.dstID == nodeID {
			keys = append(keys, edgeID)
		}
	}
	acks := make([]chan struct{}, 0, len(keys))
	for _, kk := range keys {
		ack := make(chan struct{})
		md.dispatch[kk] <- moveMsg{Kind: moveMsgKindCenter, NodeID: nodeID, Center: center, ack: ack}
		acks = append(acks, ack)
	}
	for _, ack := range acks {
		<-ack
	}
}

func TestDecentralizedNodeMove(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, slotReg, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch the per-node and per-edge goroutines

	out := md.EdgeOut("e0")
	pw := slotReg["dst.In"]
	if out == nil || pw == nil {
		t.Fatalf("missing Out/wire: out=%v pw=%v", out, pw)
	}

	// Place a bead on the wire so the move must revise it in flight.
	seg0 := wireSegment{Start: out.Geom().Start, End: out.Geom().End}
	bp := beadPlacement{InFlightMs: out.Geom().SimLatencyMs, Start: seg0.Start, End: seg0.End, Node: "src", Port: "Out"}
	if !placeAndDrive(pw, 7, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Move src — delivered per-goroutine (no central registry). The world target
	// snaps to a lattice cell (the only position model).
	const nx, ny, nz = 400, 250, 30
	deliver(md, "src", nx, ny, nz)

	// Expected recompute from the moved geometry: src center is the world target.
	// Use aimed computation to match the edge mover (all edge-connected ports are aimed).
	srcCenter := vec3{X: nx, Y: ny, Z: nz}
	dstCenter := vec3{X: 0, Y: 0, Z: 0}
	srcGeom := nodeGeom{Kind: "FanInSrc", Center: &srcCenter, Outputs: []portGeom{{Name: "Out"}}}
	dstGeom := nodeGeom{Kind: "FanInSink", Center: &dstCenter, Inputs: []portGeom{{Name: "In"}}}
	wantReg := AimedPortRegistry{
		{NodeID: "src", PortName: "Out", IsInput: false}: "dst",
		{NodeID: "dst", PortName: "In", IsInput: true}:   "src",
	}
	wantCenters := map[string]vec3{"src": srcCenter, "dst": dstCenter}
	wantCenterOf := func(id string) (vec3, bool) { c, ok := wantCenters[id]; return c, ok }
	wantSeg := segmentBetweenPortsAimed(srcGeom, "Out", "src", dstGeom, "In", "dst", wantReg, wantCenterOf)
	wantArc := wantSeg.Start.sub(wantSeg.End).length()

	// Edge mover wrote the new segment/arc onto the source Out.
	if !approxEq(out.Geom().ArcLength, wantArc) || !approxEq(out.Geom().SimLatencyMs, wantArc/PulseSpeedWuPerMs) {
		t.Fatalf("Out arc/lat = %v/%v, want %v/%v", out.Geom().ArcLength, out.Geom().SimLatencyMs, wantArc, wantArc/PulseSpeedWuPerMs)
	}
	if !approxEq(out.Geom().End.X, wantSeg.End.X) || !approxEq(out.Geom().Start.X, wantSeg.Start.X) {
		t.Fatalf("Out segment = %+v..%+v, want %+v..%+v", out.Geom().Start, out.Geom().End, wantSeg.Start, wantSeg.End)
	}

	// Wire latency aggregate updated to the moved edge's latency.
	pw.mu.Lock()
	gotMax := pw.MaxIncomingSimLatencyMs
	pw.mu.Unlock()
	if !approxEq(gotMax, wantArc/PulseSpeedWuPerMs) {
		t.Fatalf("MaxIncomingSimLatencyMs = %v, want %v", gotMax, wantArc/PulseSpeedWuPerMs)
	}

	// In-flight bead's geometry was revised to the new segment (still in flight).
	pw.mu.Lock()
	var revisedSeg wireSegment
	stillInFlight := len(pw.inflight) > 0
	if stillInFlight {
		revisedSeg = pw.inflight[0].seg
	}
	pw.mu.Unlock()
	if !stillInFlight {
		t.Fatal("bead left flight unexpectedly during move")
	}
	if !approxEq(revisedSeg.End.X, wantSeg.End.X) || !approxEq(revisedSeg.Start.X, wantSeg.Start.X) {
		t.Fatalf("in-flight segment not revised: got %+v..%+v", revisedSeg.Start, revisedSeg.End)
	}

	// Give the trace a moment, then assert the node re-emitted node-geometry and the
	// edge re-emitted its segment.
	time.Sleep(5 * time.Millisecond)
	tr.Close()
	events := tr.Events()
	var sawNodeGeom, sawEdgeGeom bool
	for _, e := range events {
		if e.Kind == T.KindNodeGeometry && e.Node == "src" {
			sawNodeGeom = true
		}
		if e.Kind == T.KindGeometry && e.Edge == "e0" && approxEq(e.EX, wantSeg.End.X) {
			sawEdgeGeom = true
		}
	}
	if !sawNodeGeom {
		t.Fatal("node 'src' did not re-emit node-geometry on move")
	}
	if !sawEdgeGeom {
		t.Fatal("edge 'e0' did not re-emit its re-derived segment on move")
	}
}

// TestResendGeometry locks the remount-recovery path: ResendGeometry re-emits a
// node-geometry event for every node and an edge-geometry event for every edge from
// the held authoritative state, identical to what startup streams — so a remounted
// webview can rebuild its edge-geometry store without restarting Go.
func TestResendGeometry(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Safe to call repeatedly; call twice to lock idempotency-while-running.
	md.ResendGeometry(ctx, tr)
	md.ResendGeometry(ctx, tr)

	tr.Close()
	events := tr.Events()
	nodeGeoms := map[string]bool{}
	edgeGeoms := map[string]bool{}
	for _, e := range events {
		if e.Kind == T.KindNodeGeometry {
			nodeGeoms[e.Node] = true
		}
		if e.Kind == T.KindGeometry {
			edgeGeoms[e.Edge] = true
		}
	}
	for _, n := range []string{"src", "dst"} {
		if !nodeGeoms[n] {
			t.Fatalf("ResendGeometry did not re-emit node-geometry for %q", n)
		}
	}
	if !edgeGeoms["e0"] {
		t.Fatal("ResendGeometry did not re-emit edge-geometry for 'e0'")
	}
}

// TestNodeGeometryLabelSidecar locks the new-system label sidecar contract at the Go
// layer: every node-geometry event carries a Label field (data.label when present, else
// the node id), and the labels arrive in node-row order (first-seen node-geometry order,
// == Buffer.SnapshotState insertion order). ResendGeometry re-emits them so a remounted
// webview repopulates its row-keyed label table. The webview host derives the {id,label}
// sidecar message straight from these events.
func TestNodeGeometryLabelSidecar(t *testing.T) {
	// "src" carries an explicit human label; "dst" omits data.label → label falls back to id.
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","data":{"label":"Source Node"},"outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Resend re-emits held geometry (the remount-recovery path the sidecar rides).
	// Called twice to lock idempotent re-emission (matches TestResendGeometry).
	md.ResendGeometry(ctx, tr)
	md.ResendGeometry(ctx, tr)

	tr.Close()

	// Expected label per node id: explicit data.label for src, id fallback for dst.
	wantLabel := map[string]string{"src": "Source Node", "dst": "dst"}

	// First-seen node id order == buffer node-row order. Collect it and verify each
	// node-geometry event's Label matches, and that resend re-emitted every node.
	var firstSeen []string
	seen := map[string]bool{}
	reemitted := map[string]int{}
	for _, e := range tr.Events() {
		if e.Kind != T.KindNodeGeometry {
			continue
		}
		reemitted[e.Node]++
		if !seen[e.Node] {
			seen[e.Node] = true
			firstSeen = append(firstSeen, e.Node)
		}
		if want := wantLabel[e.Node]; e.Label != want {
			t.Fatalf("node %q: label = %q, want %q", e.Node, e.Label, want)
		}
	}
	if len(firstSeen) != 2 {
		t.Fatalf("first-seen node order = %v, want 2 distinct nodes", firstSeen)
	}
	for _, n := range []string{"src", "dst"} {
		if reemitted[n] < 2 {
			t.Fatalf("node %q re-emitted %d times, want >= 2 (startup + resend)", n, reemitted[n])
		}
	}
}

// TestResendGeometryEmitsFullBufferSnapshot locks the new-system (agnostic-content-buffer)
// remount recovery: a freshly (re)loaded webview mounts AFTER Go's startup snapshot burst,
// and an idle/paused sim emits no further buffer snapshots — so the fresh webview would
// receive no node geometry and render nothing. The recovery is the existing "resend"
// bridge kind: ResendGeometry re-emits held node/edge geometry through the Trace, whose
// sink hook is Buffer.SnapshotState.Update (wired exactly as main.go does), so a single
// resend produces a FULL buffer snapshot (full-state by construction) even though the sim
// never advanced (no KindPosition events). This test wires that path end-to-end and asserts
// a full framed snapshot containing the current node geometry lands on the fd3 sink.
func TestResendGeometryEmitsFullBufferSnapshot(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	// fd3 sink: the Buffer.SnapshotState writes framed binary snapshots here, exactly as
	// main.go wires os.NewFile(3). Trace's onEvent hook is snapState.Update — the single
	// seam through which geometry events become buffer snapshots.
	var snapOut bytes.Buffer
	snapState := B.NewSnapshotState(&snapOut)
	tr := T.NewWithSinkHook(256, io.Discard, snapState.Update)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// The sim is IDLE: a FakeClock that never ticks and no node ran, so there are zero
	// KindPosition events — the ONLY snapshots come from geometry emits. Whatever startup
	// burst preceded this, the resend re-emits held geometry, and we assert on the FINAL
	// frame, whose full-state contents are exactly what a freshly mounted webview receives.
	md.ResendGeometry(ctx, tr)

	// Close flushes the drain goroutine: every onEvent hook has run and snapOut is safe to read.
	tr.Close()

	frames := splitBufferFrames(t, snapOut.Bytes())
	if len(frames) == 0 {
		t.Fatal("idle resend emitted no buffer snapshot frames; a fresh webview would render nothing")
	}
	// The final frame is full-state: assert it carries both nodes with their geometry.
	last := frames[len(frames)-1]
	if len(last) < B.BufHeaderSize {
		t.Fatalf("final frame too short: %d bytes", len(last))
	}
	nodeCount := binary.LittleEndian.Uint32(last[8:])
	if nodeCount != 2 {
		t.Fatalf("final buffer snapshot nodeCount: got %d, want 2 (full node geometry not present)", nodeCount)
	}
	// Spot-check that node geometry is real (non-zero radius), i.e. actual held state was emitted.
	if binary.LittleEndian.Uint32(last[4:]) != 0 {
		t.Fatalf("idle sim should have 0 beads, got %d", binary.LittleEndian.Uint32(last[4:]))
	}
	nodeBlock := last[B.BufHeaderSize:] // beadCount is 0 (idle sim), so nodes start right after header
	// Node row layout: [cx,cy,cz,radius,...] as float32; radius is column index 3.
	radius := math.Float32frombits(binary.LittleEndian.Uint32(nodeBlock[3*4:]))
	if radius <= 0 {
		t.Fatalf("first node radius: got %v, want > 0 (geometry not populated in resend snapshot)", radius)
	}
}

// splitBufferFrames decodes the [len:u32-LE][payload] framing that Buffer.emitSnapshot
// writes, mirroring the extension host's splitFrames. Fails the test on a truncated frame.
func splitBufferFrames(t *testing.T, buf []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	for len(buf) >= 4 {
		n := int(binary.LittleEndian.Uint32(buf[:4]))
		buf = buf[4:]
		if len(buf) < n {
			t.Fatalf("truncated buffer frame: need %d bytes, have %d", n, len(buf))
		}
		frames = append(frames, buf[:n])
		buf = buf[n:]
	}
	return frames
}

// TestMoverCenterRace is a -race regression for the data race between the mover
// goroutines writing geom.Center/ReachR and the stdin goroutine reading those fields
// via centerOfNode/heldCenters/fanCenters/ResendGeometry. It hammers
// RootMove (which triggers fanCenters and heldCenters) and ResendGeometry from one
// goroutine while center messages flow concurrently through the mover goroutines.
// Must pass cleanly under `go test -race`.
func TestMoverCenterRace(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(4096)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines

	// Hammer concurrently: center messages via RootMove (fanCenters + heldCenters)
	// and ResendGeometry from the "stdin goroutine" side, while the mover goroutines
	// are writing geom.Center/ReachR on the other side.
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			x := float64(i) * 0.5
			md.RootMove("src", vec3{X: x, Y: 0, Z: 0})
			md.ResendGeometry(ctx, tr)
		}
	}()
	wg.Wait()
	tr.Close()
}

// TestOutGeomRace is a -race regression for the data race between the edgeMover
// goroutine writing the source Out's per-edge geometry (ArcLength/SimLatencyMs/
// Start/End) in recomputeGeometry and the SOURCE NODE goroutine reading those four
// fields in Out.placement()/PlaceDriven during bead placement. Before the published
// snapshot they were bare struct fields written/read with no synchronization; this
// test hammers RootMove (→ edgeMover.recomputeGeometry) on one goroutine while
// placement reads flow on another. Must pass cleanly under `go test -race`.
func TestOutGeomRace(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(4096)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines

	out := md.EdgeOut("e0")
	if out == nil {
		t.Fatal("missing Out e0")
	}

	const iters = 200
	var wg sync.WaitGroup
	// Writer side: RootMove fans a center → the edgeMover recomputes and publishes
	// the source Out's geometry.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			md.RootMove("src", vec3{X: float64(i) * 0.5, Y: 0, Z: 0})
		}
	}()
	// Reader side: read the four published geometry fields via placement(), exactly as
	// the source node goroutine does when placing a bead.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			bp := out.placement()
			_ = bp.InFlightMs
			_ = bp.Start
			_ = bp.End
		}
	}()
	wg.Wait()
	tr.Close()
}
