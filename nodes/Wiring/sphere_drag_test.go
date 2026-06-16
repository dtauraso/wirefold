// sphere_drag_test.go — SphereDrag radial-scale behaviour.
//
// Topology used by most tests:
//
//	eCA: C → A   (C is sphere center, A is surface node)
//	eCB: C → B   (B is another surface node on the same sphere)
//	N: isolated node with no edges to C
//
// Initial positions: C=(0,0,0), A=(5,0,0), B=(0,5,0), N=(20,0,0).
// C carries R=5 so sphere-chain layout is active.
// Dragging A to (10,0,0) should:
//   - A lands at target (10,0,0)
//   - C.R updated to 10.0
//   - B rescaled radially: dir=(0,1,0) → new pos (0,10,0)
//   - C and N stay put

package Wiring

import (
	"context"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// flushNode sends a no-op "move" message with an ack to the node's inbox (and
// every incident edge inbox) and blocks until each mover closes the ack. Since
// "move" messages do not update geom.Center, this is a pure flush: it drains
// all earlier messages (including fanCenters "center" messages) before returning,
// without overwriting any state.
func flushNode(md *MoveDispatch, nodeID string) {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return
	}
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
		md.dispatch[kk] <- moveMsg{Kind: moveMsgKindMove, NodeID: nodeID, ack: ack}
		acks = append(acks, ack)
	}
	for _, ack := range acks {
		<-ack
	}
}

// buildSphereDragFixture constructs a MoveDispatch with 4 nodes and 2 edges:
//
//	C=(0,0,0) R=5  (center of sphere)
//	A=(5,0,0)      (surface node — target of eCA)
//	B=(0,5,0)      (surface node — target of eCB)
//	N=(20,0,0)     (isolated, no edge to C)
//
// edges: eCA C→A, eCB C→B
func buildSphereDragFixture() (*MoveDispatch, context.CancelFunc) {
	r5 := 5.0
	cCenter := vec3{X: 0, Y: 0, Z: 0}
	aCenter := vec3{X: 5, Y: 0, Z: 0}
	bCenter := vec3{X: 0, Y: 5, Z: 0}
	nCenter := vec3{X: 20, Y: 0, Z: 0}

	geoms := map[string]nodeGeom{
		"C": {Kind: "FanInSrc", Center: &cCenter, R: &r5},
		"A": {Kind: "FanInSrc", Center: &aCenter},
		"B": {Kind: "FanInSrc", Center: &bCenter},
		"N": {Kind: "FanInSrc", Center: &nCenter},
	}
	edgeEndpoints := map[string]EdgeEndpoints{
		"eCA": {Source: "C", Target: "A", SourceHandle: "Out", TargetHandle: "In"},
		"eCB": {Source: "C", Target: "B", SourceHandle: "Out", TargetHandle: "In"},
	}

	tr := T.New(256)
	md := newMoveDispatch(geoms, edgeEndpoints, tr)

	ctx, cancel := context.WithCancel(context.Background())
	md.Start(ctx)
	return md, cancel
}

func TestSphereDragDraggedNodeLandsAtTarget(t *testing.T) {
	md, cancel := buildSphereDragFixture()
	defer cancel()

	ok := md.SphereDrag("A", vec3{X: 10, Y: 0, Z: 0})
	if !ok {
		t.Fatal("SphereDrag returned false for known node A")
	}

	// Flush: wait until A's mover has processed all queued messages.
	flushNode(md, "A")

	got := md.nodeMovers["A"].geom.Center
	if got == nil {
		t.Fatal("A.Center is nil after drag")
	}
	if !approxEq(got.X, 10) || !approxEq(got.Y, 0) || !approxEq(got.Z, 0) {
		t.Errorf("A.Center = %+v, want (10,0,0)", *got)
	}
}

func TestSphereDragSiblingRescaledToNewRadius(t *testing.T) {
	md, cancel := buildSphereDragFixture()
	defer cancel()

	md.SphereDrag("A", vec3{X: 10, Y: 0, Z: 0})

	// Wait for both B and A to drain their queues.
	flushNode(md, "A")
	flushNode(md, "B")

	// C.R should be updated to 10.
	if md.nodeMovers["C"].geom.R == nil {
		t.Fatal("C.R is nil after drag")
	}
	if !approxEq(*md.nodeMovers["C"].geom.R, 10) {
		t.Errorf("C.R = %v, want 10", *md.nodeMovers["C"].geom.R)
	}

	// B should be radially scaled: dir (0,1,0) * 10 = (0,10,0).
	gotB := md.nodeMovers["B"].geom.Center
	if gotB == nil {
		t.Fatal("B.Center is nil after drag")
	}
	if !approxEq(gotB.X, 0) || !approxEq(gotB.Y, 10) || !approxEq(gotB.Z, 0) {
		t.Errorf("B.Center = %+v, want (0,10,0)", *gotB)
	}
}

func TestSphereDragNonSurfaceNodeUnchanged(t *testing.T) {
	md, cancel := buildSphereDragFixture()
	defer cancel()

	md.SphereDrag("A", vec3{X: 10, Y: 0, Z: 0})

	// Flush via a node that IS touched (A), ensuring fanCenters has returned.
	flushNode(md, "A")

	// N has no edge to C so its center must not change.
	gotN := md.nodeMovers["N"].geom.Center
	if gotN == nil {
		t.Fatal("N.Center is nil")
	}
	if !approxEq(gotN.X, 20) || !approxEq(gotN.Y, 0) || !approxEq(gotN.Z, 0) {
		t.Errorf("N.Center = %+v, want (20,0,0)", *gotN)
	}

	// C (sphere center) should also stay at origin.
	gotC := md.nodeMovers["C"].geom.Center
	if gotC == nil {
		t.Fatal("C.Center is nil")
	}
	if !approxEq(gotC.X, 0) || !approxEq(gotC.Y, 0) || !approxEq(gotC.Z, 0) {
		t.Errorf("C.Center = %+v, want (0,0,0)", *gotC)
	}
}

func TestSphereDragUnknownIDReturnsFalse(t *testing.T) {
	md, cancel := buildSphereDragFixture()
	defer cancel()

	ok := md.SphereDrag("does-not-exist", vec3{X: 1, Y: 2, Z: 3})
	if ok {
		t.Error("SphereDrag returned true for unknown node id")
	}
}
