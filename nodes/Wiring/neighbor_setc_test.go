package Wiring

// neighbor_setc_test.go — dedicated headless proof for the plain-neighbor single-
// assignment set-c redraw model (node_move.go moveMsgKindNeighborSetC /
// neighborSetCRequantize): a dragged node X sends each direct, role-free domain
// neighbor M a SINGLE set-c assignment carrying X's fresh center; M STAYS PUT — X is
// the only node whose position changes — and M re-quantizes its OWN stored local polar
// to X from the live offset, with theta, phi AND r all fresh (about M's own rotating
// pole) — no reposition, no equalize/trigger cascade, no forwarding past M.

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestNeighborSetCRequantizesEdgeNeighborStaysPut drives the real move path
// (writeTree's plain 2-node src/dst graph — no cascade role on either end) and
// asserts the three properties the single-assignment set-c requantize model requires:
// the neighbor (src) stays put, src's stored local polar to the dragged node (dst) is
// re-quantized in theta, phi AND r from the live offset, and it happens as exactly one
// hop with no cascade.
func TestNeighborSetCRequantizesEdgeNeighborStaysPut(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}
	srcCenterBefore, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src")
	}

	var lpBefore LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			lpBefore = lp
		}
	}
	if lpBefore.To != "dst" {
		t.Fatal("src has no pre-drag LocalPolar entry for dst")
	}

	// Sync point for the post-drag lhSrc read below: src (the neighbor, NOT the dragged
	// node) writes its own requantized LocalPolar entry (SetLocalPolar/SetPole, on src's
	// OWN goroutine, inside neighborSetCRequantize) strictly BEFORE it logs its
	// "abc-drag" breadcrumb in that same call — waiting for the breadcrumb (rather than
	// polling lhSrc directly, a data race against src's own mover goroutine) establishes
	// the happens-before edge. See time_node_abc_drag_breadcrumb_test.go.
	var dbg syncBuffer
	md.tr.SetSink(&dbg)

	// Drag dst off its prior bearing from src AND farther away, so both the angle and
	// the distance src re-quantizes to dst demonstrably change (a purely radial drag
	// along the existing bearing would leave theta/phi unchanged and not exercise the
	// "angle also changes" half of the new model).
	dstBefore, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst")
	}
	target := dstBefore.add(vec3{X: 60, Y: 25, Z: -15})
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	// Wait for src's own "abc-drag" breadcrumb (fired after src's own requantize
	// write, on src's own goroutine — see the sync-point comment above) before reading
	// its LocalPolar entry to dst.
	waitForAbcDrag(t, &dbg, "src")
	var lpAfter LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			lpAfter = lp
		}
	}
	time.Sleep(cascadeSettle) // let any (unwanted) further cascade settle

	// (1) src's world center is UNCHANGED — the neighbor stays put; only dst moved.
	srcCenterAfter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after drag")
	}
	const eps = 1e-9
	if d := srcCenterAfter.sub(srcCenterBefore).length(); d > eps {
		t.Fatalf("(1) src must stay put on a dst drag: before=%+v after=%+v (moved by %g)", srcCenterBefore, srcCenterAfter, d)
	}

	// (2) src's stored local polar to dst matches a FRESH quantization of the live
	// offset (dst_newcenter - src_center) about src's own pole — theta, phi AND r all
	// re-derived, not carried forward from the old bearing. Computed via the same
	// primitives requantizePoleTraced uses at the cart<->polar boundary.
	dstCenterAfter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	pole := lhSrc.Pole()
	offset := dstCenterAfter.sub(srcCenterAfter)
	d, r := dirFromOffset(offset)
	c, psi := azimuthFrom(pole, d)
	st, sp, sr := lhSrc.localPolarSteps("dst")
	wantTheta := int(math.Round(c / st))
	wantPhi := int(math.Round(psi / sp))
	wantR := int(math.Round(r / sr))
	if lpAfter.QuantITheta != wantTheta || lpAfter.QuantIPhi != wantPhi || lpAfter.QuantIR != wantR {
		t.Fatalf("(2) src's requantized local polar to dst should match a fresh quantization of the live offset: got=(theta=%d,phi=%d,r=%d) want=(theta=%d,phi=%d,r=%d)",
			lpAfter.QuantITheta, lpAfter.QuantIPhi, lpAfter.QuantIR, wantTheta, wantPhi, wantR)
	}
	if lpAfter.QuantITheta == lpBefore.QuantITheta && lpAfter.QuantIPhi == lpBefore.QuantIPhi {
		t.Fatalf("(2) the drag was chosen to move dst off src's prior bearing, so theta or phi should have changed: before=%+v after=%+v", lpBefore, lpAfter)
	}

	// (3) src receives exactly one moveMsgKindNeighborSetC: neighborSetCRequantize logs
	// exactly one "abc-drag" breadcrumb for src per received moveMsgKindNeighborSetC
	// (see its doc comment / quantized_move.go), so src's abc-drag count IS the
	// neighborSetC receipt count — a genuine production outcome, not an intercepted
	// in-flight message. The old cascade kinds ("equalize"/"trigger"/"gatePlace"/
	// "requantize") don't exist anywhere in moveMsgKind's vocabulary any more (grep
	// node_move.go's moveMsgKind* constants: only anchor/center/centers/drag/
	// neighborSetC/dragStart remain) — a message of those kinds is unrepresentable by
	// construction, not merely absent at runtime, so there is nothing left to tap for.
	srcDeltas := abcDragDeltasFor(t, &dbg, "src")
	if len(srcDeltas) != 1 {
		t.Fatalf("(3) expected exactly one abc-drag (== one moveMsgKindNeighborSetC) delivered to src; got %d: %+v", len(srcDeltas), srcDeltas)
	}

}

