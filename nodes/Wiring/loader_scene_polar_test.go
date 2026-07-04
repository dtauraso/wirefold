package Wiring

import "testing"

// TestToNodeGeomPrefersScenePolar: with a persisted scene sphere, a node's world center is
// derived from its scene polar (not the legacy x/y/z); without one, it uses x/y/z.
func TestToNodeGeomPrefersScenePolar(t *testing.T) {
	r, th, ph := 50.0, 1.2, 0.3
	n := specNode{
		ID: "n", Type: "Pulse",
		X: 999, Y: 999, Z: 999, // legacy cartesian — should be IGNORED when polar+sphere present
		ScenePolarR: &r, ScenePolarTheta: &th, ScenePolarPhi: &ph,
	}
	sceneCenter := vec3{X: 10, Y: 20, Z: -5}

	// With a persisted sphere: world = sceneCenter + polar2cart(scenePolar).
	g := n.toNodeGeom(sceneCenter, true)
	want := sceneCenter.add(polar2cart(polar{R: r, Theta: th, Phi: ph}))
	if g.Center == nil || g.Center.sub(want).length() > 1e-9 {
		t.Fatalf("with sphere: center=%v want %v (must ignore x/y/z)", g.Center, want)
	}

	// Without a persisted sphere: fall back to legacy x/y/z.
	g2 := n.toNodeGeom(vec3{}, false)
	if g2.Center == nil || *g2.Center != (vec3{X: 999, Y: 999, Z: 999}) {
		t.Fatalf("no sphere: center=%v want legacy (999,999,999)", g2.Center)
	}

	// A node with NO scene polar uses x/y/z even when a sphere exists.
	n3 := specNode{ID: "n3", Type: "Pulse", X: 1, Y: 2, Z: 3}
	g3 := n3.toNodeGeom(sceneCenter, true)
	if g3.Center == nil || *g3.Center != (vec3{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("no polar fields: center=%v want (1,2,3)", g3.Center)
	}
}
