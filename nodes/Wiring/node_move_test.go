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
	// Initial positions are scene polar about the origin: src (100,0,0), dst (0,0,0).
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","scenePolarR":100,"scenePolarTheta":1.5707963267948966,"scenePolarPhi":0,"outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0,"inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, slotReg, md, err := LoadTopology(ctx, path, tr, NewRealClock())
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
	// dst's authoritative center comes from the quantized-layout compose (Phase 3): dst is
	// src's child in the spanning tree, so its post-load center is composed from a SNAPPED
	// offset about src, not necessarily the raw authored (0,0,0) — read it back rather than
	// assume the pre-quantized value.
	dstCenterHeld, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("dst has no center after load")
	}
	srcCenter := vec3{X: nx, Y: ny, Z: nz}
	dstCenter := dstCenterHeld
	srcGeom := nodeGeom{Kind: "FanInSrc", HasPos: true, ScenePolar: cart2polar(srcCenter), Outputs: []portGeom{{Name: "Out"}}}
	dstGeom := nodeGeom{Kind: "FanInSink", HasPos: true, ScenePolar: cart2polar(dstCenter), Inputs: []portGeom{{Name: "In"}}}
	// Polar-torus port-to-port model: segment/arc between the two ports' world points.
	wantSeg := edgeSegment(srcGeom, dstGeom, "Out", "In")
	wantArc := edgeArcPolar(srcGeom, dstGeom, "Out", "In")

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
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
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
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
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
	// Expected Go kind per node id: the node's `type` field, carried on node-geometry
	// for the new-system kind→color sidecar (row-keyed, re-emitted on resend).
	wantKind := map[string]string{"src": "FanInSrc", "dst": "FanInSink"}

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
		if want := wantKind[e.Node]; e.NodeKind != want {
			t.Fatalf("node %q: nodeKind = %q, want %q", e.Node, e.NodeKind, want)
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

	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// The sim is IDLE: no node ran, so there are zero
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
// goroutines writing geom.ScenePolar/ReachR and the stdin goroutine reading those fields
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
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines

	// Hammer concurrently: center messages via RootMove (fanCenters + heldCenters)
	// and ResendGeometry from the "stdin goroutine" side, while the mover goroutines
	// are writing geom.ScenePolar/ReachR on the other side.
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
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
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

// TestRootMoveContinuousPositionLocalPolarRequantize verifies the double-link
// local-polar drag model (CLAUDE.md task/double-link-local-polar): (a) the dragged
// node's world center is the raw drag target — NOT snapped to the scene-sphere grid —
// and (b) for each neighbor, the dragged node's local polar to that neighbor lands on
// a WHOLE tick of the neighbor-specific small grid, on BOTH ends of the double link.
func TestRootMoveContinuousPositionLocalPolarRequantize(t *testing.T) {
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
	defer tr.Close()
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines so fanCenters' center messages are drained

	// A target deliberately off any scene-grid cell.
	target := vec3{X: 37.3, Y: 12.1, Z: -5.7}
	if !md.RootMove("src", target) {
		t.Fatal("RootMove returned false for known node")
	}

	// (a) The dragged node's world center is the continuous target, unsnapped.
	// The nodeMover applies the center on its own goroutine; poll centerOfNode's
	// atomic snapshot briefly rather than assuming synchronous delivery.
	const eps = 1e-9
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, ok := md.centerOfNode("src")
		if ok && math.Abs(c.X-target.X) <= eps && math.Abs(c.Y-target.Y) <= eps && math.Abs(c.Z-target.Z) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dragged node center never converged to unsnapped target %+v (last seen %+v, ok=%v)", target, c, ok)
		}
		time.Sleep(time.Millisecond)
	}

	// (b) src's local polar to dst reconstructs the distance to a whole tick of the
	// LOCAL-POLAR grid (localStepR/localStepTheta/localStepPhi — small, uniform cells,
	// distinct from the coarser scene-center stepR/stepTheta/stepPhi).
	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder registered for src")
	}
	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("centerOfNode(dst) missing")
	}
	wantPol := cart2polar(dstCenter.sub(target))
	tStep, pStep, rStep := LocalPolar{}.effectiveSteps()
	if tStep != localStepTheta || pStep != localStepPhi || rStep != localStepR {
		t.Fatalf("local-polar default steps = (%v,%v,%v), want (%v,%v,%v)", tStep, pStep, rStep, localStepTheta, localStepPhi, localStepR)
	}
	wantIR := math.Round(wantPol.R / rStep)

	var found *LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			cp := lp
			found = &cp
			break
		}
	}
	if found == nil {
		t.Fatal("src has no local polar entry for dst after RootMove")
	}
	if float64(found.QuantIR) != wantIR {
		t.Fatalf("src.localPolar[dst].QuantIR = %d, want round(%v/%v) = %v", found.QuantIR, wantPol.R, rStep, wantIR)
	}
	// The reconstructed distance is within half a cell of the raw measured distance —
	// i.e. it landed on a WHOLE tick, not a fraction.
	gotR := float64(found.QuantIR) * rStep
	if math.Abs(gotR-wantPol.R) > rStep/2+eps {
		t.Fatalf("src.localPolar[dst] R = %v (iR=%d*step=%v), want within half a cell of measured %v", gotR, found.QuantIR, rStep, wantPol.R)
	}

	// Both ends updated: dst also carries a fresh local polar back to src.
	lhDst, ok := md.layoutHolders["dst"]
	if !ok {
		t.Fatal("no LayoutHolder registered for dst")
	}
	var foundBack *LocalPolar
	for _, lp := range lhDst.LocalPolarsSnapshot() {
		if lp.To == "src" {
			cp := lp
			foundBack = &cp
			break
		}
	}
	if foundBack == nil {
		t.Fatal("dst has no local polar entry for src after RootMove")
	}
	wantPolBack := cart2polar(target.sub(dstCenter))
	wantIRBack := math.Round(wantPolBack.R / rStep)
	if float64(foundBack.QuantIR) != wantIRBack {
		t.Fatalf("dst.localPolar[src].QuantIR = %d, want round(%v/%v) = %v", foundBack.QuantIR, wantPolBack.R, rStep, wantIRBack)
	}
}