// TestNeighborSetCDeltaIsDraggedNodesOwnTripleChange proves the DRAGGED node's own
// quantized-triple delta (moveMsg.DeltaA/B/C on the neighborSetC message) equals dst's
// OWN new triple to src minus dst's OWN triple to src BEFORE the drag — computed once,
// on dst's own goroutine — and that exact delta is what src (the recipient) receives.
func TestNeighborSetCDeltaIsDraggedNodesOwnTripleChange(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhDst, ok := md.layoutHolders["dst"]
	if !ok {
		t.Fatal("no LayoutHolder for dst")
	}
	var dstToSrcBefore LocalPolar
	for _, lp := range lhDst.LocalPolarsSnapshot() {
		if lp.To == "src" {
			dstToSrcBefore = lp
		}
	}
	if dstToSrcBefore.To != "src" {
		t.Fatal("dst has no pre-drag LocalPolar entry for src")
	}

	// src is the recipient of the neighborSetC dst's drag fans out: its "abc-drag"
	// breadcrumb carries the exact (DeltaA,DeltaB,DeltaC) dst computed for that fan (see
	// neighborSetCRequantize) — the same production channel the removed msgTap used to
	// intercept in flight, and race-free (waitForAbcDrag establishes the happens-before
	// edge for dstToSrcAfter below, same argument as neighbor_setc's other test).
	var dbg syncBuffer
	md.tr.SetSink(&dbg)

	dstBefore, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst")
	}
	target := dstBefore.add(vec3{X: 60, Y: 25, Z: -15})
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	waitForAbcDrag(t, &dbg, "src")
	got := abcDragDeltasFor(t, &dbg, "src")
	if len(got) != 1 {
		t.Fatalf("expected exactly one abc-drag (== one moveMsgKindNeighborSetC) delivered to src; got %d: %+v", len(got), got)
	}

	var dstToSrcAfter LocalPolar
	for _, lp := range lhDst.LocalPolarsSnapshot() {
		if lp.To == "src" {
			dstToSrcAfter = lp
		}
	}
	wantA := dstToSrcAfter.QuantITheta - dstToSrcBefore.QuantITheta
	wantB := dstToSrcAfter.QuantIPhi - dstToSrcBefore.QuantIPhi
	wantC := dstToSrcAfter.QuantIR - dstToSrcBefore.QuantIR
	if got[0][0] != wantA || got[0][1] != wantB || got[0][2] != wantC {
		t.Fatalf("neighborSetC delta should be dst's own triple change (new-old): got=(%d,%d,%d) want=(%d,%d,%d) (before=%+v after=%+v)",
			got[0][0], got[0][1], got[0][2], wantA, wantB, wantC, dstToSrcBefore, dstToSrcAfter)
	}

}
