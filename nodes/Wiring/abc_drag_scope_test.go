package Wiring

// abc_drag_scope_test.go — proves the abc-drag drag-log (each recipient nodeMover's OWN
// current-drag-scoped gotDragMsg bit + DragDelta triple) is SCOPED TO THE CURRENT DRAG, not
// accumulated across the whole session. Regression test for the bug where dragging node A
// logged its real recipients, then dragging a DIFFERENT node B kept A's stale recipients
// around alongside B's — the fix is a KindAbcDragReset-equivalent broadcast
// (MoveDispatch.resetAbcDrag, moveMsgKindAbcReset) emitted once at the real drag-start edge
// (the gesture FSM's pending→dragging transition, nodes/Wiring/gesture.go) before the
// neighborSetC fan, which clears every node's OWN gotDragMsg/dragDelta* fields (node_mover.go's
// handle, moveMsgKindAbcReset case). This test drives md.RootMove directly (not the gesture
// FSM), so it calls md.resetAbcDrag() itself before each RootMove to stand in for that
// drag-start edge.
//
// Two disjoint node pairs (x→t,x→n and y→z) let us drag x (recipients {t,n}) then drag y
// (recipient {z}) and assert the FINAL state's gotDragMsg set is exactly {z} — if the reset
// were missing (old accumulating behavior), {t,n} would still show gotDragMsg=1.
//
// Per-owner-buffer-rows.md's final step deleted the central Buffer.SnapshotState
// accumulator + its fd-3 fallback frame this test used to poll; each recipient's
// gotDragMsg/dragDelta* now live ONLY on that node's own nodeMover (set by its own
// goroutine — node_mover.go), so this test wires each recipient's OWN dedicated stream
// directly (test-only direct field assignment, mirroring ui_publish_propagation_test.go's
// TestGesturePathPropagatesUIStateToMoverStream) and polls that node's own captured frame
// instead of a shared fd-3 NODE frame.

import (
	"context"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
)

// writeXTNY extends writeXTN's x/t/n topology with a second, DISJOINT pair y→z (no edge
// connecting it to x/t/n), so dragging y fans neighborSetC to z only.
func writeXTNY(t *testing.T) string {
	t.Helper()
	root := writeXTN(t)
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	mk("nodes/y/meta.json", `{"id":"y","type":"SrcNode","r":100,"scenePolarR":60,"scenePolarTheta":0.5,"scenePolarPhi":2.0}`)
	mk("nodes/y/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/z/meta.json", `{"id":"z","type":"SinkNode","r":100,"scenePolarR":70,"scenePolarTheta":1.4,"scenePolarPhi":-0.6}`)
	mk("nodes/z/inputs/In.json", `{"name":"In"}`)
	mk("edges/eYZ.json", `{"label":"eYZ","kind":"data","source":"y","sourceHandle":"Out","target":"z","targetHandle":"In"}`)
	mk("nodes/iso/meta.json", `{"id":"iso","type":"SrcNode","r":100,"scenePolarR":50,"scenePolarTheta":2.5,"scenePolarPhi":1.0}`)
	mk("nodes/iso/outputs/Out.json", `{"name":"Out"}`)
	return root
}

// wireNodeStream wires id's nodeMover directly to a fresh captured stream (test-only direct
// field assignment — same pattern as TestGesturePathPropagatesUIStateToMoverStream), so its
// own periodic emit (nodeMover.run's writeStreamFrame call) can be polled without any real fd.
func wireNodeStream(t *testing.T, md *MoveDispatch, id string) *uiPubLockedBuf {
	t.Helper()
	nm, ok := md.nodeMovers[id]
	if !ok {
		t.Fatalf("no nodeMover for %s", id)
	}
	row, ok := md.NodeRowFor(id)
	if !ok {
		t.Fatalf("no NODE-ROW for %s", id)
	}
	buf := &uiPubLockedBuf{}
	nm.streamOut = buf
	nm.nodeRow = row
	nm.buildFrame = func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []RowEvent) []byte {
		return B.BuildNodeStreamFrame(tick, nodeRow, cx, cy, cz, radius, sphereR, vrx, vry, vrz, frx, fry, frz, selected, kindID, hovered, latchedSel, gotDragMsg, dragDeltaA, dragDeltaB, dragDeltaC, label, portNames, portDX, portDY, portDZ, portPX, portPY, portPZ, portIsInput, portHovered, dstNodeRows, edgeRows, nil)
	}
	return buf
}

// TestAbcDragLogIsScopedToCurrentDrag drags x (recipients t,n), then drags the disjoint
// y (recipient z), and asserts each recipient's OWN gotDragMsg state at the end: t and n
// clear back to 0 once y's drag resets/refans (they receive no message on y's drag, and
// resetAbcDrag clears every node), while z ends at 1 — proving the previous drag's
// recipients were cleared, not accumulated. Under the old accumulating behavior (no
// AbcDragReset broadcast before the fan), t and n's bits would never have been cleared.
func TestAbcDragLogIsScopedToCurrentDrag(t *testing.T) {
	root := writeXTNY(t)

	tr := T.NewWithSinkHook(0, nil, nil)

	_, _, md, _, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.EnableEditPersist(root)

	bufT := wireNodeStream(t, md, "t")
	bufN := wireNodeStream(t, md, "n")
	bufZ := wireNodeStream(t, md, "z")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	xBefore, ok := md.centerOfNode("x")
	if !ok {
		t.Fatal("no center for x")
	}
	xTarget := xBefore.add(vec3{X: 55, Y: -20, Z: 30})
	// Stand in for the gesture FSM's pending→dragging transition, which is the real
	// drag-start edge that broadcasts this reset (RootMove itself no longer does).
	md.resetAbcDrag()
	if !md.RootMove("x", xTarget) {
		t.Fatal("RootMove(x) returned false")
	}
	pollDragConverged(t, md, "x", xTarget)

	// t and n (x's neighbors) should both show gotDragMsg=1 before dragging y, so the
	// stale-recipient regression has something to leak.
	waitForNodeDragMsg(t, bufT, func(got uint8, _, _, _ int32) bool { return got == 1 })
	waitForNodeDragMsg(t, bufN, func(got uint8, _, _, _ int32) bool { return got == 1 })

	// Drag y — a node fully disjoint from x/t/n — after x's drag has settled.
	yBefore, ok := md.centerOfNode("y")
	if !ok {
		t.Fatal("no center for y")
	}
	yTarget := yBefore.add(vec3{X: -25, Y: 40, Z: -10})
	md.resetAbcDrag()
	if !md.RootMove("y", yTarget) {
		t.Fatal("RootMove(y) returned false")
	}
	pollDragConverged(t, md, "y", yTarget)

	// z (y's only neighbor) should show gotDragMsg=1 for THIS drag, while t and n — never
	// touched by y's drag, and cleared by the resetAbcDrag broadcast before it — must have
	// gone back to 0. If the reset were missing, t and n would still show gotDragMsg=1
	// from x's earlier drag.
	waitForNodeDragMsg(t, bufZ, func(got uint8, _, _, _ int32) bool { return got == 1 })
	deadline := time.Now().Add(2 * time.Second)
	for {
		gotT, _, _, _, okT := lastNodeStreamDragMsg(bufT.Bytes())
		gotN, _, _, _, okN := lastNodeStreamDragMsg(bufN.Bytes())
		if okT && okN && gotT == 0 && gotN == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("t/n gotDragMsg never cleared after dragging y (stale recipients leaked across drags): t=%v(ok=%v) n=%v(ok=%v)", gotT, okT, gotN, okN)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
