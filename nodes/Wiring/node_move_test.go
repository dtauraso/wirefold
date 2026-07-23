// node_move_test.go — decentralized node-move path.
//
// Locks that a node-move handled WITHOUT a central coordinator reproduces the old
// applyNodeMove result per-goroutine: the moved node re-emits its node-geometry, and
// each incident edge recomputes its own segment/arc, re-emits its edge geometry,
// revises any in-flight bead, and updates the dest port's latency aggregate. The
// move is delivered exactly as the live bridge does — by mail-sorting each entry onto
// the node's own extIn channel and every incident edge's own extIn channel.

package Wiring

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// deliver mail-sorts a node-move to the node's extIn channel + every incident edge's
// extIn channel, each with an ack the mover closes when done, then waits — mirroring the
// live stdin-reader's direct external entries but blocking so the test can assert
// deterministically. deliver sends "center" messages (the sphere-chain position model)
// to the node's extIn + every incident edge's extIn, blocking on acks.
func deliver(md *MoveDispatch, nodeID string, x, y, z float64) {
	center := &vec3{X: x, Y: y, Z: z}
	acks := make([]chan struct{}, 0, len(md.edgeMovers)+1)
	nodeAck := make(chan struct{})
	md.nodeMovers[nodeID].extIn <- moveMsg{Kind: moveMsgKindCenter, NodeID: nodeID, Center: center, testDone: nodeAck}
	acks = append(acks, nodeAck)
	for _, em := range md.edgeMovers {
		if em.srcID != nodeID && em.dstID != nodeID {
			continue
		}
		ack := make(chan struct{})
		em.extIn <- moveMsg{Kind: moveMsgKindCenter, NodeID: nodeID, Center: center, testDone: ack}
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
	tr := T.New(0)
	clk := NewRealClock()
	_, slotReg, md, _, err := LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	// Decentralized (Step C, per-owner-buffer-rows.md): wire src's/e0's own dedicated
	// stream frames directly (bypassing the fd/os.NewFile machinery SetNodeStreams/
	// SetEdgeStreams use in production) so this test can observe their own row-resolved
	// RowEvents on move, mirroring the retired central Trace event assertions below.
	var nodeEvents, edgeEvents []RowEvent
	var nodeMu, edgeMu sync.Mutex
	md.nodeMovers["src"].streamOut = io.Discard
	md.nodeMovers["src"].buildFrame = func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []RowEvent) []byte {
		nodeMu.Lock()
		nodeEvents = append(nodeEvents, events...)
		nodeMu.Unlock()
		return nil
	}
	md.edgeMovers["e0"].streamOut = io.Discard
	md.edgeMovers["e0"].buildFrame = func(tick uint32, srcPortRow, dstPortRow int32, selected uint8, label string, beadVal []int32, beadX, beadY, beadZ []float32, events []RowEvent) []byte {
		edgeMu.Lock()
		edgeEvents = append(edgeEvents, events...)
		edgeMu.Unlock()
		return nil
	}
	md.Start(ctx) // launch the per-node and per-edge goroutines

	out := md.EdgeOut("e0")
	pw := slotReg["dst.In"]
	if out == nil || pw == nil {
		t.Fatalf("missing Out/wire: out=%v pw=%v", out, pw)
	}

	// Place a bead on the wire so the move must revise it in flight. md.Start above
	// already launched this wire's own goroutine (edgeMover.run), which self-drives
	// the wire on its own clock copy — no manual driving needed from the test.
	seg0 := wireSegment{Start: out.Geom().Start, End: out.Geom().End}
	bp := beadPlacement{InFlightMs: out.Geom().SimLatencyMs, Start: seg0.Start, End: seg0.End, Node: "src", Port: "Out"}
	if pw.Send(7, bp) != SendPlaced {
		t.Fatal("Send rejected on fresh wire")
	}
	// Give the wire's own goroutine a moment to drain the send into its inflight
	// state (its next DriveOneCycle, at most one human-clock cycle away) before
	// the move below asks it to revise that bead's geometry.
	time.Sleep(cascadeSettle)

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
	srcGeom := nodeGeom{nodeIdentity: nodeIdentity{Kind: "FanInSrc"}, HasPos: true, ScenePolar: cart2polar(srcCenter), Outputs: []portGeom{{Name: "Out"}}}
	dstGeom := nodeGeom{nodeIdentity: nodeIdentity{Kind: "FanInSink"}, HasPos: true, ScenePolar: cart2polar(dstCenter), Inputs: []portGeom{{Name: "In"}}}
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

	// In-flight bead's geometry was revised to the new segment (still in flight).
	// pw is owned exclusively by its own goroutine now (the edgeMover md.Start
	// launched) — read its state via the atomic InFlightSegments snapshot rather
	// than the live (unexported, unlocked) inflight slice.
	//
	// deliver() acks once the edgeMover has recomputed out.Geom, but the wire
	// republishes its in-flight-segment snapshot (publishSnap) on its OWN next
	// DriveOneCycle — one human-clock cycle after the revision, not at ack time.
	// So poll the atomic snapshot until the revision is observable rather than
	// reading it once (flake fix: wait on the real happens-before edge — the
	// published revision — instead of assuming it lands synchronously with the ack).
	var revisedSeg wireSegment
	deadline := time.Now().Add(2 * time.Second)
	for {
		segs := pw.InFlightSegments()
		if len(segs) == 0 {
			t.Fatal("bead left flight unexpectedly during move")
		}
		revisedSeg = segs[0]
		if approxEq(revisedSeg.End.X, wantSeg.End.X) && approxEq(revisedSeg.Start.X, wantSeg.Start.X) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("in-flight segment not revised within timeout: got %+v..%+v, want %+v..%+v",
				revisedSeg.Start, revisedSeg.End, wantSeg.Start, wantSeg.End)
		}
		time.Sleep(time.Millisecond)
	}

	// Give the goroutines a moment, then assert the node re-emitted its own
	// node-geometry RowEvent and the edge re-emitted its own geometry RowEvent.
	time.Sleep(5 * time.Millisecond)
	nodeMu.Lock()
	sawNodeGeom := false
	for _, e := range nodeEvents {
		if e.Kind == T.KindNodeGeometry {
			sawNodeGeom = true
		}
	}
	nodeMu.Unlock()
	edgeMu.Lock()
	sawEdgeGeom := false
	for _, e := range edgeEvents {
		if e.Kind == T.KindGeometry {
			sawEdgeGeom = true
		}
	}
	edgeMu.Unlock()
	if !sawNodeGeom {
		t.Fatal("node 'src' did not re-emit node-geometry on move")
	}
	if !sawEdgeGeom {
		t.Fatal("edge 'e0' did not re-emit its re-derived segment on move")
	}
}

