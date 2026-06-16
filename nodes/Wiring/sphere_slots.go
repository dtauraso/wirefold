// sphere_slots.go — sphere-SURFACE placement helpers for the polar layout.
//
// A node is a sphere of radius R (nodeR, port_geometry.go). There is no global
// slot grid and no Fibonacci distribution. Two pure helpers seed and quantize
// surface positions:
//
//   - projectToSphere: INITIAL placement only. Project a neighbor's current
//     position onto the node's sphere — seeds positions from the present layout.
//   - diameterStepAngle: the angular drag step on the surface for a node of a
//     given diameter (arc length = R*angle, so angle = diameter/R). A smaller
//     node has a finer step / more reachable places on the same sphere; the
//     diameter also prevents overlap.
//
// Pure helpers — B3 wires these into position computation; no behavior change here.

package Wiring

import "math"

// projectToSphere returns the point on the sphere of radius R centered at
// `center` that lies along the direction from `center` toward `neighborPos`:
//
//	pos = center + R * normalize(neighborPos - center)
//
// Used for INITIAL placement only: seed a neighbor's spot on a node's sphere
// from its current edge direction. If neighborPos coincides with center (zero
// length), the direction is undefined; we fall back to +Y so the result is still
// on the surface.
func projectToSphere(center vec3, R float64, neighborPos vec3) vec3 {
	dx := neighborPos.X - center.X
	dy := neighborPos.Y - center.Y
	dz := neighborPos.Z - center.Z
	len := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if len == 0 {
		return vec3{X: center.X, Y: center.Y + R, Z: center.Z}
	}
	s := R / len
	return vec3{X: center.X + dx*s, Y: center.Y + dy*s, Z: center.Z + dz*s}
}

// diameterStepAngle returns the angular step (radians) on a sphere of radius R
// for a node of the given diameter. Arc length = R*angle, so angle = diameter/R.
//
// This is the per-node drag-step quantum on the surface: a smaller diameter
// yields a smaller (finer) step, so a smaller node has more reachable places on
// the same sphere. Guards R>0 (a non-positive R yields a 0 step).
func diameterStepAngle(R, diameter float64) float64 {
	if R <= 0 {
		return 0
	}
	return diameter / R
}
