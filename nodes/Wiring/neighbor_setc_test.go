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
	"sync"
	"testing"
	"time"
)

// tappedMsg is a minimal recorded (destID, kind, senderID) tuple from md.SetMsgTap,
// used by this file's message-trace assertion.
type tappedMsg struct {
	destID   string
	kind     string
	senderID string
}

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

	// Tap every routed message so we can assert (3): this drag is exactly one hop —
	// src receives nothing but the new moveMsgKindNeighborSetC (senderID=dst) — with
	// no equalize/trigger/gate-place/requantize cascade kind (those kinds no longer
	// exist in the vocabulary at all).
	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

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

	// Poll for src's own LocalPolar entry to dst to pick up the new quantized values
	// (async message-delivery race — the same shape every other test in this package
	// uses).
	lpAfter := pollLocalPolarRequantized(t, lhSrc, "dst", lpBefore)
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

	// (3) src receives exactly one moveMsgKindNeighborSetC (senderID=dst), and none of
	// the old cascade kinds — those kinds don't even exist in the vocabulary anymore,
	// so this is a belt-and-suspenders check against reintroducing one. A plain
	// moveMsgKindCenter re-emit to src (dst's own commit fanning its incident edge's
	// geometry to partners) is expected and is NOT a cascade — it carries no position
	// write (nodeMover.handle's nil-Center branch is a pure re-emit).
	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()
	setCCount := 0
	forbidden := map[string]bool{"equalize": true, "trigger": true, "gatePlace": true, "requantize": true}
	for _, m := range trace {
		if m.destID != "src" {
			continue
		}
		if forbidden[m.kind] {
			t.Fatalf("(3) src should never receive a %q cascade message; got %+v in trace %+v", m.kind, m, trace)
		}
		if m.kind == moveMsgKindNeighborSetC {
			if m.senderID != "dst" {
				t.Fatalf("(3) src's neighborSetC should be sent by dst; got %+v", m)
			}
			setCCount++
		}
	}
	if setCCount != 1 {
		t.Fatalf("(3) expected exactly one moveMsgKindNeighborSetC (senderID=dst) routed to src; got %d in trace %+v", setCCount, trace)
	}

	md.persist.quantOffset.flush()
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

	var mu sync.Mutex
	var recorded []moveMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if destID == "src" && msg.Kind == moveMsgKindNeighborSetC {
			mu.Lock()
			recorded = append(recorded, msg)
			mu.Unlock()
		}
	})
	defer md.SetMsgTap(nil)

	dstBefore, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst")
	}
	target := dstBefore.add(vec3{X: 60, Y: 25, Z: -15})
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	deadline := time.Now().Add(2 * time.Second)
	var got moveMsg
	for {
		mu.Lock()
		n := len(recorded)
		if n > 0 {
			got = recorded[n-1]
		}
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("src never received a moveMsgKindNeighborSetC from dst")
		}
		time.Sleep(time.Millisecond)
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
	if got.DeltaA != wantA || got.DeltaB != wantB || got.DeltaC != wantC {
		t.Fatalf("neighborSetC delta should be dst's own triple change (new-old): got=(%d,%d,%d) want=(%d,%d,%d) (before=%+v after=%+v)",
			got.DeltaA, got.DeltaB, got.DeltaC, wantA, wantB, wantC, dstToSrcBefore, dstToSrcAfter)
	}

	md.persist.quantOffset.flush()
}
