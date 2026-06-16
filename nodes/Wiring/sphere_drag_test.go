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

// Co-sphere radius coupling: dragging surface node A out to radius 10 scales the
// other surface node B of center C radially to the same radius (keeping B's
// direction); the center C and the isolated node N stay put.
func TestRootMoveCouplesSurfaceSiblings(t *testing.T) {
	md, cancel := buildRootMoveFixture()
	defer cancel()
	const eps = 1e-6
	md.RootMove("A", vec3{X: 10, Y: 0, Z: 0}) // newR = 10
	// B was at (0,5,0), dir (0,1,0) → scaled to radius 10 → (0,10,0).
	wB, _ := md.roots.world("B")
	if wB.sub(vec3{0, 10, 0}).length() > eps {
		t.Errorf("B = %v want (0,10,0) (radially scaled to new radius)", wB)
	}
	// Center C and isolated N unchanged.
	wC, _ := md.roots.world("C")
	if wC.sub(vec3{0, 0, 0}).length() > eps {
		t.Errorf("center C moved to %v; should stay at origin", wC)
	}
	wN, _ := md.roots.world("N")
	if wN.sub(vec3{20, 0, 0}).length() > eps {
		t.Errorf("isolated N moved to %v; should stay put", wN)
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