// mockPulseSink is a minimal single-In kind registering the "Pulse" kind for the
// Wiring test binary (loader_scene_polar_test.go / loader_tree_test.go load "Pulse"
// nodes and need it registered). Its Update is a no-op like faninSrc/faninSink in
// fanin_travel_time_test.go — no bead traffic is driven by it. "Hold" and
// "HoldNewSendOld" are already registered by their real node packages (imported by
// other _test.go files sharing this test binary via nonblocking_traversal_test.go /
// gate_nonblocking_traversal_test.go), so tests reuse those real kinds instead of
// re-registering mocks (Register panics on a duplicate kind).
type mockPulseSink struct {
	LayoutHolder
	In *In
}

func (n *mockPulseSink) Update(ctx context.Context) { <-ctx.Done() }

func init() {
	Register("Pulse", func() any { return &mockPulseSink{} })
}

// TestRootMoveNode2CascadesToSource verifies the drag cascade in RootMove: dragging
// node "2" (a HoldNewSendOld node, same kind as node 5) whose source is node "5" must,
// after equalizing 2's own peers, make node 5 "act like it was dragged" too —
// re-running node 5's OWN hardcoded nodeID=="5" equalize (source "2") at 5's current
// (unchanged) position, so 5's other peers ("7","8", node 5's double-link equalize
// target set) reposition to the NEW 2<->5 distance. Node ids are literally
// "2"/"5"/"7"/"8" so the nodeID=="2" (source "5") and nodeID=="5" (source "2")
// hardcodes in RootMove drive the equalize and its one-level cascade. Built via
// newMoveDispatch directly (mirroring TestNode5DragEqualizesNeighborDistances in
// node5_equalize_test.go), NOT via LoadTopology's quantized-layout JSON round trip,
// which would requantize the hand-picked view coordinates onto a coarse grid.
func TestRootMoveNode2CascadesToSource(t *testing.T) {
	geoms := map[string]nodeGeom{
		"2": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Outputs: []portGeom{{Name: "outT"}}},
		"5": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}), Inputs: []portGeom{{Name: "inT"}}, Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"7": {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"2To5": {Source: "2", Target: "5", SourceHandle: "outT", TargetHandle: "inT"},
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"2": {}, "5": {}, "7": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	fiveCenter, ok := md.centerOfNode("5")
	if !ok {
		t.Fatal("centerOfNode(5) missing before move")
	}
	sevenCenterBefore, ok := md.centerOfNode("7")
	if !ok {
		t.Fatal("centerOfNode(7) missing before move")
	}
	eightCenterBefore, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing before move")
	}

	target := vec3{X: 5, Y: 5, Z: 5}
	if !md.RootMove("2", target) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c, ok := md.centerOfNode("2")
		if !ok || math.Abs(c.X-target.X) > eps || math.Abs(c.Y-target.Y) > eps || math.Abs(c.Z-target.Z) > eps {
			return false
		}
		c7, ok7 := md.centerOfNode("7")
		c8, ok8 := md.centerOfNode("8")
		return ok7 && ok8 && c7 != sevenCenterBefore && c8 != eightCenterBefore
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("drag + cascade never converged")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// Node 5 (the time neighbor / cascade target) itself stays put — only its OTHER
	// peers (7, 8) reposition.
	fiveCenterAfter, ok := md.centerOfNode("5")
	if !ok {
		t.Fatal("centerOfNode(5) missing after move")
	}
	if fiveCenterAfter != fiveCenter {
		t.Fatalf("time neighbor '5' moved: got %+v, want unchanged %+v", fiveCenterAfter, fiveCenter)
	}

	wantDist := cart2polar(fiveCenterAfter.sub(target)).R

	sevenCenterAfter, ok := md.centerOfNode("7")
	if !ok {
		t.Fatal("centerOfNode(7) missing after move")
	}
	gotDist7 := cart2polar(sevenCenterAfter.sub(fiveCenterAfter)).R
	if math.Abs(gotDist7-wantDist) > eps {
		t.Fatalf("dist(5,7) after cascade = %v, want dist(2,5) = %v", gotDist7, wantDist)
	}
	wantBearing7 := cart2polar(sevenCenterBefore.sub(fiveCenter))
	gotBearing7 := cart2polar(sevenCenterAfter.sub(fiveCenterAfter))
	if math.Abs(gotBearing7.Theta-wantBearing7.Theta) > eps || math.Abs(gotBearing7.Phi-wantBearing7.Phi) > eps {
		t.Fatalf("7's bearing from 5 changed: got (theta=%v,phi=%v), want (theta=%v,phi=%v)",
			gotBearing7.Theta, gotBearing7.Phi, wantBearing7.Theta, wantBearing7.Phi)
	}

	eightCenterAfter, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing after move")
	}
	gotDist8 := cart2polar(eightCenterAfter.sub(fiveCenterAfter)).R
	if math.Abs(gotDist8-wantDist) > eps {
		t.Fatalf("dist(5,8) after cascade = %v, want dist(2,5) = %v", gotDist8, wantDist)
	}
	wantBearing8 := cart2polar(eightCenterBefore.sub(fiveCenter))
	gotBearing8 := cart2polar(eightCenterAfter.sub(fiveCenterAfter))
	if math.Abs(gotBearing8.Theta-wantBearing8.Theta) > eps || math.Abs(gotBearing8.Phi-wantBearing8.Phi) > eps {
		t.Fatalf("8's bearing from 5 changed: got (theta=%v,phi=%v), want (theta=%v,phi=%v)",
			gotBearing8.Theta, gotBearing8.Phi, wantBearing8.Theta, wantBearing8.Phi)
	}
}

