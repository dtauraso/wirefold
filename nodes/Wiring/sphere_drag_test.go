// sphere_drag_test.go — RootMove polar-layout behaviour.
//
// Topology:
//
//	eCA: C → A   (C is sphere center, A is a surface node)
//	eCB: C → B   (B is another surface node on the same sphere)
//	N: isolated node with no edges to C
//
// Initial positions: C=(0,0,0), A=(5,0,0), B=(0,5,0), N=(20,0,0).
// Under the polar model a drag updates ONLY the dragged node's outer root
// (soft membership). Dragging A to (10,0,0) should:
//   - A's root + world land at (10,0,0)
//   - B, C, N stay exactly put (no radial scale, no cascade)
//   - C's sphere R grows to reach A (derived, not stored)

package Wiring

import (
	"context"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// flushNode drains a node's (and incident edges') inboxes via an acked no-op.
func flushNode(md *MoveDispatch, nodeID string) {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return
	}
	keys := []string{nodeID}
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

// buildRootMoveFixture: C center with surface nodes A,B; N isolated. Roots built
// from the centers so RootMove has the polar authority installed.
func buildRootMoveFixture() (*MoveDispatch, context.CancelFunc) {
	centers := map[string]vec3{
		"C": {0, 0, 0},
		"A": {5, 0, 0},
		"B": {0, 5, 0},
		"N": {20, 0, 0},
	}
	geoms := map[string]nodeGeom{}
	for id, c := range centers {
		cc := c
		geoms[id] = nodeGeom{Kind: "FanInSrc", Center: &cc}
	}
	edgeEndpoints := map[string]EdgeEndpoints{
		"eCA": {Source: "C", Target: "A", SourceHandle: "Out", TargetHandle: "In"},
		"eCB": {Source: "C", Target: "B", SourceHandle: "Out", TargetHandle: "In"},
	}
	tr := T.New(256)
	md := newMoveDispatch(geoms, edgeEndpoints, tr)
	md.setRoots(buildRoots(centers))
	ctx, cancel := context.WithCancel(context.Background())
	md.Start(ctx)
	return md, cancel
}

func TestRootMoveDraggedNodeLandsAtTarget(t *testing.T) {
	md, cancel := buildRootMoveFixture()
	defer cancel()
	if !md.RootMove("A", vec3{X: 10, Y: 0, Z: 0}) {
		t.Fatal("RootMove returned false for known node A")
	}
	w, ok := md.roots.world("A")
	if !ok {
		t.Fatal("no world for A")
	}
	if w.sub(vec3{10, 0, 0}).length() > 1e-6 {
		t.Errorf("A world = %v want (10,0,0)", w)
	}
}

func TestRootMoveTouchesOneRootOnly(t *testing.T) {
	md, cancel := buildRootMoveFixture()
	defer cancel()
	before := map[string]vec3{}
	for id := range md.roots.roots {
		w, _ := md.roots.world(id)
		before[id] = w
	}
	md.RootMove("A", vec3{X: 10, Y: 0, Z: 0})
	for _, id := range []string{"B", "C", "N"} {
		w, _ := md.roots.world(id)
		if w.sub(before[id]).length() > 1e-9 {
			t.Errorf("node %s moved (%v -> %v); soft membership should keep it put", id, before[id], w)
		}
	}
}

func TestRootMoveGrowsSphereR(t *testing.T) {
	md, cancel := buildRootMoveFixture()
	defer cancel()
	edges := md.heldEdges()
	if r := md.roots.sphereR("C", edges); r > 5+1e-9 {
		t.Fatalf("initial R(C) = %v want 5", r)
	}
	md.RootMove("A", vec3{X: 10, Y: 0, Z: 0})
	// A now at distance 10; B still at 5 → reach grows to 10.
	if r := md.roots.sphereR("C", edges); r < 10-1e-6 {
		t.Errorf("R(C) = %v want ~10 after moving A out", r)
	}
}

func TestRootMoveUnknownReturnsFalse(t *testing.T) {
	md, cancel := buildRootMoveFixture()
	defer cancel()
	if md.RootMove("does-not-exist", vec3{X: 1, Y: 2, Z: 3}) {
		t.Error("RootMove returned true for unknown node id")
	}
}
