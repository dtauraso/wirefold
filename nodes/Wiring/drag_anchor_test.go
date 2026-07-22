// drag_anchor_test.go — the drag-received-delta must be anchored at DRAG START, not
// at the previous pointer-move event. See node_move.go's moveMsgKindDragStart doc
// comment for the model. RootMove runs on every ~8ms pointer-move — far finer than one
// quantize step — so a per-move-event delta reads (0,0,0) for many consecutive commits
// even while the drag is steadily crossing cells; the fix reports current-minus-anchor
// so the log shows the drag's running total at every commit.

package Wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

const dragAnchorTopo = `{
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

func loadDragAnchorTopo(t *testing.T) (context.Context, context.CancelFunc, *MoveDispatch) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(dragAnchorTopo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tr := T.New(4096)
	t.Cleanup(tr.Close)
	_, _, md, _, err := LoadTopology(ctx, path, tr, NewRealClock())
	if err != nil {
		cancel()
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)
	return ctx, cancel, md
}

// TestDragDeltaAnchoredAtDragStart is the core regression: a drag delivered as
// multiple successive RootMove calls, where the SECOND move's quantized radial index
// is UNCHANGED from the first move's (so the naive current-minus-previous-move delta
// is 0) but both differ from the drag-start anchor. The reported delta at the second
// move must reflect the cumulative change since drag start (non-zero), not the
// zero-looking single-step change. This is the exact "reads (0,0,0) almost always"
// symptom: consecutive per-move quantized values often coincide even while the drag's
// total offset from its start keeps growing.
func TestDragDeltaAnchoredAtDragStart(t *testing.T) {
	_, cancel, md := loadDragAnchorTopo(t)
	defer cancel()

	lh, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}

	// dst is the "abc-drag" recipient of every neighborSetC src fans out; its breadcrumb
	// carries the exact (DeltaA,DeltaB,DeltaC) src computed for that fan (see
	// neighborSetCRequantize) — this is the production channel the removed msgTap used
	// to intercept in flight. Wire the debug sink BEFORE the setup move so no breadcrumb
	// is missed, same happens-before argument as waitForAbcDrag's doc comment: src writes
	// its own LocalPolar entry strictly before logging the breadcrumb, in the same call
	// on src's own goroutine.
	var dbg syncBuffer
	md.tr.SetDebugSink(&dbg)

	// Setup (untracked): a bare RootMove call, before any drag-start, that merely gets
	// src's LocalPolar-to-dst entry into existence (a freshly-loaded LayoutHolder starts
	// with none) at the topology's own starting distance (R=100, local-polar index
	// round(100/2)=50). This is deliberately NOT the "drag" under test — it establishes
	// the baseline the real drag will start from.
	if !md.RootMove("src", vec3{X: 100, Y: 0, Z: 0}) {
		t.Fatal("setup RootMove returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 1)
	waitForLocalPolarIR(t, lh, "dst", 50)

	// Drag start: arm src's anchor at its CURRENT triple (index 50 to dst) -- the same
	// signal gesture.go's gestPending->gestDragging edge sends.
	md.sendMove("src", moveMsg{Kind: moveMsgKindDragStart, NodeID: "src"})

	// Drag src outward along the same +x axis dst sits on (0,0,0), so only the radial
	// local-polar component (DeltaC, localStepR=2.0) moves; bearing stays fixed.
	// Move 1: src 100 -> 101.9 (R 101.9, round(101.9/2)=51 vs anchor index 50: delta +1).
	if !md.RootMove("src", vec3{X: 101.9, Y: 0, Z: 0}) {
		t.Fatal("RootMove #1 returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 2)

	// Move 2: src 101.9 -> 102.05 (R 102.05, round(102.05/2)=51 -- SAME quantized index
	// as move 1, so the naive move-to-move delta is 0). The drag's TOTAL offset from the
	// anchor (index 50) is still +1.
	if !md.RootMove("src", vec3{X: 102.05, Y: 0, Z: 0}) {
		t.Fatal("RootMove #2 returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 3)

	deltas := abcDragDeltasFor(t, &dbg, "dst")
	if len(deltas) < 3 {
		t.Fatalf("expected at least 3 abc-drag deltas for dst, got %d: %+v", len(deltas), deltas)
	}
	// deltas[0] = setup, deltas[1] = move #1, deltas[2] = move #2.
	move2Delta := deltas[2]
	if move2Delta[2] == 0 {
		t.Fatalf("move #2's reported DeltaC = 0 (move-to-move delta); want the drag's cumulative offset from its start (+1, non-zero) — this is the exact bug: %+v", deltas)
	}
	if move2Delta[2] != 1 {
		t.Fatalf("move #2's reported DeltaC = %d, want +1 (cumulative offset from drag-start anchor, index 50->51): %+v", move2Delta[2], deltas)
	}
}

// TestDragAnchorRearmsOnNewDrag verifies a SECOND drag on the same node computes its
// deltas relative to the SECOND drag's own start, not the first drag's — the anchor
// must not leak across drags. Drives the drag-start signal explicitly (the same
// message gesture.go's gestPending->gestDragging edge sends) to arm each drag.
func TestDragAnchorRearmsOnNewDrag(t *testing.T) {
	_, cancel, md := loadDragAnchorTopo(t)
	defer cancel()

	lh, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}

	// See TestDragDeltaAnchoredAtDragStart's comment: wire the debug sink before the
	// setup move so the "abc-drag" breadcrumb trail for dst is complete from the start.
	var dbg syncBuffer
	md.tr.SetDebugSink(&dbg)

	// Setup (untracked): establish src's LocalPolar-to-dst entry at the topology's
	// starting distance before either tracked drag begins.
	if !md.RootMove("src", vec3{X: 100, Y: 0, Z: 0}) {
		t.Fatal("setup RootMove returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 1)
	waitForLocalPolarIR(t, lh, "dst", 50)

	// Drag 1: arm at src's current position (R=100, index 50), then move to R=104
	// (index 52) -- delta vs drag-1's anchor should be +2.
	md.sendMove("src", moveMsg{Kind: moveMsgKindDragStart, NodeID: "src"})
	if !md.RootMove("src", vec3{X: 104, Y: 0, Z: 0}) {
		t.Fatal("RootMove (drag1) returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 2)
	waitForLocalPolarIR(t, lh, "dst", 52)

	// Drag 2 starts HERE (R=104, index 52) -- re-arm the anchor at this new position.
	// Two moves within drag 2, structured exactly like
	// TestDragDeltaAnchoredAtDragStart's move1/move2 so this is ALSO RED against the
	// per-move-event bug, not just a fix-internal check:
	//   move A: R=104 -> 105.9 (index 53) -- delta vs drag-2 anchor (52): +1.
	//   move B: R=105.9 -> 106.05 (index 53, SAME as move A -- a per-move-event delta
	//   here is 0). The cumulative delta since drag-2's OWN start must still read +1.
	// If the anchor wrongly kept drag-1's stale value (index 50) instead of re-arming
	// at drag-2's start (index 52), move B would instead read +3.
	md.sendMove("src", moveMsg{Kind: moveMsgKindDragStart, NodeID: "src"})
	if !md.RootMove("src", vec3{X: 105.9, Y: 0, Z: 0}) {
		t.Fatal("RootMove (drag2 move A) returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 3)
	if !md.RootMove("src", vec3{X: 106.05, Y: 0, Z: 0}) {
		t.Fatal("RootMove (drag2 move B) returned false")
	}
	waitForAbcDragCount(t, &dbg, "dst", 4)

	deltas := abcDragDeltasFor(t, &dbg, "dst")
	if len(deltas) < 4 {
		t.Fatalf("expected 4 abc-drag deltas for dst, got %d: %+v", len(deltas), deltas)
	}
	// deltas[0] = setup, [1] = drag1, [2] = drag2 move A, [3] = drag2 move B.
	moveBDelta := deltas[3][2]
	if moveBDelta == 3 {
		t.Fatalf("drag 2 move B's DeltaC = 3 (stale drag-1 anchor at index 50 leaked through); want +1 relative to drag 2's OWN start (index 52): %+v", deltas)
	}
	if moveBDelta != 1 {
		t.Fatalf("drag 2 move B's DeltaC = %d, want +1 (drag 2's cumulative offset from ITS OWN start, not the last move-to-move step which is 0): %+v", moveBDelta, deltas)
	}
}

// waitForLocalPolarIR blocks until lh's LocalPolar entry for "to" reports the wanted
// QuantIR, or fails the test on timeout. Used to make setup moves deterministic before
// a test's tracked drag begins (the mover applies moves on its own goroutine).
func waitForLocalPolarIR(t *testing.T, lh *LayoutHolder, to string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, lp := range lh.LocalPolarsSnapshot() {
			if lp.To == to && lp.QuantIR == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for LocalPolar[%s].QuantIR == %d", to, want)
		}
		time.Sleep(time.Millisecond)
	}
}