// TestRootMoveNode5DragCascadesToNode6 verifies the mirror direction of the node-2/
// node-5 cascade: dragging node "5" (source "2") must, after equalizing 5's own peers
// (7,8), make node 2 "act like it was dragged" too — re-running node 2's OWN
// hardcoded nodeID=="2" equalize (source "5") at 2's current (unchanged) position, so
// node 2's OTHER peer "6" (previously untouched by a node-5 drag, back when the
// cascade was one-directional) repositions to the new 2<->5 distance along its
// current bearing from node 2. Node 2 itself stays put (only its peer set is
// re-equalized), and node 1/node 3 are not reached (this test omits them; the
// node-2-origin path to node 1 is covered by TestRootMoveNode2CascadeKeepsNode1EdgesEqual).
// Built via newMoveDispatch directly, mirroring TestRootMoveNode2CascadesToSource.
func TestRootMoveNode5DragCascadesToNode6(t *testing.T) {
	geoms := map[string]nodeGeom{
		"5": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "inT"}}, Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"2": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}), Outputs: []portGeom{{Name: "outT"}, {Name: "out6"}}},
		"7": {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
		"6": {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{60, 15, -10}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
		"2To5": {Source: "2", Target: "5", SourceHandle: "outT", TargetHandle: "inT"},
		"2To6": {Source: "2", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"5": {}, "2": {}, "7": {}, "8": {}, "6": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	twoCenter, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("centerOfNode(2) missing before move")
	}
	sixCenterBefore, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing before move")
	}

	target := vec3{X: 5, Y: 5, Z: 5}
	if !md.RootMove("5", target) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c, ok := md.centerOfNode("5")
		if !ok || math.Abs(c.X-target.X) > eps || math.Abs(c.Y-target.Y) > eps || math.Abs(c.Z-target.Z) > eps {
			return false
		}
		c6, ok6 := md.centerOfNode("6")
		return ok6 && c6 != sixCenterBefore
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("drag + cascade never converged")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// Node 2 (the source / cascade target) itself stays put — only its peer (6)
	// repositions.
	twoCenterAfter, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("centerOfNode(2) missing after move")
	}
	if twoCenterAfter != twoCenter {
		t.Fatalf("source node '2' moved: got %+v, want unchanged %+v", twoCenterAfter, twoCenter)
	}

	wantDist := cart2polar(target.sub(twoCenterAfter)).R

	sixCenterAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	gotDist6 := cart2polar(sixCenterAfter.sub(twoCenterAfter)).R
	if math.Abs(gotDist6-wantDist) > eps {
		t.Fatalf("dist(2,6) after cascade = %v, want dist(2,5) = %v", gotDist6, wantDist)
	}
	wantBearing6 := cart2polar(sixCenterBefore.sub(twoCenter))
	gotBearing6 := cart2polar(sixCenterAfter.sub(twoCenterAfter))
	if math.Abs(gotBearing6.Theta-wantBearing6.Theta) > eps || math.Abs(gotBearing6.Phi-wantBearing6.Phi) > eps {
		t.Fatalf("6's bearing from 2 changed: got (theta=%v,phi=%v), want (theta=%v,phi=%v)",
			gotBearing6.Theta, gotBearing6.Phi, wantBearing6.Theta, wantBearing6.Phi)
	}
}

