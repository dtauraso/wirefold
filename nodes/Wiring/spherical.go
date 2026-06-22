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

const sphEps = 1e-9

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
// (= angularDistance(pole, p)) and the position angle psi of p about the pole, measured
// from the half-great-circle pole→+y (ψ=0). Pure spherical trig (cosine + sine rules in
// the triangle +y–pole–p). When the pole is itself ±y the +y reference is degenerate, so
// the base azimuth φ is used directly (rotation about ±y is just φ ± angle).
func azimuthFrom(pole, p dir) (c, psi float64) {
	c = angularDistance(pole, p)
	st := math.Sin(pole.Theta)
	if st < sphEps {
		// Rotation about ±y is φ ± angle (right-hand). About +y a positive turn
		// DECREASES φ (carries +z→+x), so the pole-frame azimuth runs as -φ; about
		// -y it runs as +φ. (Verified against the Rodrigues oracle.)
		if pole.Theta < math.Pi/2 { // pole = +y
			return p.Theta, -p.Phi
		}
		return math.Pi - p.Theta, p.Phi // pole = -y
	}
	if math.Sin(c) < sphEps {
		return c, 0 // p on the pole axis: azimuth undefined
	}
	dphi := p.Phi - pole.Phi
	sPsi := math.Sin(p.Theta) * math.Sin(dphi)
	cPsi := (math.Cos(p.Theta) - math.Cos(pole.Theta)*math.Cos(c)) / st
	return c, math.Atan2(sPsi, cPsi)
}

// fromAxisFrame is the inverse of azimuthFrom: given a colatitude c and position angle psi
// in the frame poled at `pole`, return the base direction. (Cosine rule for the new θ,
// then sine+cosine rules for the azimuth offset Δφ about +y.)
func fromAxisFrame(pole dir, c, psi float64) dir {
	st := math.Sin(pole.Theta)
	if st < sphEps {
		if pole.Theta < math.Pi/2 { // pole = +y (azimuth runs as -φ; see azimuthFrom)
			return dir{Theta: c, Phi: wrapPi(-psi)}
		}
		return dir{Theta: math.Pi - c, Phi: wrapPi(psi)} // pole = -y
	}
	ct := clamp(math.Cos(pole.Theta)*math.Cos(c)+st*math.Sin(c)*math.Cos(psi), -1, 1)
	thetaP := math.Acos(ct)
	if math.Sin(thetaP) < sphEps {
		return dir{Theta: thetaP, Phi: pole.Phi} // result on a pole: φ undefined
	}
	sDphi := math.Sin(c) * math.Sin(psi)
	cDphi := (math.Cos(c) - math.Cos(pole.Theta)*ct) / st
	return dir{Theta: thetaP, Phi: wrapPi(pole.Phi + math.Atan2(sDphi, cDphi))}
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
