package Wiring

// neighbor_setc_test.go — dedicated headless proof for the plain-neighbor single-
// assignment set-c redraw model (node_move.go moveMsgKindNeighborSetC /
// neighborSetCReposition): a dragged node X sends each direct, role-free domain
// neighbor M a SINGLE set-c assignment; M keeps its stored bearing (QuantITheta/
// QuantIPhi) to X exactly, writes only the new c, and repositions itself along that
// unchanged direction to the new distance (X held fixed) — no equalize/trigger
// cascade, no forwarding past M.

import (
	"context"
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

// TestNeighborSetCRedrawKeepsBearingRepositionsOneHop drives the real move path
// (writeTree's plain 2-node src/dst graph — no cascade role on either end) and
// asserts the four properties the single-assignment set-c model requires.
func TestNeighborSetCRedrawKeepsBearingRepositionsOneHop(t *testing.T) {
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
	srcCenter, ok := md.centerOfNode("src")
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

	// Tap every routed message so we can assert (4): no equalize/trigger cascade ever
	// runs for this drag, and src receives exactly the new moveMsgKindNeighborSetC
	// (never the old bearing-re-deriving moveMsgKindRequantize).
	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	// Drag dst further away from src so the edge length (r) changes.
	dstBefore, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst")
	}
	dir := dstBefore.sub(srcCenter)
	target := srcCenter.add(dir.normalize().scale(dir.length() + 40))
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	// Poll for src's own LocalPolar entry to dst to pick up the new c (async
	// message-delivery race — the same shape every other test in this package uses).
	var lpAfter LocalPolar
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, lp := range lhSrc.LocalPolarsSnapshot() {
			if lp.To == "dst" {
				lpAfter = lp
			}
		}
		if lpAfter.QuantIR != lpBefore.QuantIR {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("src's local polar to dst never picked up the new set-c: before=%+v after=%+v", lpBefore, lpAfter)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let any (unwanted) further cascade settle

	// (1) src's stored QuantITheta/QuantIPhi are UNCHANGED — byte-equal to before.
	if lpAfter.QuantITheta != lpBefore.QuantITheta || lpAfter.QuantIPhi != lpBefore.QuantIPhi {
		t.Fatalf("(1) src's stored bearing to dst must be KEPT exactly: before=%+v after=%+v", lpBefore, lpAfter)
	}

	// (2) src's QuantIR changed to the new quantized length.
	if lpAfter.QuantIR == lpBefore.QuantIR {
		t.Fatalf("(2) src's QuantIR to dst should have changed: before=%+v after=%+v", lpBefore, lpAfter)
	}

	// (3) src's new world center equals X_newcenter - dir(storedθ,storedφ about src's
	// own pole) * (newIR * stepR), within one step.
	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	srcCenterAfter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after drag")
	}
	st, sp, sr := lpAfter.effectiveSteps()
	wantDir := fromAxisFrame(lhSrc.Pole(), float64(lpAfter.QuantITheta)*st, float64(lpAfter.QuantIPhi)*sp)
	wantCenter := dstCenter.sub(dirToVec3(wantDir).scale(float64(lpAfter.QuantIR) * sr))
	if d := srcCenterAfter.sub(wantCenter).length(); d > sr {
		t.Fatalf("(3) src's new world center should equal dst_newcenter - dir(kept bearing)*newR: got=%+v want=%+v (off by %g, step=%v)", srcCenterAfter, wantCenter, d, sr)
	}

	// (4) src never received anything but a set-c assignment — only the drag itself
	// (on dst) and a set-c assignment (on src), one hop, no forwarding past src. There
	// is no cascade machinery left to accidentally run, so this only checks src's own
	// trace is exactly the expected NeighborSetC message.
	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()
	sawSetC := false
	for _, m := range trace {
		if m.destID == "src" && m.kind == moveMsgKindNeighborSetC && m.senderID == "dst" {
			sawSetC = true
		}
	}
	if !sawSetC {
		t.Fatalf("(4) expected a moveMsgKindNeighborSetC (senderID=dst) routed to src; got %+v", trace)
	}

	md.quantOffsetPersist.flush()
}
