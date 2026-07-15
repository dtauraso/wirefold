package Wiring

import "testing"

// TestToNodeGeomScenePolar: a node's world center is DERIVED from its stored scene polar
// about the scene sphere center — SceneCenter + polar2cart(ScenePolar) (polar-frame-rewrite.md).
// Scene polar is the only stored position; there is no cartesian x/y/z load path.
func TestToNodeGeomScenePolar(t *testing.T) {
	r, th, ph := 50.0, 1.2, 0.3
	n := specNode{
		ID: "n", Type: "Pulse",
		ScenePolarR: &r, ScenePolarTheta: &th, ScenePolarPhi: &ph,
	}
	sceneCenter := vec3{X: 10, Y: 20, Z: -5}

	g := n.toNodeGeom(sceneCenter)
	if !g.HasPos {
		t.Fatalf("scene polar present: HasPos=false, want true")
	}
	want := sceneCenter.add(polar2cart(polar{R: r, Theta: th, Phi: ph}))
	if nodeWorldPos(g).sub(want).length() > 1e-9 {
		t.Fatalf("world=%v want %v", nodeWorldPos(g), want)
	}

	// A node with NO scene polar has no position (HasPos false → world origin).
	n2 := specNode{ID: "n2", Type: "Pulse"}
	g2 := n2.toNodeGeom(sceneCenter)
	if g2.HasPos {
		t.Fatalf("no polar fields: HasPos=true, want false")
	}
}