// TestRootMoveNode2CascadeKeepsNode1EdgesEqual verifies the node-1 side of the same
// drag cascade: node "1" is EXCLUDED from node 2's own equalize peer set (node 1
// ignores node 2's r update and stays put at its pre-drag position), but rootMove's
// case "2" cascades into node 1's OWN hardcoded nodeID=="1" equalize (source "2"),
// which repositions node 1's other peer "3" along its current bearing from node 1 so
// that dist(1,3) == the new organic dist(1,2). Built via newMoveDispatch directly
// (mirroring TestRootMoveNode2CascadesToSource), NOT via LoadTopology.
func TestRootMoveNode2CascadeKeepsNode1EdgesEqual(t *testing.T) {
	geoms := map[string]nodeGeom{
		"1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{10, 5, -8}), Inputs: []portGeom{{Name: "inT"}}, Outputs: []portGeom{{Name: "out3"}}},
		"2": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in1"}}, Outputs: []portGeom{{Name: "out5"}}},
		"3": {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{35, 20, 12}), Inputs: []portGeom{{Name: "in"}}},
		"5": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"2To1": {Source: "2", Target: "1", SourceHandle: "out5", TargetHandle: "inT"},
		"1To3": {Source: "1", Target: "3", SourceHandle: "out3", TargetHandle: "in"},
		"2To5": {Source: "2", Target: "5", SourceHandle: "out5", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"1": {}, "2": {}, "3": {}, "5": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	oneCenter, ok := md.centerOfNode("1")
	if !ok {
		t.Fatal("centerOfNode(1) missing before move")
	}
	threeCenterBefore, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing before move")
	}

	target := vec3{X: 5, Y: 5, Z: 5}
	if !md.RootMove("2", target) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c, ok := md.centerOfNode("2")
		if !ok || math.Abs(c.X-target.X) > eps || math.Abs(c.Y-target.Y) > eps || math.Abs(c.Z-target.Z) > eps {
			return false
		}
		c3, ok3 := md.centerOfNode("3")
		return ok3 && c3 != threeCenterBefore
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("drag + cascade never converged")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// Node 1 ignored node 2's r update: it stays put.
	oneCenterAfter, ok := md.centerOfNode("1")
	if !ok {
		t.Fatal("centerOfNode(1) missing after move")
	}
	if oneCenterAfter != oneCenter {
		t.Fatalf("node '1' moved: got %+v, want unchanged %+v", oneCenterAfter, oneCenter)
	}

	threeCenterAfter, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing after move")
	}
	if threeCenterAfter == threeCenterBefore {
		t.Fatal("node '3' did not move after node-1 cascade")
	}

	gotDist13 := cart2polar(threeCenterAfter.sub(oneCenterAfter)).R
	gotDist12 := cart2polar(target.sub(oneCenterAfter)).R
	if math.Abs(gotDist13-gotDist12) > eps {
		t.Fatalf("dist(1,3) after cascade = %v, want dist(1,2) = %v", gotDist13, gotDist12)
	}

	wantBearing3 := cart2polar(threeCenterBefore.sub(oneCenter))
	gotBearing3 := cart2polar(threeCenterAfter.sub(oneCenterAfter))
	if math.Abs(gotBearing3.Theta-wantBearing3.Theta) > eps || math.Abs(gotBearing3.Phi-wantBearing3.Phi) > eps {
		t.Fatalf("3's bearing from 1 changed: got (theta=%v,phi=%v), want (theta=%v,phi=%v)",
			gotBearing3.Theta, gotBearing3.Phi, wantBearing3.Theta, wantBearing3.Phi)
	}
}

