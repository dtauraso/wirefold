// rotating_pole.go — the ROTATING PER-NODE LOCAL POLE for the local-polar offset
// quantization (docs/planning/visual-editor/rotating-pole-frame.md).
//
// A node's local-polar entries (layout_holder.go LocalPolar) quantize each neighbor
// offset as (colatitude,bearing,radius) about a POLE — historically the fixed world +y
// direction. A fixed pole means any offset whose direction passes near +y hits the
// azimuth singularity: the φ-cell width collapses to zero as colatitude→0, so a small
// world nudge crosses unbounded φ-cells and the quantized bearing degrades to noise.
//
// This file makes the pole PER-NODE and MOVABLE: each node owns one pole direction
// (LayoutHolder.localPole), persisted in its meta.json, and every offset it quantizes is
// expressed in the frame poled AT that direction via spherical.go's azimuthFrom. When an
// offset's colatitude about the current pole falls below poleKickTheta (20°), the pole is
// KICKED — rotated along the great circle through (offset,pole) until the offset sits at
// the equator (colatitude π/2) — and every one of the node's other offsets is re-checked
// (a kick can put a second offset below threshold; bounded to maxPoleKicks iterations).
//
// No offset direction is ever reconstructed into a node POSITION here (the movement
// cascade stays distance-driven, node_move.go's Equalize/placeEqualRadii/
// placeAtDistanceFromBoth) — this only changes how a stored offset DIRECTION is
// quantized, never where a node itself sits.
package Wiring

import "math"

// poleKickTheta is the colatitude threshold (about a node's own local pole) below which
// an offset's quantized bearing is considered too close to the singularity: rationale in
// rotating-pole-frame.md — a φ-cell at 20° is still ~0.34x the equatorial width; below it
// the bearing degrades fast toward the blow-up, and 20° is far enough from typical
// neighbor spacing not to thrash the pole on every drag.
const poleKickTheta = math.Pi / 9 // 20 degrees

// maxPoleKicks bounds the kick-and-recheck loop. Real nodes have 2-3 neighbors; this is
// generous headroom while guaranteeing termination even in a pathological (e.g. exactly
// antipodal neighbors) case that can never fully satisfy every offset.
const maxPoleKicks = 8

// dirFromOffset converts a Cartesian, node-relative offset vector into its direction
// (dir, on the unit sphere) and radius. Reuses polar.go's cart2polar — the "pole" baked
// into cart2polar (world +y) is irrelevant here: only the resulting (Theta,Phi) pair is
// read as a direction, never as a quantity measured FROM world +y.
func dirFromOffset(o vec3) (d dir, r float64) {
	p := cart2polar(o)
	return dir{Theta: p.Theta, Phi: p.Phi}, p.R
}

// meanDir averages a set of directions as unit Cartesian vectors, renormalized, then
// converts back to a dir. Spherical averaging has no closed pole-frame form, so this is
// the one place this file touches Cartesian math directly. If the vectors exactly cancel
// (sum length 0) it falls back to the first direction (a deterministic tie-break; only
// possible with antipodal or symmetric offset sets).
func meanDir(ds []dir) dir {
	var sum vec3
	for _, d := range ds {
		sum = sum.add(polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi}))
	}
	if sum.length() == 0 {
		return ds[0]
	}
	p := cart2polar(sum)
	return dir{Theta: p.Theta, Phi: p.Phi}
}

// initLocalPole seeds a fresh pole for a node with no persisted pole yet: perpendicular
// to the mean of its current offset directions (i.e. the mean direction parked at the new
// pole's equator), so the "average" neighbor starts as far from the pole as possible.
// Deterministic given the offsets. Falls back to world +y (Theta=0,Phi=0) when there are
// no offsets at all (nothing to dodge yet).
func initLocalPole(offsets []dir) dir {
	if len(offsets) == 0 {
		return dir{Theta: 0, Phi: 0}
	}
	mean := meanDir(offsets)
	return fromAxisFrame(mean, math.Pi/2, 0)
}

// kickPoleAwayFrom moves pole so that oDir (currently at colatitude c about pole, c <
// poleKickTheta) lands at exactly the equator (colatitude π/2) of the new pole: it rotates
// pole along the great circle through (oDir,pole) — arcBetween(oDir,pole).Axis — by the
// remaining angle (π/2 - c). Verified empirically (rotating_pole_test.go
// TestKickIncreasesAngularDistance) that this INCREASES angularDistance(newPole,oDir), not
// decreases it — the sign of arcBetween(oDir,pole) (from oDir TO pole) is what makes the
// rotation carry pole further away from oDir rather than closer.
func kickPoleAwayFrom(pole, oDir dir, c float64) dir {
	arc := arcBetween(oDir, pole)
	return rotateDir(pole, arc.Axis, math.Pi/2-c)
}

