package Wiring

// port_torus_colinearity_test.go — STAGE 2 of the `port ∈ torus` lock: applyPortTorusColinearity
// keeps an edge whose both ports are pinned to their own node's border ring colinear by coupling
// the dependent node's z to the dragged node's z (see locks.go applyPortTorusColinearity).

import "testing"

// portTorusTestMD builds a MoveDispatch with two nodes S,D connected by an edge
// S.out -> D.in (an edgeMover, matching what heldEdges/RootMove actually reads), each
// starting at a DIFFERENT z, plus an eqPortTorus lock on each port (Active as given).
func portTorusTestMD(active bool, sCenter, dCenter vec3) *MoveDispatch {
	md := &MoveDispatch{selectedLockIndex: -1}
	md.edgeMovers = map[string]*edgeMover{
		"S->D": {
			edgeID: "S->D",
			srcID:  "S", dstID: "D",
			srcH: "out", dstH: "in",
		},
	}
	md.polarEqs = []polarEq{
		{Kind: eqPortTorus, PortNode: "S", PortName: "out", PortIsInput: false, TorusNode: "S", Active: active},
		{Kind: eqPortTorus, PortNode: "D", PortName: "in", PortIsInput: true, TorusNode: "D", Active: active},
	}
	md.nodeMovers = map[string]*nodeMover{} // unused by the solve; pos() below supplies centers
	_ = sCenter
	_ = dCenter
	return md
}

func portTorusPosFn(centers map[string]vec3) func(string) (vec3, bool) {
	return func(id string) (vec3, bool) {
		v, ok := centers[id]
		return v, ok
	}
}

func TestApplyPortTorusColinearityMovesDependentZ(t *testing.T) {
	centers := map[string]vec3{
		"S": {X: 0, Y: 0, Z: 5},
		"D": {X: 10, Y: 3, Z: -2}, // different z than S
	}
	md := portTorusTestMD(true, centers["S"], centers["D"])
	pos := portTorusPosFn(centers)

	out := md.applyPortTorusColinearity("S", pos)
	dNew, ok := out["D"]
	if !ok {
		t.Fatalf("applyPortTorusColinearity did not move D: out=%v", out)
	}
	if dNew.Z != centers["S"].Z {
		t.Fatalf("D.z=%v after solve, want S.z=%v", dNew.Z, centers["S"].Z)
	}
	if dNew.X != centers["D"].X || dNew.Y != centers["D"].Y {
		t.Fatalf("D.x/y changed: got %v, want x/y preserved from %v", dNew, centers["D"])
	}

	// Build post-solve geometries and check the ring-colinearity claim: with
	// S_center.z == D_center.z, the aimed ports on S and D land ON their own node's
	// X-Y-plane ring, and S_center, S.port, D.port, D_center are colinear.
	sCenter := centers["S"]
	dCenterAfter := dNew

	gS := nodeGeom{Kind: "Test", Center: &sCenter}
	gD := nodeGeom{Kind: "Test", Center: &dCenterAfter}
	registry := AimedPortRegistry{
		{NodeID: "S", PortName: "out", IsInput: false}: "D",
		{NodeID: "D", PortName: "in", IsInput: true}:   "S",
	}
	postCenters := map[string]vec3{"S": sCenter, "D": dCenterAfter}
	centerOf := func(id string) (vec3, bool) { v, ok := postCenters[id]; return v, ok }

	sPort := portWorldPosAimed(gS, "out", false, "S", registry, centerOf)
	dPort := portWorldPosAimed(gD, "in", true, "D", registry, centerOf)

	// (b) each port lies in its own node's ring plane: port.z == that node's center.z.
	if sPort.Z != sCenter.Z {
		t.Fatalf("S.port.z=%v, want S_center.z=%v (ring plane)", sPort.Z, sCenter.Z)
	}
	if dPort.Z != dCenterAfter.Z {
		t.Fatalf("D.port.z=%v, want D_center.z=%v (ring plane)", dPort.Z, dCenterAfter.Z)
	}

	// (c) S_center, S.port, D.port, D_center colinear: cross((S.port-S_center),
	// (D_center-S_center)) ~= 0.
	v1 := sPort.sub(sCenter)
	v2 := dCenterAfter.sub(sCenter)
	cross := vec3{
		X: v1.Y*v2.Z - v1.Z*v2.Y,
		Y: v1.Z*v2.X - v1.X*v2.Z,
		Z: v1.X*v2.Y - v1.Y*v2.X,
	}
	const eps = 1e-9
	if cross.length() > eps {
		t.Fatalf("S_center,S.port,D_center not colinear: cross=%v (len=%v)", cross, cross.length())
	}
	// D.port must lie on the same line too (it's derived the same way, aimed at S).
	v3 := dPort.sub(sCenter)
	cross2 := vec3{
		X: v3.Y*v2.Z - v3.Z*v2.Y,
		Y: v3.Z*v2.X - v3.X*v2.Z,
		Z: v3.X*v2.Y - v3.Y*v2.X,
	}
	if cross2.length() > eps {
		t.Fatalf("S_center,D.port,D_center not colinear: cross=%v (len=%v)", cross2, cross2.length())
	}
}

func TestApplyPortTorusColinearityInactiveLocksMoveNothing(t *testing.T) {
	centers := map[string]vec3{
		"S": {X: 0, Y: 0, Z: 5},
		"D": {X: 10, Y: 3, Z: -2},
	}
	md := portTorusTestMD(false, centers["S"], centers["D"]) // Active: false on both locks
	pos := portTorusPosFn(centers)

	out := md.applyPortTorusColinearity("S", pos)
	if len(out) != 0 {
		t.Fatalf("applyPortTorusColinearity with inactive locks wrote %v, want no writes", out)
	}
}

// TestApplyPolarEqsStillSkipsPortTorus guards STAGE 1's contract: eqPortTorus entries
// remain inert inside applyPolarEqs (they're handled by applyPortTorusColinearity, not
// the node-node solver), even though STAGE 2 now gives them a real solve elsewhere.
func TestApplyPolarEqsStillSkipsPortTorus(t *testing.T) {
	md := &MoveDispatch{selectedLockIndex: -1}
	md.polarEqs = []polarEq{
		{Kind: eqPortTorus, PortNode: "S", PortName: "out", PortIsInput: false, TorusNode: "S", Active: true},
	}
	out := md.applyPolarEqs("S", func(string) (vec3, bool) { return vec3{}, true })
	if len(out) != 0 {
		t.Fatalf("applyPolarEqs wrote %v for an eqPortTorus entry, want no writes (handled by applyPortTorusColinearity)", out)
	}
}
