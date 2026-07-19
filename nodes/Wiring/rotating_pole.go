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
// (docs/planning/visual-editor/deterministic-local-pole.md): `pole = localPole(offsets)`,
// where `offsets` are the node's CURRENT neighbor offset vectors. Home is world +y. Away
// from the singularity the pole IS home; when the offset closest to +y falls inside the
// singular zone (y-component > cos(poleKickTheta)) the pole tilts by a FIXED increment
// (poleKickTheta) away from that offset's horizontal projection — a closed-form Rodrigues
// rotation of +y, no re-solved angle, no iteration. The pole is never stored, never
// persisted, never carried in a message — every offset it quantizes is expressed in the
// frame poled AT that direction via spherical.go's azimuthFrom, and the pole is
// recomputed from scratch on every call.
//
// This is TRIG-FREE per update (matches docs/demos/polar-drag-3d.html's autoPole): "near
// +y" is a constant dot-product compare, the tilt axis is derived from one sqrt (no
// atan2), and the tilted pole vector is a closed-form combination of precomputed
// sin/cos(poleKickTheta) constants. The ONLY trig in this file is the cart↔polar
// boundary conversion of the final tilted pole vector (reusing polar.go's cart2polar).
//
// No offset direction is ever reconstructed into a node POSITION here (a drag is a
// free move, and neighborSetCReposition keeps a neighbor's stored bearing exactly as
// persisted) — this only changes how a stored offset DIRECTION is quantized, never
// where a node itself sits.
package Wiring

import "math"

// poleKickTheta is BOTH the tiny singular zone around +y and the dodge target. Its point
// is NOT to well-condition the bearing (near the pole you cannot — the φ-cell width scales
// with sin(colatitude), so it is inherently coarse there) but to make the EXACT singularity
// (colatitude 0, where φ is undefined) UNREACHABLE: an offset that would land at 0 is bumped
// away from it by a fixed increment, keeping the bearing defined and deterministic. A tiny
// zone (1°) is deliberate: it keeps the pole at home almost always (minimal disturbance to
// every OTHER neighbor's bearing, since the pole is their shared measurement axis), and it
// makes it rare for even one — and rarer still for two — offsets to fall inside at once, so
// the single-step dodge's one-offender limit is seldom exercised. Coarse-but-defined near
// the pole is fine; the bearing has no placement consumer (radius/QuantIR drives placement,
// measured separately).
const poleKickTheta = math.Pi / 180 // 1 degree

// One-time trig constants for the fixed-increment tilt — computed once at package init,
// never per-call. cosPoleKick is the "near +y" threshold (dot-product compare, no acos);
// sinPoleKick/cosPoleKick feed the closed-form Rodrigues rotation of +y below.
var (
	cosPoleKick = math.Cos(poleKickTheta)
	sinPoleKick = math.Sin(poleKickTheta)
)

// dirFromOffset converts a Cartesian, node-relative offset vector into its direction
// (dir, on the unit sphere) and radius. Reuses polar.go's cart2polar — the "pole" baked
// into cart2polar (world +y) is irrelevant here: only the resulting (Theta,Phi) pair is
// read as a direction, never as a quantity measured FROM world +y.
func dirFromOffset(o vec3) (d dir, r float64) {
	p := cart2polar(o)
	return dir{Theta: p.Theta, Phi: p.Phi}, p.R
}

// dirToVec3 converts a direction (unit-sphere point, pole=+y) to a Cartesian unit vector.
// This is the one place a stored-index RECONSTRUCTION (fromAxisFrame, spherical.go) needs
// a vec3 to feed localPole's tilt-axis geometry (localPole's contract takes offset
// vectors). It is boundary trig on a direction already reconstructed from stored indices
// — never a re-measurement of a live cartesian position.
func dirToVec3(d dir) vec3 {
	return polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi})
}

// localPole returns the deterministic measurement pole for a node whose neighbor offset
// vectors are `offsets` (raw Cartesian offsets, any nonzero length — normalized inside).
// Home is world +y. When the offset nearest +y is inside the singular zone, the pole
// tilts by the FIXED angle poleKickTheta about an axis perpendicular to that offset's
// horizontal (x,z) projection, so the offset dodges away from the pole (its colatitude
// about the new pole only increases, never re-lands at an exact target). Pure: no state,
// no I/O, no iteration. Deterministic tie-break (max Y, then min X, then min Z) so it
// never depends on map/slice order. TRIG-FREE per call except the final cart→polar
// conversion of the tilted pole vector.
func localPole(offsets []vec3) dir {
	home := dir{Theta: 0, Phi: 0} // world +y
	var closest vec3
	found := false
	for _, o := range offsets {
		u := o.normalize()
		if u.length() == 0 {
			continue // degenerate zero offset: no direction, ignore
		}
		if !found ||
			u.Y > closest.Y ||
			(u.Y == closest.Y && u.X < closest.X) ||
			(u.Y == closest.Y && u.X == closest.X && u.Z < closest.Z) {
			closest, found = u, true
		}
	}
	if !found || closest.Y <= cosPoleKick {
		return home
	}
	// Tilt axis: perpendicular to closest's horizontal projection, in the xz-plane
	// (so it stays orthogonal to +y — a·v=0 in the Rodrigues rotation below).
	ax, az := -closest.Z, closest.X
	h := math.Hypot(ax, az)
	if h < 1e-9 {
		// Degenerate: offender essentially ON +y, horizontal projection is ~zero.
		ax, az = 1, 0
	} else {
		ax, az = ax/h, az/h
	}
	// Closed-form Rodrigues rotation of v=+y about unit axis (ax,0,az) (which is
	// perpendicular to v, so a·v=0) by the fixed angle poleKickTheta:
	//   v' = v*cos(θ) + (a×v)*sin(θ) + a*(a·v)*(1-cos(θ))
	// with a·v=0 this reduces to v' = v*cos(θ) + (a×v)*sin(θ), and
	// a×v = (ax,0,az)×(0,1,0) = (0*0-az*1, az*0-ax*0, ax*1-0*0) = (-az, 0, ax).
	poleVec := vec3{
		X: -az * sinPoleKick,
		Y: cosPoleKick,
		Z: ax * sinPoleKick,
	}
	p := cart2polar(poleVec)
	return dir{Theta: p.Theta, Phi: p.Phi}
}