// resolveLocalPole is the pure core of the rotating-pole model: given a node's CURRENT
// pole (ignored if hasPole is false) and a set of neighbor directions, it seeds the pole
// if unset, then kicks it away from any offense (bounded to maxPoleKicks iterations,
// re-checking every direction after each kick — a kick can put a second offset below
// threshold). Returns the final pole and whether every offset ended up clear of
// poleKickTheta (false iff the loop exited by hitting maxPoleKicks with a still-offending
// offset — e.g. an exactly-antipodal-neighbor pathology that can never fully satisfy every
// offset). Pure — no I/O, no locking, no world positions; callers persist the result,
// quantize bearings against it separately (azimuthFrom), and should surface a false return
// via a breadcrumb (this case is rare and should be observable, not silent).
func resolveLocalPole(pole dir, hasPole bool, dirs map[string]dir) (dir, bool) {
	if !hasPole {
		ordered := make([]dir, 0, len(dirs))
		for _, d := range dirs {
			ordered = append(ordered, d)
		}
		pole = initLocalPole(ordered)
	}
	converged := false
	for i := 0; i < maxPoleKicks; i++ {
		worstID, worstC := "", math.Pi
		for id, d := range dirs {
			c := angularDistance(pole, d)
			if c < poleKickTheta && c < worstC {
				worstID, worstC = id, c
			}
		}
		if worstID == "" {
			converged = true
			break // every offset clear of the threshold
		}
		pole = kickPoleAwayFrom(pole, dirs[worstID], worstC)
	}
	return pole, converged
}

// requantizeLocalPolarsAboutPole is the SINGLE site every LOCAL-polar write routes
// through once a node's LayoutHolder exists (node_move.go's four call sites). `updates`
// carries the FRESH world offset (vec3, this node as origin) for each neighbor whose
// distance/direction just changed; every OTHER neighbor already on lh keeps its existing
// radius untouched and has its bearing DIRECTION reconstructed EXACTLY from its stored
// (QuantITheta,QuantIPhi) under the OLD pole (fromAxisFrame is azimuthFrom's exact
// inverse) — not re-measured, just re-expressed under the (possibly kicked) new pole. The
// pole is always resolved against the WHOLE neighbor set, never just the neighbor being
// touched, so a kick sees the full picture. Returns the final pole (also stored on lh) and
// whether resolveLocalPole converged (false iff it hit maxPoleKicks with a still-offending
// offset — rare, e.g. an antipodal-neighbor pathology). Callers with a *T.Trace reachable
// should breadcrumb a false return ("pole.kick.uncapped") so the case is observable rather
// than silent.
func requantizeLocalPolarsAboutPole(lh *LayoutHolder, updates map[string]vec3) (dir, bool) {
	pole, hasPole := lh.LocalPole()
	existing := lh.LocalPolarsSnapshot()

	dirs := make(map[string]dir, len(existing)+len(updates))
	radii := make(map[string]float64, len(updates))
	for _, lp := range existing {
		if _, fresh := updates[lp.To]; fresh {
			continue
		}
		if hasPole {
			t, p, _ := lp.effectiveSteps()
			dirs[lp.To] = fromAxisFrame(pole, float64(lp.QuantITheta)*t, float64(lp.QuantIPhi)*p)
		}
	}
	for id, o := range updates {
		d, r := dirFromOffset(o)
		dirs[id] = d
		radii[id] = r
	}

	finalPole, converged := resolveLocalPole(pole, hasPole, dirs)
	lh.SetLocalPole(finalPole)

	// Re-quantize every UNCHANGED neighbor's bearing about the (possibly kicked) final
	// pole, preserving its own step constants and radius exactly.
	for _, lp := range existing {
		if _, fresh := updates[lp.To]; fresh {
			continue
		}
		d, ok := dirs[lp.To]
		if !ok {
			continue // no persisted pole yet and this entry had nothing to reconstruct from
		}
		t, p, r := lp.effectiveSteps()
		c, psi := azimuthFrom(finalPole, d)
		lh.SetLocalPolar(lp.To, int(math.Round(c/t)), int(math.Round(psi/p)), lp.QuantIR, t, p, r)
	}
	// Write every FRESH neighbor's bearing + radius about the final pole, on its own
	// (possibly newly-established) step constants.
	for id := range updates {
		t, p, rStep := lh.localPolarSteps(id)
		d := dirs[id]
		c, psi := azimuthFrom(finalPole, d)
		lh.SetLocalPolar(id, int(math.Round(c/t)), int(math.Round(psi/p)), int(math.Round(radii[id]/rStep)), t, p, rStep)
	}
	return finalPole, converged
}
