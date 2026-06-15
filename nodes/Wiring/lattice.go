// lattice.go — integer lattice coordinate system for node placement.
//
// Nodes may specify a (i, j, k) lattice cell instead of a free-form x/y/z position.
// The lattice is a finite discrete grid with spacing latticeSpacing world-units and
// half-extent latticeHalf cells in each axis (valid coords are [-latticeHalf, latticeHalf]).
//
// Priority: Cell (lattice) takes precedence over the free-form Pos when both are set.
// The free-form Pos path in nodeWorldPos is untouched; a nil Cell falls through to it.
//
// Conversions:
//   - latticeToWorld(i, j, k) → (x, y, z)  — clamp each coord, then multiply by spacing.
//   - worldToLattice(x, y, z) → (i, j, k)  — divide by spacing, round, clamp.

package Wiring

import "math"

const (
	// latticeSpacing and latticeHalf are DERIVED from the initial node layout
	// (topology/view/nodes/*.json), not hand-picked. spacing = the minimum
	// pairwise distance between node world-centers (so the two closest nodes land
	// on adjacent cells and the rest spread proportionally, preserving the graph's
	// shape — no collapse, no depth-stacking). latticeHalf = max(|i|,|j|,|k|) over
	// all node cells + a 2-cell margin, so every node fits without clamping.
	latticeSpacing = 46.5425 // min pairwise distance between initial node centers
	latticeHalf    = 10      // layout extent (max |cell| = 8) + 2-cell margin
)

// clampLattice clamps c to [-latticeHalf, latticeHalf].
func clampLattice(c int) int {
	if c < -latticeHalf {
		return -latticeHalf
	}
	if c > latticeHalf {
		return latticeHalf
	}
	return c
}

// latticeToWorld converts integer lattice coordinates (i, j, k) to world-space
// (x, y, z). Each coordinate is clamped to [-latticeHalf, latticeHalf] before
// scaling, so the result is always within the finite lattice box.
// Cell (0,0,0) maps to the world origin (0,0,0).
func latticeToWorld(i, j, k int) (x, y, z float64) {
	x = float64(clampLattice(i)) * latticeSpacing
	y = float64(clampLattice(j)) * latticeSpacing
	z = float64(clampLattice(k)) * latticeSpacing
	return
}

// worldToLattice converts world-space (x, y, z) to the nearest integer lattice
// cell (i, j, k). Each axis is divided by latticeSpacing, rounded to the nearest
// integer, and clamped to [-latticeHalf, latticeHalf].
func worldToLattice(x, y, z float64) (i, j, k int) {
	i = clampLattice(int(math.Round(x / latticeSpacing)))
	j = clampLattice(int(math.Round(y / latticeSpacing)))
	k = clampLattice(int(math.Round(z / latticeSpacing)))
	return
}