// TestRootMoveNode9DragKeepsRadiiEqual verifies node 9's current drag behavior: node 9
// is NOT placed at the raw drag target. Instead rootMove constrains it, via
// placeNode9EqualRadii, to the point — about the scene center — where node 9's bearing
// from the scene center matches the drag's bearing (S -> target) but its RADIUS along
// that bearing is solved so its distances to the two FIXED neighbors 3 and 6 come out
// EQUAL. Node 3 and node 6 never move. node9DragEqualizeLocalC still runs afterward as
// node 9's own local-polar requantize (equalizing its two edge-c records to each other),
// unaffected by this change. Built via newMoveDispatch directly (mirroring
// TestRootMoveNode2CascadeKeepsNode1EdgesEqual), NOT via LoadTopology.
func TestRootMoveNode9DragKeepsRadiiEqual(t *testing.T) {
	geoms := map[string]nodeGeom{
		"9": {Kind: "WindowAndInhibitLeftGate", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"3": {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{10, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
		"6": {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 20}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"9To3": {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in"},
		"9To6": {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"9": {}, "3": {}, "6": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lh9 := md.layoutHolders["9"]

	nineBefore, ok := md.centerOfNode("9")
	if !ok {
		t.Fatal("centerOfNode(9) missing before move")
	}
	threeCenterBefore, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing before move")
	}
	sixCenterBefore, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing before move")
	}

	// Drag target whose bearing from the scene center (origin, the default
	// md.sceneSphere.Center) is NOT parallel to the 3-6 axis, so placeNode9EqualRadii
	// solves a genuine point rather than falling back to target.
	target := vec3{X: 5, Y: 5, Z: 5}

	// Independently compute the expected landing with the same formula
	// placeNode9EqualRadii uses: p = S + t*u, t solved so |p-a| == |p-b|.
	s := vec3{0, 0, 0} // md.sceneSphere.Center's zero-value default
	u := target.sub(s).normalize()
	ba := sixCenterBefore.sub(threeCenterBefore)
	denom := u.dot(ba)
	if denom == 0 {
		t.Fatal("test fixture bearing is parallel to the 3-6 axis — adjust positions/target")
	}
	tSolved := (sixCenterBefore.dot(sixCenterBefore)/2 - threeCenterBefore.dot(threeCenterBefore)/2 - s.dot(ba)) / denom
	if tSolved <= 0 {
		t.Fatal("test fixture solves a t <= 0 (behind scene center) — adjust positions/target")
	}
	expected := s.add(u.scale(tSolved))
	if expected == target {
		t.Fatal("test fixture's expected landing coincides with the raw target — adjust geometry to exercise the real solve")
	}

	if !md.RootMove("9", target) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c, ok := md.centerOfNode("9")
		return ok && math.Abs(c.X-expected.X) <= eps && math.Abs(c.Y-expected.Y) <= eps && math.Abs(c.Z-expected.Z) <= eps
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("node 9 drag never converged to the equal-radii point")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	nineAfter, ok := md.centerOfNode("9")
	if !ok {
		t.Fatal("centerOfNode(9) missing after move")
	}

	// (a) node 9 moved from its start.
	if nineAfter == nineBefore {
		t.Fatal("node 9 did not move")
	}

	// (c) node 9 landed exactly at placeNode9EqualRadii(target), independently computed.
	if math.Abs(nineAfter.X-expected.X) > eps || math.Abs(nineAfter.Y-expected.Y) > eps || math.Abs(nineAfter.Z-expected.Z) > eps {
		t.Fatalf("node 9 landing = %+v, want equal-radii solve %+v", nineAfter, expected)
	}
	if nineAfter == target {
		t.Fatalf("node 9 landed at the raw target %+v; expected the constrained equal-radii point %+v", target, expected)
	}

	// (d) node 3 and node 6 never moved.
	threeCenterAfter, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing after move")
	}
	if threeCenterAfter != threeCenterBefore {
		t.Fatalf("node 3 moved: got %+v, want unchanged %+v", threeCenterAfter, threeCenterBefore)
	}
	sixCenterAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	if sixCenterAfter != sixCenterBefore {
		t.Fatalf("node 6 moved: got %+v, want unchanged %+v", sixCenterAfter, sixCenterBefore)
	}

	// (b) THE KEY ASSERTION: node 9's two radii, to the fixed neighbors 3 and 6, are equal.
	dist3 := cart2polar(nineAfter.sub(threeCenterAfter)).R
	dist6 := cart2polar(nineAfter.sub(sixCenterAfter)).R
	if math.Abs(dist3-dist6) > eps {
		t.Fatalf("node 9's radii to 3 and 6 are not equal: dist(9,3)=%v, dist(9,6)=%v", dist3, dist6)
	}

	// (e) node 9's two edge-c records (LocalPolar QuantIR to 3 and to 6) are equal to
	// each other — the node9DragEqualizeLocalC requantize step.
	var got3, got6 *LocalPolar
	for _, lp := range lh9.LocalPolarsSnapshot() {
		cp := lp
		switch lp.To {
		case "3":
			got3 = &cp
		case "6":
			got6 = &cp
		}
	}
	if got3 == nil {
		t.Fatal("node 9 has no local polar entry for 3 after RootMove")
	}
	if got6 == nil {
		t.Fatal("node 9 has no local polar entry for 6 after RootMove")
	}
	if got3.QuantIR != got6.QuantIR {
		t.Fatalf("node9's two entries have different QuantIR: to3=%d to6=%d, want equal (shorter c)", got3.QuantIR, got6.QuantIR)
	}
}

// TestRootMoveNode10DragKeepsRadiiEqual mirrors TestRootMoveNode9DragKeepsRadiiEqual but
// for node 10's generalized equal-radii drag against its FIXED neighbors 6 and 8: rootMove
// constrains node 10, via placeEqualRadii, to the point — about the scene center — where
// node 10's bearing from the scene center matches the drag's bearing (S -> target) but its
// RADIUS along that bearing is solved so its distances to 6 and 8 come out EQUAL. Node 6
// and node 8 never move. equalizeEdgeCLocal still runs afterward as node 10's own
// local-polar requantize (equalizing its two edge-c records to each other).
func TestRootMoveNode10DragKeepsRadiiEqual(t *testing.T) {
	geoms := map[string]nodeGeom{
		"10": {Kind: "WindowAndInhibitRightGate", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
		"6":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{10, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
		"8":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 20}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"10": {}, "6": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lh10 := md.layoutHolders["10"]

	tenBefore, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("centerOfNode(10) missing before move")
	}
	sixCenterBefore, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing before move")
	}
	eightCenterBefore, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing before move")
	}

	// Drag target whose bearing from the scene center (origin, the default
	// md.sceneSphere.Center) is NOT parallel to the 6-8 axis, so placeEqualRadii
	// solves a genuine point rather than falling back to target.
	target := vec3{X: 5, Y: 5, Z: 5}

	// Independently compute the expected landing with the same formula
	// placeEqualRadii uses: p = S + t*u, t solved so |p-a| == |p-b|.
	s := vec3{0, 0, 0} // md.sceneSphere.Center's zero-value default
	u := target.sub(s).normalize()
	ba := eightCenterBefore.sub(sixCenterBefore)
	denom := u.dot(ba)
	if denom == 0 {
		t.Fatal("test fixture bearing is parallel to the 6-8 axis — adjust positions/target")
	}
	tSolved := (eightCenterBefore.dot(eightCenterBefore)/2 - sixCenterBefore.dot(sixCenterBefore)/2 - s.dot(ba)) / denom
	if tSolved <= 0 {
		t.Fatal("test fixture solves a t <= 0 (behind scene center) — adjust positions/target")
	}
	expected := s.add(u.scale(tSolved))
	if expected == target {
		t.Fatal("test fixture's expected landing coincides with the raw target — adjust geometry to exercise the real solve")
	}

	if !md.RootMove("10", target) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c, ok := md.centerOfNode("10")
		return ok && math.Abs(c.X-expected.X) <= eps && math.Abs(c.Y-expected.Y) <= eps && math.Abs(c.Z-expected.Z) <= eps
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("node 10 drag never converged to the equal-radii point")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	tenAfter, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("centerOfNode(10) missing after move")
	}

	// (a) node 10 moved from its start.
	if tenAfter == tenBefore {
		t.Fatal("node 10 did not move")
	}

	// (c) node 10 landed exactly at placeEqualRadii(target), independently computed.
	if math.Abs(tenAfter.X-expected.X) > eps || math.Abs(tenAfter.Y-expected.Y) > eps || math.Abs(tenAfter.Z-expected.Z) > eps {
		t.Fatalf("node 10 landing = %+v, want equal-radii solve %+v", tenAfter, expected)
	}
	if tenAfter == target {
		t.Fatalf("node 10 landed at the raw target %+v; expected the constrained equal-radii point %+v", target, expected)
	}

	// (d) node 6 and node 8 never moved.
	sixCenterAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	if sixCenterAfter != sixCenterBefore {
		t.Fatalf("node 6 moved: got %+v, want unchanged %+v", sixCenterAfter, sixCenterBefore)
	}
	eightCenterAfter, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing after move")
	}
	if eightCenterAfter != eightCenterBefore {
		t.Fatalf("node 8 moved: got %+v, want unchanged %+v", eightCenterAfter, eightCenterBefore)
	}

	// (b) THE KEY ASSERTION: node 10's two radii, to the fixed neighbors 6 and 8, are equal.
	dist6 := cart2polar(tenAfter.sub(sixCenterAfter)).R
	dist8 := cart2polar(tenAfter.sub(eightCenterAfter)).R
	if math.Abs(dist6-dist8) > eps {
		t.Fatalf("node 10's radii to 6 and 8 are not equal: dist(10,6)=%v, dist(10,8)=%v", dist6, dist8)
	}

	// (e) node 10's two edge-c records (LocalPolar QuantIR to 6 and to 8) are equal to
	// each other — the equalizeEdgeCLocal requantize step.
	var got6, got8 *LocalPolar
	for _, lp := range lh10.LocalPolarsSnapshot() {
		cp := lp
		switch lp.To {
		case "6":
			got6 = &cp
		case "8":
			got8 = &cp
		}
	}
	if got6 == nil {
		t.Fatal("node 10 has no local polar entry for 6 after RootMove")
	}
	if got8 == nil {
		t.Fatal("node 10 has no local polar entry for 8 after RootMove")
	}
	if got6.QuantIR != got8.QuantIR {
		t.Fatalf("node10's two entries have different QuantIR: to6=%d to8=%d, want equal (shorter c)", got6.QuantIR, got8.QuantIR)
	}
}

