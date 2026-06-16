package Wiring

import (
	"math"
	"testing"
)

// abs distance between two vec3 positions
func chord(a, b vec3) float64 {
	return b.sub(a).length()
}

// TestRelaxSingleEdge: A->B, radius[A]=100, B starts close; after relaxation |B-A| ≈ 100
func TestRelaxSingleEdge(t *testing.T) {
	centers := map[string]vec3{
		"A": {0, 0, 0},
		"B": {10, 0, 0},
	}
	edges := []sphereEdge{{Source: "A", Target: "B"}}
	radius := map[string]float64{"A": 100}
	pinned := map[string]bool{}

	out := relaxPositions(centers, edges, radius, pinned, relaxIterations)

	got := chord(out["A"], out["B"])
	if math.Abs(got-100) > 1.0 {
		t.Errorf("expected |B-A| ≈ 100, got %.4f", got)
	}
}

// TestRelaxPinnedSource: A pinned at origin, B moves so |B-A| ≈ 100
func TestRelaxPinnedSource(t *testing.T) {
	centers := map[string]vec3{
		"A": {0, 0, 0},
		"B": {10, 0, 0},
	}
	edges := []sphereEdge{{Source: "A", Target: "B"}}
	radius := map[string]float64{"A": 100}
	pinned := map[string]bool{"A": true}

	out := relaxPositions(centers, edges, radius, pinned, relaxIterations)

	// A must not move
	if out["A"] != (vec3{0, 0, 0}) {
		t.Errorf("pinned A moved: got %v", out["A"])
	}
	got := chord(out["A"], out["B"])
	if math.Abs(got-100) > 1.0 {
		t.Errorf("expected |B-A| ≈ 100, got %.4f", got)
	}
}

// TestRelaxBackEdge: edges A->B and B->A — no panic, finite positions
func TestRelaxBackEdge(t *testing.T) {
	centers := map[string]vec3{
		"A": {0, 0, 0},
		"B": {10, 0, 0},
	}
	edges := []sphereEdge{
		{Source: "A", Target: "B"},
		{Source: "B", Target: "A"},
	}
	radius := map[string]float64{"A": 100, "B": 100}
	pinned := map[string]bool{}

	out := relaxPositions(centers, edges, radius, pinned, relaxIterations)

	for k, v := range out {
		if math.IsNaN(v.X) || math.IsNaN(v.Y) || math.IsNaN(v.Z) ||
			math.IsInf(v.X, 0) || math.IsInf(v.Y, 0) || math.IsInf(v.Z, 0) {
			t.Errorf("node %s has non-finite position %v", k, v)
		}
	}
}

// TestRelaxDeterministic: shuffled edge slice order produces identical output
func TestRelaxDeterministic(t *testing.T) {
	centers := map[string]vec3{
		"A": {0, 0, 0},
		"B": {10, 0, 0},
		"C": {5, 5, 0},
	}
	edges1 := []sphereEdge{
		{Source: "A", Target: "B"},
		{Source: "B", Target: "C"},
		{Source: "A", Target: "C"},
	}
	edges2 := []sphereEdge{
		{Source: "B", Target: "C"},
		{Source: "A", Target: "C"},
		{Source: "A", Target: "B"},
	}
	radius := map[string]float64{"A": 80, "B": 60, "C": 50}
	pinned := map[string]bool{}

	out1 := relaxPositions(centers, edges1, radius, pinned, relaxIterations)
	out2 := relaxPositions(centers, edges2, radius, pinned, relaxIterations)

	for k := range centers {
		if out1[k] != out2[k] {
			t.Errorf("node %s: order-dependent result: %v vs %v", k, out1[k], out2[k])
		}
	}
}

// TestRelaxOverConstrained: C pulled by A (R=50) and B (R=150), A and B pinned — C settles finite
func TestRelaxOverConstrained(t *testing.T) {
	centers := map[string]vec3{
		"A": {0, 0, 0},
		"B": {200, 0, 0},
		"C": {100, 0, 0},
	}
	edges := []sphereEdge{
		{Source: "A", Target: "C"},
		{Source: "B", Target: "C"},
	}
	radius := map[string]float64{"A": 50, "B": 150}
	pinned := map[string]bool{"A": true, "B": true}

	out := relaxPositions(centers, edges, radius, pinned, relaxIterations)

	v := out["C"]
	if math.IsNaN(v.X) || math.IsNaN(v.Y) || math.IsNaN(v.Z) ||
		math.IsInf(v.X, 0) || math.IsInf(v.Y, 0) || math.IsInf(v.Z, 0) {
		t.Errorf("C has non-finite position %v", v)
	}
}
