package Wiring

import (
	"testing"
)

// measureScalars/deriveCenters round-trip: a node's world center, re-measured about the
// scene center (flat absolute scene-polar — every node independent) and then re-derived,
// reproduces the original center. Scalars are chosen directly (exact integer cells) rather
// than measured from an arbitrary world center — measurement quantizes to the nearest
// cell, so round-tripping an off-grid center is lossy by design; the invariant under test
// is that measure(derive(scalars)) reproduces the SAME scalars (idempotent on-grid points).
func TestMeasureScalarsRoundTrips(t *testing.T) {
	sceneCenter := vec3{}
	scalars := map[string]quantizedOffset{
		"g": {},
		"p": {iTheta: 4, iPhi: 2, iR: 3},
		"c": {iTheta: 2, iPhi: -3, iR: 2},
	}
	derived := deriveCenters(scalars, sceneCenter)
	ids := map[string]bool{"g": true, "p": true, "c": true}
	remeasured := measureScalars(derived, ids, sceneCenter, scalars)
	for _, id := range []string{"g", "p", "c"} {
		if remeasured[id] != scalars[id] {
			t.Fatalf("%s: round-trip mismatch remeasured=%+v want=%+v", id, remeasured[id], scalars[id])
		}
	}
}

// TestMeasureScalarsMeasuresEveryNodeAboutSceneCenter asserts every node is measured
// independently about the ONE scene center — there is no reference/parent origin, so a
// node's offset never depends on another node's position.
func TestMeasureScalarsMeasuresEveryNodeAboutSceneCenter(t *testing.T) {
	sceneCenter := vec3{X: 10, Y: 20, Z: 30}
	centers := map[string]vec3{
		"a": sceneCenter.add(vec3{X: 40, Y: 0, Z: 0}),
		"b": sceneCenter.add(vec3{X: 0, Y: 0, Z: 60}),
	}
	ids := map[string]bool{"a": true, "b": true}
	offs := measureScalars(centers, ids, sceneCenter, nil)
	if _, ok := offs["a"]; !ok {
		t.Fatal("expected an offset for a")
	}
	if _, ok := offs["b"]; !ok {
		t.Fatal("expected an offset for b")
	}

	// Re-derive: each node's center comes straight back from the scene center, with no
	// dependency on the other node's offset.
	derived := deriveCenters(offs, sceneCenter)
	if d := derived["a"].sub(centers["a"]).length(); d > 1e-6 {
		t.Fatalf("a: derived center drifted by %v", d)
	}
	if d := derived["b"].sub(centers["b"]).length(); d > 1e-6 {
		t.Fatalf("b: derived center drifted by %v", d)
	}
}
