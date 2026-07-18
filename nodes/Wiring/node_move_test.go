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
	"math"
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
		md.dispatch[kk] <- moveMsg{Kind: moveMsgKindCenter, NodeID: nodeID, Center: center, testDone: ack}
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

// TestNodeGeometryLabelSidecar locks the new-system label sidecar contract at the Go
// layer: every node-geometry event carries a Label field (data.label when present, else
// the node id), and the labels arrive in node-row order (first-seen node-geometry order,
// == Buffer.SnapshotState insertion order). The webview host derives the {id,label}
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

	// LoadTopology builds the movers' held geometry but the node-geometry EmitGeometry
	// closure only fires from each node's OWN Update() goroutine (main.go), which this
	// headless test never starts (md.Start only launches the MoveDispatch movers, not
	// application node goroutines). Emit directly from the movers' held state — the same
	// direct read the removed ResendGeometry(!started) path used — so this test can assert
	// on Label/NodeKind without spinning up full node goroutines.
	for _, nm := range md.nodeMovers {
		emitNodeGeometryLocked(tr, nm.id, nm.geom, nm.partnerCenter)
	}

	tr.Close()

	// Expected label per node id: explicit data.label for src, id fallback for dst.
	wantLabel := map[string]string{"src": "Source Node", "dst": "dst"}
	// Expected Go kind per node id: the node's `type` field, carried on node-geometry
	// for the new-system kind→color sidecar (row-keyed).
	wantKind := map[string]string{"src": "FanInSrc", "dst": "FanInSink"}

	// First-seen node id order == buffer node-row order. Collect it and verify each
	// node-geometry event's Label matches.
	var firstSeen []string
	seen := map[string]bool{}
	for _, e := range tr.Events() {
		if e.Kind != T.KindNodeGeometry {
			continue
		}
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
	_, _, md, err := LoadTopology(ctx, path, tr, NewRealClock())
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
	rStep := LocalPolar{}.effectiveSteps()
	if rStep != localStepR {
		t.Fatalf("local-polar default radius step = %v, want %v", rStep, localStepR)
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

// TestLocalPolarDirPoleStable proves the rebuilt local-polar direction
// representation (layout_holder.go LocalPolar.Dir, an EXACT unit vector) does not
// blow up near the +y pole, unlike the retired (quantITheta,quantIPhi) decomposition
// it replaced. The retired representation quantized cart2polar(offset) about the
// FIXED +y pole: near θ≈0 one φ-cell spans r·sinθ·stepφ → 0, so a tiny world nudge of
// the neighbor could swing quantIPhi by many cells (in the worst case, an unbounded
// jump) even though the true direction barely moved — that is the bug this rebuild
// makes unrepresentable, since Dir is stored faithfully rather than re-quantized
// through that pole-singular decomposition.
func TestLocalPolarDirPoleStable(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 0, "y": 0,   "z": 0},
	    "dst": {"x": 0, "y": 100, "z": 0}
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
	md.Start(ctx)

	lookupDir := func(from, to string) (vec3, bool) {
		lh, ok := md.layoutHolders[from]
		if !ok {
			return vec3{}, false
		}
		for _, lp := range lh.LocalPolarsSnapshot() {
			if lp.To == to {
				return lp.Dir, true
			}
		}
		return vec3{}, false
	}

	waitFor := func(cond func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for condition")
			}
			time.Sleep(time.Millisecond)
		}
	}

	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("centerOfNode(dst) missing")
	}

	// Drag src so its offset to dst points almost exactly along +y (within ~0.05°):
	// place src directly below dst except for a tiny x/z jitter.
	epsAngle := 0.05 * math.Pi / 180           // 0.05 degrees in radians
	tinyOffset := 100 * math.Tan(epsAngle*0.3) // well within the 0.05deg budget
	target1 := vec3{X: dstCenter.X + tinyOffset, Y: dstCenter.Y - 100, Z: dstCenter.Z}
	if !md.RootMove("src", target1) {
		t.Fatal("RootMove returned false for known node")
	}
	waitFor(func() bool {
		c, ok := md.centerOfNode("src")
		return ok && c == target1
	})

	dir1, ok := lookupDir("src", "dst")
	if !ok {
		t.Fatal("src has no local polar entry for dst")
	}
	if l := dir1.length(); math.Abs(l-1) > 1e-6 {
		t.Fatalf("Dir is not a unit vector: length=%v", l)
	}
	for _, c := range []float64{dir1.X, dir1.Y, dir1.Z} {
		if math.IsNaN(c) || math.IsInf(c, 0) {
			t.Fatalf("Dir has non-finite component: %+v", dir1)
		}
	}
	if dir1.Y < 0.999 {
		t.Fatalf("Dir not near +y as expected: %+v", dir1)
	}

	// A tiny further world nudge (well inside the "near the pole" regime where the
	// retired (theta,phi) quantization would swing phi by many grid cells) must
	// produce a BOUNDED change in Dir — the whole point of storing an exact unit
	// vector instead of re-deriving it through a pole-singular quantization.
	target2 := vec3{X: target1.X + 0.01, Y: target1.Y, Z: target1.Z + 0.01}
	if !md.RootMove("src", target2) {
		t.Fatal("RootMove returned false for known node (nudge)")
	}
	waitFor(func() bool {
		c, ok := md.centerOfNode("src")
		return ok && c == target2
	})
	dir2, ok := lookupDir("src", "dst")
	if !ok {
		t.Fatal("src has no local polar entry for dst after nudge")
	}

	delta := dir2.sub(dir1).length()
	// The world nudge itself is ~0.014 units against a ~100-unit offset, so the true
	// direction change is on that same tiny order. Bound generously (still orders of
	// magnitude below a "swung to a different cell" jump, which under the retired
	// (theta,phi) representation could be a delta approaching 2 near the pole).
	if delta > 0.01 {
		t.Fatalf("Dir changed by %v for a tiny nudge near the pole — not bounded (retired theta/phi quantization would have been free to swing phi arbitrarily here)", delta)
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
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"2": {}, "5": {}, "7": {}, "8": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"5": {}, "2": {}, "7": {}, "8": {}, "6": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"1": {}, "2": {}, "3": {}, "5": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"9": {}, "3": {}, "6": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	// placeEqualRadii uses: nearest point on the perpendicular-bisector plane of
	// (3,6) to target — p = target - nhat*((target-mid)·nhat).
	n := sixCenterBefore.sub(threeCenterBefore)
	if n.length() == 0 {
		t.Fatal("test fixture's neighbors 3 and 6 coincide — adjust positions")
	}
	nhat := n.normalize()
	mid := threeCenterBefore.add(sixCenterBefore).scale(0.5)
	expected := target.sub(nhat.scale(target.sub(mid).dot(nhat)))
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
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"10": {}, "6": {}, "8": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	// placeEqualRadii uses: nearest point on the perpendicular-bisector plane of
	// (6,8) to target — p = target - nhat*((target-mid)·nhat).
	n := eightCenterBefore.sub(sixCenterBefore)
	if n.length() == 0 {
		t.Fatal("test fixture's neighbors 6 and 8 coincide — adjust positions")
	}
	nhat := n.normalize()
	mid := sixCenterBefore.add(eightCenterBefore).scale(0.5)
	expected := target.sub(nhat.scale(target.sub(mid).dot(nhat)))
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

// TestRootMoveNode6MakesFourEdgesEqual verifies node 6's CURRENT cascade: node 6 is a
// FREE move — it lands exactly at the raw drag target, no re-solve. It computes the
// SHORTER of its two c-distances (6→9, 6→10, each rounded to a whole tick of
// localStepR against 9/10's PRE-drag centers), giving d = shortest*localStepR. Node 9
// is then placed, via placeAtDistanceFromBoth, at distance d from BOTH its fixed
// neighbors 3 and 6 (the nearest point on the two spheres' equal-distance circle to
// node 9's current position); node 10 likewise at distance d from BOTH 6 and 8. Nodes
// 3 and 8 never move. The net result: all four edges 9→3, 9→6, 6→10, 10→8 land at
// exactly d. Each of 9 and 10 has its two edge-c records set to the propagated
// shortest c. The fixture is chosen so d is NOT clamped to either half-distance
// (|3-6|/2, |6-8|/2), so placeAtDistanceFromBoth solves a genuine circle, not the
// midpoint fallback.
func TestRootMoveNode6MakesFourEdgesEqual(t *testing.T) {
	geoms := map[string]nodeGeom{
		"6":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in"}, {Name: "in2"}}},
		"9":  {Kind: "WindowAndInhibitLeftGate", HasPos: true, ScenePolar: cart2polar(vec3{8.1, 2, 2}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"3":  {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{4, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
		"10": {Kind: "WindowAndInhibitRightGate", HasPos: true, ScenePolar: cart2polar(vec3{2, 6.1, 2}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
		"8":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 4}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"9To3":  {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in"},
		"9To6":  {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in2"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"6": {}, "9": {}, "3": {}, "10": {}, "8": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
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
	// c9old, c10old: node 9 and node 10's centers BEFORE the drag — node 6's cascade
	// computes cTo9/cTo10 against these (rounded to a localStepR tick), and
	// placeAtDistanceFromBoth uses THESE pre-drag centers as the "nearest to current
	// position" tie-break.
	c9old, ok := md.centerOfNode("9")
	if !ok {
		t.Fatal("centerOfNode(9) missing before move")
	}
	c10old, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("centerOfNode(10) missing before move")
	}

	// Drag target for node 6: non-degenerate, and cTo9 != cTo10 so "shortest" is a
	// meaningful, non-trivial choice.
	target6 := vec3{X: 2, Y: 2, Z: 2}

	const step = localStepR
	cTo9 := math.Round(c9old.sub(target6).length() / step)
	cTo10 := math.Round(c10old.sub(target6).length() / step)
	if cTo9 == cTo10 {
		t.Fatal("test fixture's cTo9 == cTo10 — adjust geometry so shortest is a meaningful choice")
	}
	shortest := math.Min(cTo9, cTo10)
	d := shortest * step

	half9 := threeCenterBefore.sub(target6).length() / 2
	half10 := target6.sub(eightCenterBefore).length() / 2
	if d < half9 || d < half10 {
		t.Fatalf("test fixture's d=%v is clamped by a half-distance (half9=%v, half10=%v) — adjust geometry so placeAtDistanceFromBoth solves a genuine circle", d, half9, half10)
	}

	// Independently reconstruct placeAtDistanceFromBoth's circle solve for each of 9
	// (anchors 3, target6) and 10 (anchors target6, 8), using the pre-drag centers as
	// the "current position" tie-break (node 9/10 haven't moved yet when the cascade
	// calls this).
	placeAtDistanceFromBoth := func(cur, a, b vec3, dist float64) vec3 {
		ab := b.sub(a)
		half := ab.length() / 2
		if dist < half {
			dist = half
		}
		m := a.add(b).scale(0.5)
		nhat := ab.scale(1 / (2 * half))
		q := cur.sub(nhat.scale(cur.sub(m).dot(nhat)))
		dir := q.sub(m)
		rho := math.Sqrt(math.Max(0, dist*dist-half*half))
		if dir.length() == 0 {
			return m
		}
		return m.add(dir.normalize().scale(rho))
	}
	expected9 := placeAtDistanceFromBoth(c9old, threeCenterBefore, target6, d)
	expected10 := placeAtDistanceFromBoth(c10old, target6, eightCenterBefore, d)

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
		c10, ok10 := md.centerOfNode("10")
		c3, ok3 := md.centerOfNode("3")
		c8, ok8 := md.centerOfNode("8")
		if !ok9 || !ok10 || !ok3 || !ok8 {
			return false
		}
		dist9To3 := c9.sub(c3).length()
		dist9To6 := c9.sub(c6).length()
		dist10To6 := c10.sub(c6).length()
		dist10To8 := c10.sub(c8).length()
		return math.Abs(dist9To3-dist9To6) <= eps && math.Abs(dist10To6-dist10To8) <= eps
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("node 6 drag never converged (node 6 at raw target AND 9's radii equal AND 10's radii equal)")
		}
		time.Sleep(time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// (a) node 6 landed at the RAW target — a free move, no re-solve.
	sixAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	if math.Abs(sixAfter.X-target6.X) > eps || math.Abs(sixAfter.Y-target6.Y) > eps || math.Abs(sixAfter.Z-target6.Z) > eps {
		t.Fatalf("node 6 landing = %+v, want raw target %+v (free move)", sixAfter, target6)
	}

	nineAfter, ok := md.centerOfNode("9")
	if !ok {
		t.Fatal("centerOfNode(9) missing after move")
	}
	tenAfter, ok := md.centerOfNode("10")
	if !ok {
		t.Fatal("centerOfNode(10) missing after move")
	}
	threeCenterAfter, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing after move")
	}
	eightCenterAfter, ok := md.centerOfNode("8")
	if !ok {
		t.Fatal("centerOfNode(8) missing after move")
	}

	// node 9 and node 10 landed exactly at their independently-computed
	// placeAtDistanceFromBoth solves.
	if math.Abs(nineAfter.X-expected9.X) > eps || math.Abs(nineAfter.Y-expected9.Y) > eps || math.Abs(nineAfter.Z-expected9.Z) > eps {
		t.Fatalf("node 9 landing = %+v, want equal-distance-circle solve %+v", nineAfter, expected9)
	}
	if math.Abs(tenAfter.X-expected10.X) > eps || math.Abs(tenAfter.Y-expected10.Y) > eps || math.Abs(tenAfter.Z-expected10.Z) > eps {
		t.Fatalf("node 10 landing = %+v, want equal-distance-circle solve %+v", tenAfter, expected10)
	}

	dist9To3 := nineAfter.sub(threeCenterAfter).length()
	dist9To6 := nineAfter.sub(sixAfter).length()
	dist10To6 := tenAfter.sub(sixAfter).length()
	dist10To8 := tenAfter.sub(eightCenterAfter).length()

	// (b) node 9's two radii — to 3 and to 6 — are equal.
	if math.Abs(dist9To3-dist9To6) > eps {
		t.Fatalf("node 9's radii to 3 and 6 are not equal: dist(9,3)=%v, dist(9,6)=%v", dist9To3, dist9To6)
	}
	// (c) node 10's two radii — to 6 and to 8 — are equal.
	if math.Abs(dist10To6-dist10To8) > eps {
		t.Fatalf("node 10's radii to 6 and 8 are not equal: dist(10,6)=%v, dist(10,8)=%v", dist10To6, dist10To8)
	}
	// (d) all four edges — 9→3, 9→6, 6→10, 10→8 — are equal to each other.
	if math.Abs(dist9To3-dist10To8) > eps {
		t.Fatalf("dist(9,3)=%v != dist(10,8)=%v, want all four edges equal", dist9To3, dist10To8)
	}
	// (e) each equals the expected d = shortest*localStepR (only asserted because the
	// fixture was chosen so d is not clamped by either half-distance, above).
	if math.Abs(dist9To3-d) > eps {
		t.Fatalf("dist(9,3)/dist(9,6) = %v, want shortest-c distance d = %v", dist9To3, d)
	}
	if math.Abs(dist10To6-d) > eps {
		t.Fatalf("dist(10,6)/dist(10,8) = %v, want shortest-c distance d = %v", dist10To6, d)
	}

	// (g) node 9's edge-c records (to "3" and to "6") and node 10's (to "6" and "8")
	// both equal int(shortest) — the propagated shortest c.
	lh9 := md.layoutHolders["9"]
	var got9To3, got9To6 *LocalPolar
	for _, lp := range lh9.LocalPolarsSnapshot() {
		cp := lp
		switch lp.To {
		case "3":
			got9To3 = &cp
		case "6":
			got9To6 = &cp
		}
	}
	if got9To3 == nil || got9To6 == nil {
		t.Fatal("node 9 missing an edge-c record after node-6 cascade")
	}
	if got9To3.QuantIR != int(shortest) || got9To6.QuantIR != int(shortest) {
		t.Fatalf("node9's edge-c records = to3:%d to6:%d, want both %d (shortest c)", got9To3.QuantIR, got9To6.QuantIR, int(shortest))
	}

	lh10 := md.layoutHolders["10"]
	var got10To6, got10To8 *LocalPolar
	for _, lp := range lh10.LocalPolarsSnapshot() {
		cp := lp
		switch lp.To {
		case "6":
			got10To6 = &cp
		case "8":
			got10To8 = &cp
		}
	}
	if got10To6 == nil || got10To8 == nil {
		t.Fatal("node 10 missing an edge-c record after node-6 cascade")
	}
	if got10To6.QuantIR != int(shortest) || got10To8.QuantIR != int(shortest) {
		t.Fatalf("node10's edge-c records = to6:%d to8:%d, want both %d (shortest c)", got10To6.QuantIR, got10To8.QuantIR, int(shortest))
	}

	// (f) node 3 and node 8 unchanged.
	if threeCenterAfter != threeCenterBefore {
		t.Fatalf("node 3 moved: got %+v, want unchanged %+v", threeCenterAfter, threeCenterBefore)
	}
	if eightCenterAfter != eightCenterBefore {
		t.Fatalf("node 8 moved: got %+v, want unchanged %+v", eightCenterAfter, eightCenterBefore)
	}
}

// TestRootMoveNode6TriggersNode2MovesNode5 verifies the current node-6->node-2 hop:
// dragging node 6 drives node 2's own hardcoded nodeID=="2" equalize, but with
// origin=="6" node 2 SOURCES on node 6 (not node 5) via src:="6" in the "2" case,
// so it repositions its remaining peer (node 5, since node 1 is hardcoded-excluded
// and node 6 is the source) to the fresh 6<->2 distance — and then `break`s BEFORE
// fanning onward to 5/1, so neither node 5's own rootMove nor node 1's rootMove is
// re-run. Node 2 itself does not move (its own rootMove target is its current
// center); node 6 is the source so it is untouched by the equalize step (it's
// already at the fresh position from its own top-level move).
//
// Built via newMoveDispatch directly (mirroring TestRootMoveNode6MakesFourEdgesEqual
// and TestRootMoveNode2CascadeKeepsNode1EdgesEqual), NOT via LoadTopology. Node 8 is
// intentionally shared between node 10 (10To8) and node 5 (5To8), same as the real
// topology's node 8.
func TestRootMoveNode6TriggersNode2MovesNode5(t *testing.T) {
	geoms := map[string]nodeGeom{
		"6":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in9"}, {Name: "in10"}, {Name: "in2"}}},
		"9":  {Kind: "WindowAndInhibitLeftGate", HasPos: true, ScenePolar: cart2polar(vec3{8.1, 2, 2}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"3":  {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{4, 0, 0}), Inputs: []portGeom{{Name: "in9"}, {Name: "in1"}}},
		"10": {Kind: "WindowAndInhibitRightGate", HasPos: true, ScenePolar: cart2polar(vec3{2, 6.1, 2}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
		"8":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 4}), Inputs: []portGeom{{Name: "in10"}, {Name: "in5"}}},
		"2":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{50, 0, 0}), Outputs: []portGeom{{Name: "out6"}, {Name: "out5"}, {Name: "out1"}}},
		"1":  {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{60, 5, -8}), Inputs: []portGeom{{Name: "in2"}}, Outputs: []portGeom{{Name: "out3"}}},
		"5":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{50, 40, 0}), Inputs: []portGeom{{Name: "in2"}}, Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"7":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{50, 70, 20}), Inputs: []portGeom{{Name: "in5"}}},
	}
	edges := map[string]EdgeEndpoints{
		"9To3":  {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in9"},
		"9To6":  {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in9"},
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in10"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in10"},
		"2To6":  {Source: "2", Target: "6", SourceHandle: "out6", TargetHandle: "in2"},
		"2To5":  {Source: "2", Target: "5", SourceHandle: "out5", TargetHandle: "in2"},
		"2To1":  {Source: "2", Target: "1", SourceHandle: "out1", TargetHandle: "in2"},
		"1To3":  {Source: "1", Target: "3", SourceHandle: "out3", TargetHandle: "in1"},
		"5To7":  {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in5"},
		"5To8":  {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in5"},
	}
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"6": {}, "9": {}, "3": {}, "10": {}, "8": {},
		"2": {}, "1": {}, "5": {}, "7": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	sixCenterBefore, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing before move")
	}
	twoCenterBefore, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("centerOfNode(2) missing before move")
	}
	fiveCenterBefore, ok := md.centerOfNode("5")
	if !ok {
		t.Fatal("centerOfNode(5) missing before move")
	}
	threeCenterBefore, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing before move")
	}
	oneCenterBefore, ok := md.centerOfNode("1")
	if !ok {
		t.Fatal("centerOfNode(1) missing before move")
	}

	target6 := vec3{X: 2, Y: 2, Z: 2}
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
		c2, ok2 := md.centerOfNode("2")
		c5, ok5 := md.centerOfNode("5")
		if !ok2 || !ok5 {
			return false
		}
		dist26 := c2.sub(c6).length()
		dist25 := c2.sub(c5).length()
		return math.Abs(dist25-dist26) <= eps
	}
	for !converged() {
		if time.Now().After(deadline) {
			t.Fatal("node 6 drag + 6->2 cascade never converged")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Give any trailing re-emit messages a moment to settle.
	time.Sleep(20 * time.Millisecond)

	// (a) node 6 landed at the raw drag target — it was NOT re-solved or moved again
	// by node 2's equalize step.
	sixAfter, ok := md.centerOfNode("6")
	if !ok {
		t.Fatal("centerOfNode(6) missing after move")
	}
	if math.Abs(sixAfter.X-target6.X) > eps || math.Abs(sixAfter.Y-target6.Y) > eps || math.Abs(sixAfter.Z-target6.Z) > eps {
		t.Fatalf("node 6 landing = %+v, want raw target %+v", sixAfter, target6)
	}
	_ = sixCenterBefore

	// (b) node 2 itself did not move — its own rootMove target was its current center.
	twoCenterAfter, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("centerOfNode(2) missing after move")
	}
	if math.Abs(twoCenterAfter.X-twoCenterBefore.X) > eps || math.Abs(twoCenterAfter.Y-twoCenterBefore.Y) > eps || math.Abs(twoCenterAfter.Z-twoCenterBefore.Z) > eps {
		t.Fatalf("node 2 moved: got %+v, want unchanged %+v", twoCenterAfter, twoCenterBefore)
	}

	// (c) node 5 MOVED, and dist(2,5) == dist(2,6) — the core assertion that node 2
	// sourced its equalize on the fresh 6<->2 distance (src:="6" in case "2") and
	// repositioned its remaining peer, node 5, to match it.
	fiveCenterAfter, ok := md.centerOfNode("5")
	if !ok {
		t.Fatal("centerOfNode(5) missing after move")
	}
	if fiveCenterAfter == fiveCenterBefore {
		t.Fatal("node 5 did not move — the 6->2 hop's src:=\"6\" equalize did not reposition its peer")
	}
	dist25 := twoCenterAfter.sub(fiveCenterAfter).length()
	dist26 := twoCenterAfter.sub(sixAfter).length()
	if math.Abs(dist25-dist26) > eps {
		t.Fatalf("dist(2,5)=%v != dist(2,6)=%v after the 6->2 hop's equalize", dist25, dist26)
	}

	// (d) node 5 preserved its bearing FROM node 2: equalize moves a peer along its
	// current direction from the dragged node, only rescaling the distance.
	wantDir := fiveCenterBefore.sub(twoCenterBefore).normalize()
	gotDir := fiveCenterAfter.sub(twoCenterAfter).normalize()
	if dot := wantDir.dot(gotDir); math.Abs(dot-1) > 1e-6 {
		t.Fatalf("node 5's bearing from node 2 changed: dot(before,after)=%v, want ~1", dot)
	}

	// (e) no onward cascade: node 3 (via a would-be 6->2->1 hop) and node 1 itself did
	// NOT move, since case "2" breaks before fanning to 5/1 when origin=="6".
	threeCenterAfter, ok := md.centerOfNode("3")
	if !ok {
		t.Fatal("centerOfNode(3) missing after move")
	}
	if math.Abs(threeCenterAfter.X-threeCenterBefore.X) > eps || math.Abs(threeCenterAfter.Y-threeCenterBefore.Y) > eps || math.Abs(threeCenterAfter.Z-threeCenterBefore.Z) > eps {
		t.Fatalf("node 3 moved: got %+v, want unchanged %+v (no onward 6->2->1 cascade)", threeCenterAfter, threeCenterBefore)
	}
	oneCenterAfter, ok := md.centerOfNode("1")
	if !ok {
		t.Fatal("centerOfNode(1) missing after move")
	}
	if math.Abs(oneCenterAfter.X-oneCenterBefore.X) > eps || math.Abs(oneCenterAfter.Y-oneCenterBefore.Y) > eps || math.Abs(oneCenterAfter.Z-oneCenterBefore.Z) > eps {
		t.Fatalf("node 1 moved: got %+v, want unchanged %+v (no onward 6->2->1 cascade)", oneCenterAfter, oneCenterBefore)
	}
}

// TestNode6DragNoDataRace reproduces the "concurrent map read and map write" fatal that
// used to hit measureScalars(...,md.quantizedOffsets) when a node-6 drag's cascade fanned
// out GatePlace messages to nodes 9 and 10, whose gatePlaceNode -> moveNodeAndSetEdgeCs then
// ran CONCURRENTLY on their own two mover goroutines and both read/wrote the single shared
// md.quantizedOffsets map (a plain, unsynchronized Go map) — fatal even though each commit
// touched a DIFFERENT key, because Go's runtime detects concurrent map access on the map
// OBJECT, not per key. The fix (node6-drag-decentralized.md's generalization) gives every
// nodeMover its own quantOffset field, mutated only by that node's own goroutine, so 9 and
// 10's concurrent commits never touch a shared map. This test must be run with `-race` (as
// `go test -race ./nodes/Wiring/...` does) to catch a regression — without -race a data race
// on a map only *sometimes* fatals. Driving many drags maximizes the chance nodes 9 and 10's
// GatePlace handling actually overlaps in time.
func TestNode6DragNoDataRace(t *testing.T) {
	geoms := map[string]nodeGeom{
		"6":  {Kind: "HoldNewSendOld", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in"}, {Name: "in2"}}},
		"9":  {Kind: "WindowAndInhibitLeftGate", HasPos: true, ScenePolar: cart2polar(vec3{8.1, 2, 2}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"3":  {Kind: "Pulse", HasPos: true, ScenePolar: cart2polar(vec3{4, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
		"10": {Kind: "WindowAndInhibitRightGate", HasPos: true, ScenePolar: cart2polar(vec3{2, 6.1, 2}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
		"8":  {Kind: "Hold", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 4}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"9To3":  {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in"},
		"9To6":  {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in2"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil, nil, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"6": {}, "9": {}, "3": {}, "10": {}, "8": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	// Drive many rounds of node-6 drags, each waiting for node 6 to settle at its own
	// target before the next round fires — this still dispatches a fresh GatePlace to
	// BOTH 9 and 10 every round (so their concurrent commits repeatedly overlap, giving
	// -race many chances to catch a regression) without flooding any mover's bounded
	// inbox (capacity 8) faster than it can drain, which would deadlock the test
	// itself rather than exercise the race.
	const rounds = 50
	const eps = 1e-6
	for i := 0; i < rounds; i++ {
		target := vec3{X: float64(2 + i%5), Y: float64(2 + (i*3)%7), Z: float64(2 + (i*5)%11)}
		if !md.RootMove("6", target) {
			t.Fatalf("round %d: RootMove returned false for known node", i)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			c6, ok := md.centerOfNode("6")
			if ok && math.Abs(c6.X-target.X) <= eps && math.Abs(c6.Y-target.Y) <= eps && math.Abs(c6.Z-target.Z) <= eps {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: node 6 never converged to target %+v", i, target)
			}
			time.Sleep(time.Millisecond)
		}
	}
	// Give any trailing GatePlace/Trigger messages from the final round a moment to
	// settle so the race detector observes the tail of the cascade too.
	time.Sleep(50 * time.Millisecond)
}
