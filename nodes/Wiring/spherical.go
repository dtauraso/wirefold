package Wiring

import "math"

// spherical.go — spherical (angle-only) primitives for the polar VIEWPOINT model.
//
// This is the "no Cartesian" toolkit: a direction is an angle pair, a rotation is an
// axis-direction + angle, and every operation is spherical trigonometry on those angles.
// There are NO vectors, matrices, or quaternions here — those exist only in the renderer
// (three.js), built at draw time from the angles this package produces. (Tests may use a
// Cartesian oracle to verify the trig; production code never does.)
//
// Pole convention matches polar.go: θ = angle from +y (0=up, π=down), φ = azimuth around
// +y (0 = +x, increasing toward +z).

// dir is a direction on the unit sphere (pole = +y).
type dir struct {
	Theta float64 // angle from +y, 0..π
	Phi   float64 // azimuth around +y, -π..π
}

// rot is a rotation: a right-hand turn by Angle about the Axis direction.
type rot struct {
	Axis  dir
	Angle float64
}

// wrapPi folds an angle into (-π, π].
func wrapPi(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

// angularDistance is the great-circle angle between two directions (spherical law of
// cosines on the pole-frame angles — pure angle arithmetic).
func angularDistance(a, b dir) float64 {
	cd := math.Cos(a.Theta)*math.Cos(b.Theta) +
		math.Sin(a.Theta)*math.Sin(b.Theta)*math.Cos(a.Phi-b.Phi)
	return math.Acos(clamp(cd, -1, 1))
}

// azimuthFrom expresses p in the frame POLED at `pole`: returns the colatitude c
// (= angularDistance(pole, p)) and the bearing psi of p about the pole. The bearing is an
// atan2 of two UNNORMALIZED terms (the great-circle bearing formula) — there is no
// division anywhere, so the pole and axis-coincidence cases resolve as atan2(0,0)=0 (a
// finite, correct value, since azimuth is arbitrary there) with no special case.
func azimuthFrom(pole, p dir) (c, psi float64) {
	c = angularDistance(pole, p)
	dphi := p.Phi - pole.Phi
	psi = math.Atan2(
		math.Sin(p.Theta)*math.Sin(dphi),
		math.Sin(pole.Theta)*math.Cos(p.Theta)-math.Cos(pole.Theta)*math.Sin(p.Theta)*math.Cos(dphi),
	)
	return c, psi
}

// fromAxisFrame is the inverse of azimuthFrom: given a colatitude c and bearing psi in the
// frame poled at `pole`, return the base direction. The new θ comes from the cosine rule
// (acos, clamped only to its valid [-1,1] domain — exact, not a tolerance), and the
// azimuth offset Δφ is again an atan2 of two unnormalized terms — no division, no poles.
func fromAxisFrame(pole dir, c, psi float64) dir {
	cosT := clamp(math.Cos(pole.Theta)*math.Cos(c)+math.Sin(pole.Theta)*math.Sin(c)*math.Cos(psi), -1, 1)
	theta := math.Acos(cosT)
	dphi := math.Atan2(
		math.Sin(c)*math.Sin(psi),
		math.Sin(pole.Theta)*math.Cos(c)-math.Cos(pole.Theta)*math.Sin(c)*math.Cos(psi),
	)
	return dir{Theta: theta, Phi: wrapPi(pole.Phi + dphi)}
}

// rotateDir turns direction p by `angle` (right-hand) about the `axis` direction. The
// rotation is "add to the azimuth about the axis": re-express p in the axis frame, advance
// its position angle, convert back. azimuthFrom/fromAxisFrame are exact inverses, so a
// zero angle is an exact no-op.
func rotateDir(p, axis dir, angle float64) dir {
	c, psi := azimuthFrom(axis, p)
	return fromAxisFrame(axis, c, psi+angle)
}

// arcBetween is the shortest-arc rotation carrying `from` to `to`: the axis is the pole of
// the great circle through them (90° from `from`, a quarter turn past `to`'s bearing) and
// the angle is their separation. rotateDir(from, r.Axis, r.Angle) == to.
func arcBetween(from, to dir) rot {
	c, psi := azimuthFrom(from, to)
	axis := fromAxisFrame(from, math.Pi/2, psi+math.Pi/2)
	return rot{Axis: axis, Angle: c}
}

// angleAboutAxis returns the signed rotation about a fixed `axis` direction that carries
// `from` to `to`, measured as the azimuth difference in the frame poled at the axis
// (psi_to − psi_from), wrapped to (−π, π]. This is the locked-disk quantity for
// handhold-constrained rotation: the axis is frozen at gesture start; only the angle about
// it tracks the cursor. Mirrors polar.ts angleAboutAxis (spherical edition).
func angleAboutAxis(from, to, axis dir) float64 {
	_, psiFrom := azimuthFrom(axis, from)
	_, psiTo := azimuthFrom(axis, to)
	return wrapPi(psiTo - psiFrom)
}
