package Wiring

import "math"

// polar.go — spherical (polar) coordinate type and conversions for the
// polar layout model (docs/planning/visual-editor/polar-coordinate-model.md).
//
// Pole = +y (vertical/up). Convention (matches the spec §2):
//
//	x = r·sinθ·cosφ
//	y = r·cosθ
//	z = r·sinθ·sinφ
//
//	r — radius (distance from the origin)
//	θ (Theta) — polar angle from +y: 0 = straight up, π/2 = equator, π = down
//	φ (Phi)   — azimuth around +y in the x–z plane: 0 = +x, increasing toward +z
//
// All angles are in radians. Conversions are pure functions of an origin-
// relative vector; the caller subtracts the frame origin first.

// polar is a point in spherical coordinates relative to some (caller-supplied)
// origin, pole = +y.
type polar struct {
	R     float64 // radius
	Theta float64 // polar angle from +y (radians, 0..π)
	Phi   float64 // azimuth around +y (radians, -π..π)
}

// polar2cart converts a polar coordinate to an origin-relative Cartesian vec3.
func polar2cart(p polar) vec3 {
	st := math.Sin(p.Theta)
	return vec3{
		X: p.R * st * math.Cos(p.Phi),
		Y: p.R * math.Cos(p.Theta),
		Z: p.R * st * math.Sin(p.Phi),
	}
}

// cart2polar converts an origin-relative Cartesian vec3 to polar (pole = +y).
// At the origin (r=0) θ and φ are 0. On the +y/-y axis (st=0) φ is 0 since
// azimuth is undefined there.
func cart2polar(v vec3) polar {
	r := v.length()
	if r == 0 {
		return polar{}
	}
	theta := math.Acos(clamp(v.Y/r, -1, 1))
	phi := math.Atan2(v.Z, v.X) // 0 when on the y-axis (Z=X=0)
	return polar{R: r, Theta: theta, Phi: phi}
}

// polarDist returns the straight-line distance between two points given in polar about the
// SAME origin (pole +y), via the spherical law of cosines — NO cartesian, no vector
// subtraction (polar-frame-rewrite.md: all geometry math stays polar; cartesian appears only
// at the GPU boundary):
//
//	cosγ = cosθ₁·cosθ₂ + sinθ₁·sinθ₂·cos(φ₁−φ₂)   // angle between the two radial vectors
//	d²   = r₁² + r₂² − 2·r₁·r₂·cosγ                // law of cosines on the triangle O,P₁,P₂
func polarDist(a, b polar) float64 {
	cosG := math.Cos(a.Theta)*math.Cos(b.Theta) +
		math.Sin(a.Theta)*math.Sin(b.Theta)*math.Cos(a.Phi-b.Phi)
	d2 := a.R*a.R + b.R*b.R - 2*a.R*b.R*cosG
	if d2 <= 0 {
		return 0
	}
	return math.Sqrt(d2)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
