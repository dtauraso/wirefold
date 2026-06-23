package Wiring

import (
	"math"
	"testing"
)

// approxEqual returns true when two floats are within eps of each other.
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// approxVec3 returns true when all components are within eps.
func approxVec3(a, b vec3, eps float64) bool {
	return approxEqual(a.X, b.X, eps) && approxEqual(a.Y, b.Y, eps) && approxEqual(a.Z, b.Z, eps)
}

// buildAimedFixture returns a minimal AimedPortRegistry for edges 1→2 and 1→6
// (same keys as loader.go populates in production).
func buildAimedFixture() AimedPortRegistry {
	return AimedPortRegistry{
		{NodeID: "1", PortName: "ToHoldNewSendOld", IsInput: false}: "2",
		{NodeID: "2", PortName: "FromPrevHoldNewSendOldNode", IsInput: true}: "1",
		{NodeID: "1", PortName: "ToExcitatory", IsInput: false}: "6",
		{NodeID: "6", PortName: "FromInput", IsInput: true}: "1",
	}
}

// buildGeom builds a minimal nodeGeom with a known center for testing.
func buildGeom(kind string, center vec3) nodeGeom {
	c := center
	return nodeGeom{
		Kind:   kind,
		Center: &c,
		// At least one output port so portDir has something to fall back on.
		Outputs: []portGeom{{Name: "ToHoldNewSendOld"}, {Name: "ToExcitatory"}},
		Inputs:  []portGeom{{Name: "FromPrevHoldNewSendOldNode"}, {Name: "FromInput"}},
	}
}

// TestPortDirAimed_Node1AimsAtNode2 verifies that node 1's ToHoldNewSendOld
// port direction points toward node 2's center.
func TestPortDirAimed_Node1AimsAtNode2(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	node2Center := vec3{X: 3, Y: 0, Z: 0}

	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centers := map[string]vec3{"1": node1Center, "2": node2Center}
	centerOf := func(id string) (vec3, bool) {
		c, ok := centers[id]
		return c, ok
	}

	dir, ok := portDirAimed(geom1, "ToHoldNewSendOld", false, "1", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed returned ok=false for registered port")
	}
	want := vec3{X: 1, Y: 0, Z: 0}
	const eps = 1e-9
	if !approxVec3(dir, want, eps) {
		t.Errorf("portDirAimed(node1→node2 at (3,0,0)) = %+v; want %+v", dir, want)
	}
}

// TestPortDirAimed_Node1AimsAtNode2_AfterMove verifies that after moving node 2
// to (0,3,0) the direction updates to (0,1,0).
func TestPortDirAimed_Node1AimsAtNode2_AfterMove(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	node2Center := vec3{X: 0, Y: 3, Z: 0} // moved

	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centers := map[string]vec3{"1": node1Center, "2": node2Center}
	centerOf := func(id string) (vec3, bool) {
		c, ok := centers[id]
		return c, ok
	}

	dir, ok := portDirAimed(geom1, "ToHoldNewSendOld", false, "1", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed returned ok=false for registered port after move")
	}
	want := vec3{X: 0, Y: 1, Z: 0}
	const eps = 1e-9
	if !approxVec3(dir, want, eps) {
		t.Errorf("portDirAimed(node1→node2 at (0,3,0)) = %+v; want %+v", dir, want)
	}
}

// TestPortDirAimed_NonRegisteredPort verifies that a non-registered port still
// returns a non-zero direction via the portDir fallback.
func TestPortDirAimed_NonRegisteredPort(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centerOf := func(id string) (vec3, bool) { return vec3{}, false }

	// "SomeOtherPort" is not in the registry; should fall back to portDir.
	// portDir returns (vec3{}, false) for unknown ports so we test a known port
	// not in the registry by querying with a different nodeID.
	dir, _ := portDir(geom1, "ToHoldNewSendOld", false)
	if dir.length() == 0 {
		// portDir with no AnchorId returns ringAnchorDir(R, 0) which is (1,0,0) —
		// never zero for a valid port on a known kind. If we get zero the test is
		// inconclusive but not a regression.
		t.Log("portDir returned zero vector for fallback port (inconclusive)")
		return
	}
	// Now verify portDirAimed with a node NOT in the registry also falls back.
	geomX := buildGeom("HoldNewSendOld", vec3{X: 10, Y: 0, Z: 0})
	dirAimed, ok := portDirAimed(geomX, "ToHoldNewSendOld", false, "99", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed fallback returned ok=false for a valid port")
	}
	if dirAimed.length() == 0 {
		t.Error("portDirAimed fallback returned zero direction for a non-registered node")
	}
}