// TestRootMoveNode6DragResolves9And10 verifies node 6's new cascade: node 6 moves
// FREELY (the normal path — no equal-radii constraint on node 6 itself), landing
// exactly at the raw drag target. Because node 6 is the FIXED neighbor gate nodes 9
// (3,6) and 10 (6,8) solve their equal radii against, rootMove's case "6" cascades
// into 9 and 10 (each re-run at its own CURRENT center, with node 6's just-computed
// newPos as the sourceCenterOverride so their placeEqualRadii/equalizeEdgeCLocal read
// node 6's FRESH center instead of its async-lagged snapshot). Node 3 and node 8 —
// the other fixed neighbors of 9 and 10 respectively — never move.
func TestRootMoveNode6DragResolves9And10(t *testing.T) {
	geoms := map[string]nodeGeom{
		"6":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in"}, {Name: "in2"}}},
		"9":  {Kind: "WindowAndInhibitLeftGate", HasPos: true, ScenePolar: cart2polar(vec3{5, 5, 0}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"3":  {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{10, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
		"10": {Kind: "WindowAndInhibitRightGate", HasPos: true, ScenePolar: cart2polar(vec3{-6, 2, 3}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
		"8":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 10}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"9To3":  {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in"},
		"9To6":  {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in2"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"6": {}, "9": {}, "3": {}, "10": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	threeCenterBefore, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing before move")
	}
	eightCenterBefore, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing before move")
	}

	// Drag target for node 6, whose bearing from the scene center is not parallel to
	// either the 3-9-6 or the 6-10-8 equal-distance planes, so both gate solves are genuine.
	target6 := vec3{X: 3, Y: 4, Z: 5}

	if !md.RootMove("6", target6) {
		t.Fatal("RootMove returned false for known node")
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	converged := func() bool {
		c6, ok6 := md.centerOfNode("6")
		if !ok6 || math.Abs(c6.X-target6.X) > eps || math.Abs(c6.Y-target6.Y) > eps || math.Abs(c6.Z-target6.Z) > eps {
			return false
		}
		c9, ok9 := md.centerOfNode("9")
		c3, ok3 := md.centerOfNode("3")
		c10, ok10 := md.centerOfNode("10")
		c8, ok8 := md.centerOfNode("8")
		if !ok9 || !ok3 || !ok10 || !ok8 {
			return false
		}
		dist93 := cart2polar(c9.sub(c3)).R
		dist96 := cart2polar(c9.sub(c6)).R
		dist106 := cart2polar(c10.sub(c6)).R
		dist108 := cart2polar(c10.sub(c8)).R
		return math.Abs(dist93-dist96) <= eps && math.Abs(dist106-dist108) <= eps
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("node 6 drag never converged (node 6 at target AND node 9/10 equal-radii)")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// (a) node 6 moved FREELY to the raw drag target — no equal-radii constraint on node 6 itself.
	sixAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	if math.Abs(sixAfter.X-target6.X) > eps || math.Abs(sixAfter.Y-target6.Y) > eps || math.Abs(sixAfter.Z-target6.Z) > eps {
		t.Fatalf("node 6 landing = %+v, want raw target %+v", sixAfter, target6)
	}

	nineAfter, ok := md.centerOfNode("9")
	if !ok {
		t.Fatal("centerOfNode(9) missing after move")
	}
	threeCenterAfter, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing after move")
	}
	tenAfter, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("centerOfNode(10) missing after move")
	}
	eightCenterAfter, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing after move")
	}

	// (b) KEY: node 9's radii to 3 and to 6 (node 6's NEW center) are equal.
	dist93 := cart2polar(nineAfter.sub(threeCenterAfter)).R
	dist96 := cart2polar(nineAfter.sub(sixAfter)).R
	if math.Abs(dist93-dist96) > eps {
		t.Fatalf("node 9's radii to 3 and 6 are not equal after node-6 cascade: dist(9,3)=%v, dist(9,6)=%v", dist93, dist96)
	}

	// (c) KEY: node 10's radii to 6 (node 6's NEW center) and to 8 are equal.
	dist106 := cart2polar(tenAfter.sub(sixAfter)).R
	dist108 := cart2polar(tenAfter.sub(eightCenterAfter)).R
	if math.Abs(dist106-dist108) > eps {
		t.Fatalf("node 10's radii to 6 and 8 are not equal after node-6 cascade: dist(10,6)=%v, dist(10,8)=%v", dist106, dist108)
	}

	// (d) node 3 and node 8 centers unchanged.
	if threeCenterAfter != threeCenterBefore {
		t.Fatalf("node 3 moved: got %+v, want unchanged %+v", threeCenterAfter, threeCenterBefore)
	}
	if eightCenterAfter != eightCenterBefore {
		t.Fatalf("node 8 moved: got %+v, want unchanged %+v", eightCenterAfter, eightCenterBefore)
	}
}
