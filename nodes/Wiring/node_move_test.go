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
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	_, slotReg, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
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
	seg0 := wireSegment{Start: out.Start, End: out.End}
	bp := beadPlacement{InFlightMs: out.SimLatencyMs, Start: seg0.Start, End: seg0.End, Node: "src", Port: "Out"}
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
		{NodeID: "dst", PortName: "In", IsInput: true}:  "src",
	}
	wantCenters := map[string]vec3{"src": srcCenter, "dst": dstCenter}
	wantCenterOf := func(id string) (vec3, bool) { c, ok := wantCenters[id]; return c, ok }
	wantSeg := segmentBetweenPortsAimed(srcGeom, "Out", "src", dstGeom, "In", "dst", wantReg, wantCenterOf)
	wantArc := wantSeg.Start.sub(wantSeg.End).length()

	// Edge mover wrote the new segment/arc onto the source Out.
	if !approxEq(out.ArcLength, wantArc) || !approxEq(out.SimLatencyMs, wantArc/PulseSpeedWuPerMs) {
		t.Fatalf("Out arc/lat = %v/%v, want %v/%v", out.ArcLength, out.SimLatencyMs, wantArc, wantArc/PulseSpeedWuPerMs)
	}
	if !approxEq(out.End.X, wantSeg.End.X) || !approxEq(out.Start.X, wantSeg.Start.X) {
		t.Fatalf("Out segment = %+v..%+v, want %+v..%+v", out.Start, out.End, wantSeg.Start, wantSeg.End)
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
	_, _, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Safe to call repeatedly; call twice to lock idempotency-while-running.
	md.ResendGeometry(tr)
	md.ResendGeometry(tr)

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
	_, _, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
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
			md.ResendGeometry(tr)
		}
	}()
	wg.Wait()
	tr.Close()
}