// TestNodeGeometryLabelSidecar locks the new-system label sidecar contract at the Go
// layer: every node carries a Label (data.label when present, else the node id) and a
// Kind (the node's `type` field), the values each nodeMover's own dedicated stream frame
// packs (node_mover.go's writeStreamFrame) for the row-keyed {id,label}/kind→color
// sidecars. Read directly off each nodeMover's held geom — the same fields
// writeStreamFrame reads — rather than through the retired central Trace event path.
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
	tr := T.New(0)
	_, _, md, _, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Expected label per node id: explicit data.label for src, id fallback for dst.
	wantLabel := map[string]string{"src": "Source Node", "dst": "dst"}
	// Expected Go kind per node id: the node's `type` field, carried for the
	// new-system kind→color sidecar (row-keyed).
	wantKind := map[string]string{"src": "FanInSrc", "dst": "FanInSink"}

	seen := map[string]bool{}
	for _, nm := range md.nodeMovers {
		seen[nm.id] = true
		label := nm.geom.Label
		if label == "" {
			label = nm.id
		}
		if want := wantLabel[nm.id]; label != want {
			t.Fatalf("node %q: label = %q, want %q", nm.id, label, want)
		}
		if want := wantKind[nm.id]; nm.geom.Kind != want {
			t.Fatalf("node %q: kind = %q, want %q", nm.id, nm.geom.Kind, want)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("saw %d distinct nodes, want 2", len(seen))
	}
}

// TestMoverCenterRace is a -race regression for the data race between the mover
// goroutines writing geom.ScenePolar/ReachR and the stdin goroutine reading those fields
// via centerOfNode/heldCenters/fanCenters. It hammers
// RootMove (which triggers fanCenters and heldCenters) from one
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
	_, _, md, _, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines

	// Hammer concurrently: center messages via RootMove (fanCenters + heldCenters)
	// from the "stdin goroutine" side, while the mover goroutines
	// are writing geom.ScenePolar/ReachR on the other side.
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			x := float64(i) * 0.5
			md.RootMove("src", vec3{X: x, Y: 0, Z: 0})
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
	_, _, md, _, err := LoadTopology(ctx, path, tr, NewRealClock())
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
	_, _, md, _, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx) // launch mover goroutines so fanCenters' center messages are drained

	// Sync points for the two LayoutHolder reads below, both racy against a live mover
	// goroutine if read via a bare poll (data race under -race, not just theoretically):
	//   - lhSrc is written by src's OWN mover goroutine (requantizeLocalPolars) strictly
	//     BEFORE that same call enqueues src's moveMsgKindNeighborSetC to dst — waiting
	//     for the tapped message (captureNeighborSetC, same mechanism drag_anchor_test.go
	//     uses) establishes a happens-before edge for lhSrc's write.
	//   - lhDst is written by dst's OWN mover goroutine (neighborSetCRequantize) strictly
	//     BEFORE that same call logs its "abc-drag" breadcrumb — waiting for the
	//     breadcrumb (same mechanism time_node_abc_drag_breadcrumb_test.go uses)
	//     establishes a happens-before edge for lhDst's write.
	got := captureNeighborSetC(md, "dst")
	var dbg syncBuffer
	tr.SetDebugSink(&dbg)

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
	waitForNeighborSetC(t, got, 1)

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

	// Both ends updated: dst also carries a fresh local polar back to src. src's
	// moveMsgKindNeighborSetC to dst is delivered and processed on dst's OWN
	// nodeMover goroutine, which drains its inbox non-blockingly and paces on its own
	// clock cycle rather than waking instantly on receive — wait for dst's own
	// "abc-drag" breadcrumb (fired on dst's own goroutine, after dst's own
	// SetLocalPolar/SetPole write) rather than polling lhDst directly from this
	// goroutine, which would be a data race against dst's mover goroutine.
	lhDst, ok := md.layoutHolders["dst"]
	if !ok {
		t.Fatal("no LayoutHolder registered for dst")
	}
	wantPolBack := cart2polar(target.sub(dstCenter))
	wantIRBack := math.Round(wantPolBack.R / rStep)

	waitForAbcDrag(t, &dbg, "dst")
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
	if float64(foundBack.QuantIR) != wantIRBack {
		t.Fatalf("dst.localPolar[src].QuantIR = %d, want round(%v/%v) = %v", foundBack.QuantIR, wantPolBack.R, rStep, wantIRBack)
	}
}
