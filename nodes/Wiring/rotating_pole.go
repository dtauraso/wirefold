// rotating_pole.go — the ROTATING PER-NODE LOCAL POLE for the local-polar offset
// quantization (docs/planning/visual-editor/rotating-pole-frame.md).
//
// A node's local-polar entries (layout_holder.go LocalPolar) quantize each neighbor
// offset as (colatitude,bearing,radius) about a POLE — historically the fixed world +y
// direction. A fixed pole means any offset whose direction passes near +y hits the
// azimuth singularity: the φ-cell width collapses to zero as colatitude→0, so a small
// world nudge crosses unbounded φ-cells and the quantized bearing degrades to noise.
//
// This file makes the pole a PURE, DETERMINISTIC FUNCTION OF LIVE GEOMETRY
// (docs/planning/visual-editor/deterministic-local-pole.md): `pole = localPole(dirs)`,
// where `dirs` is the node's CURRENT neighbor offset directions. Home is world +y. Away
// from the singularity the pole IS home; when the offset closest to +y falls inside the
// singular zone (colatitude < poleKickTheta, 1°) the pole tilts a little (one closed-form
// step, never iterated) so that offset lands at exactly colatitude poleKickTheta. The pole
// is never stored, never persisted, never carried in a message — every offset it quantizes
// is expressed in the frame poled AT that direction via spherical.go's azimuthFrom, and the
// pole is recomputed from scratch on every call.
//
// No offset direction is ever reconstructed into a node POSITION here (the movement
// cascade stays distance-driven, node_move.go's Equalize/placeEqualRadii/
// placeAtDistanceFromBoth) — this only changes how a stored offset DIRECTION is
// quantized, never where a node itself sits.
package Wiring

import "math"

// poleKickTheta is BOTH the tiny singular zone around +y and the dodge target. Its point
// is NOT to well-condition the bearing (near the pole you cannot — the φ-cell width scales
// with sin(colatitude), so it is inherently coarse there) but to make the EXACT singularity
// (colatitude 0, where φ is undefined) UNREACHABLE: an offset that would land at 0 is bumped
// to exactly poleKickTheta, keeping the bearing defined and deterministic. A tiny zone (1°)
// is deliberate: it keeps the pole at home almost always (minimal disturbance to every OTHER
// neighbor's bearing, since the pole is their shared measurement axis), and it makes it rare
// for even one — and rarer still for two — offsets to fall inside at once, so the single-step
// dodge's one-offender limit is seldom exercised. Coarse-but-defined near the pole is fine;
// the bearing has no placement consumer (radius/QuantIR drives placement, measured separately).
const poleKickTheta = math.Pi / 180 // 1 degree

// dirFromOffset converts a Cartesian, node-relative offset vector into its direction
// (dir, on the unit sphere) and radius. Reuses polar.go's cart2polar — the "pole" baked
// into cart2polar (world +y) is irrelevant here: only the resulting (Theta,Phi) pair is
// read as a direction, never as a quantity measured FROM world +y.
func dirFromOffset(o vec3) (d dir, r float64) {
	p := cart2polar(o)
	return dir{Theta: p.Theta, Phi: p.Phi}, p.R
}

// localPole returns the deterministic measurement pole for a node whose neighbor offset
// directions are `dirs`. Home is world +y; when the offset nearest +y is inside the
// singular zone it tilts a little away from that offset so the offset clears the zone.
// Pure: no state, no I/O, no iteration. Deterministic tie-break (Theta then Phi) so it
// never depends on map/slice order.
func localPole(dirs []dir) dir {
	home := dir{Theta: 0, Phi: 0} // world +y
	// colatitude of an offset about +y is just its Theta.
	minC := math.Pi
	var closest dir
	found := false
	for _, d := range dirs {
		if !found || d.Theta < minC || (d.Theta == minC && d.Phi < closest.Phi) {
			minC, closest, found = d.Theta, d, true
		}
	}
	if !found || minC >= poleKickTheta {
		return home
	}
	// Tilt home away from `closest` along the geodesic through them, by just enough to put
	// `closest` at colatitude poleKickTheta about the new pole. arcBetween/rotateDir are
	// pole-safe (atan2 of unnormalised terms), so minC≈0 resolves finite: the dodge
	// direction is arbitrary there but the RESULT (closest at poleKickTheta) is well-
	// conditioned regardless, because it is a single step, not an iterated one.
	return rotateDir(home, arcBetween(closest, home).Axis, poleKickTheta-minC)
}
