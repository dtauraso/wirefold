// curve_params.go — single source of truth for curve-shape constants shared
// between the Go substrate and the TS visual layer.
//
// Codegen: tools/gen-node-defs reads this file and emits
// tools/topology-vscode/src/schema/curve-params.ts.
// After changing any constant here, regenerate with:
//   cd tools/topology-vscode && npm run gen:node-defs
//
// curve-params constants are prefixed with CurveParam so gen-node-defs can
// identify them via the "CurveParam" name prefix.

package Wiring

import "math"

// CurveParamPulseSpeedWuPerMs is the uniform pulse speed in world-units per
// millisecond.  Both substrate (simLatencyMs) and TS visual layer (travel
// duration) derive timing from this value.
//
// Matches PULSE_SPEED_WU_PER_MS in the generated curve-params.ts.
const CurveParamPulseSpeedWuPerMs = 0.08

// CurveParamMinArcLength is the minimum arc length in world units.
// Prevents zero-duration pulses when two nodes are co-located.
const CurveParamMinArcLength = 1.0

// CurveParamBulgeFactor is the perpendicular offset of the Bezier control
// point as a fraction of the chord length.  The control point sits at the
// chord midpoint offset by (BulgeFactor * chordLength) in the perpendicular
// direction.  Matches the `span * 0.25` formula in buildEdgeCurve (TS).
const CurveParamBulgeFactor = 0.25

// CurveParamBezierSampleCount is the number of segments used when
// numerically integrating the quadratic Bezier arc length.
// 64 segments gives < 0.01% error for typical bulge ratios (< 0.5).
const CurveParamBezierSampleCount = 64

// CurveParamNodeRadiusDivisor is the divisor applied to min(width,height)
// to obtain the node sphere radius.  Matches nodeRadius in geometry-helpers.ts
// (Math.min(width, height) / 4); port endpoints sit on this sphere surface.
const CurveParamNodeRadiusDivisor = 4

// CurveParamSlotPct0/1/2 are the percentage positions along a node side for
// the three snap slots (0=25%, 1=50%, 2=75%).  Matches the SLOT_PCT table in
// geometry-helpers.ts.  A port with no explicit slot is auto-spaced instead.
const (
	CurveParamSlotPct0 = 25
	CurveParamSlotPct1 = 50
	CurveParamSlotPct2 = 75
)

// slotPct returns the side-percentage for a snap slot index (0..2).
func slotPct(slot int) float64 {
	switch slot {
	case 0:
		return CurveParamSlotPct0
	case 1:
		return CurveParamSlotPct1
	default:
		return CurveParamSlotPct2
	}
}

// BezierArcLength computes the arc length of a quadratic Bezier curve whose
// endpoints are p0 and p2 in 2-D (x, y) using the given bulgeFactor and
// number of samples.  The control point p1 is placed at the chord midpoint
// offset perpendicularly by bulgeFactor * chordLength, matching the formula
// in TS's buildEdgeCurve.  Result is floored at CurveParamMinArcLength.
func BezierArcLength(p0x, p0y, p2x, p2y, bulgeFactor float64, samples int) float64 {
	// Chord direction and perpendicular.
	chordX := p2x - p0x
	chordY := p2y - p0y
	chordLen := math.Sqrt(chordX*chordX + chordY*chordY)

	// Midpoint of chord.
	midX := (p0x + p2x) * 0.5
	midY := (p0y + p2y) * 0.5

	// Perpendicular to chord direction: rotate (chordX,chordY) by 90° CCW and
	// normalise.  Matches THREE.Vector3(0,0,1).cross(edgeDir).normalize() in TS
	// (z-cross of a 2-D direction in the xy plane gives the CCW perpendicular).
	var perpX, perpY float64
	if chordLen > 0 {
		perpX = -chordY / chordLen
		perpY = chordX / chordLen
	}

	// Control point p1 = midpoint + perpendicular * bulgeFactor * chordLen.
	p1x := midX + perpX*bulgeFactor*chordLen
	p1y := midY + perpY*bulgeFactor*chordLen

	// Numerically integrate arc length by summing segment lengths.
	n := max(samples, 1)
	inv := 1.0 / float64(n)
	// Start at t=0.
	prevX := p0x
	prevY := p0y
	total := 0.0
	for i := 1; i <= n; i++ {
		t := float64(i) * inv
		u := 1.0 - t
		// Quadratic Bezier: B(t) = u²·p0 + 2ut·p1 + t²·p2
		bx := u*u*p0x + 2*u*t*p1x + t*t*p2x
		by := u*u*p0y + 2*u*t*p1y + t*t*p2y
		dx := bx - prevX
		dy := by - prevY
		total += math.Sqrt(dx*dx + dy*dy)
		prevX, prevY = bx, by
	}

	if total < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return total
}

// vec3 is a minimal 3-D vector used by the port-to-port curve math so the Go
// arc length matches buildPortCurve in geometry-helpers.ts exactly.
type vec3 struct{ X, Y, Z float64 }

func (a vec3) sub(b vec3) vec3 { return vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a vec3) add(b vec3) vec3 { return vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a vec3) scale(s float64) vec3 {
	return vec3{a.X * s, a.Y * s, a.Z * s}
}
func (a vec3) length() float64 {
	return math.Sqrt(a.X*a.X + a.Y*a.Y + a.Z*a.Z)
}
func (a vec3) normalize() vec3 {
	l := a.length()
	if l == 0 {
		return vec3{}
	}
	return vec3{a.X / l, a.Y / l, a.Z / l}
}

// cross returns a × b.
func cross(a, b vec3) vec3 {
	return vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

// PortCurveArcLength computes the arc length of the quadratic Bezier curve that
// runs from source-output port world position p0 to target-input port world
// position p2, mirroring buildPortCurve in geometry-helpers.ts exactly:
//   - mid   = (p0+p2)/2
//   - dir   = normalize(p2-p0)
//   - lift  = normalize((0,0,1) × dir)
//   - span  = |p2-p0|
//   - p1    = mid + lift * (span * bulgeFactor)
//   - length integrated over `samples` segments of the 3-D quadratic Bezier
//
// Result is floored at CurveParamMinArcLength.
func PortCurveArcLength(p0, p2 vec3, bulgeFactor float64, samples int) float64 {
	mid := p0.add(p2).scale(0.5)
	edgeDir := p2.sub(p0).normalize()
	lift := cross(vec3{0, 0, 1}, edgeDir).normalize()
	span := p2.sub(p0).length()
	p1 := mid.add(lift.scale(span * bulgeFactor))

	n := max(samples, 1)
	inv := 1.0 / float64(n)
	prev := p0
	total := 0.0
	for i := 1; i <= n; i++ {
		t := float64(i) * inv
		u := 1.0 - t
		// Quadratic Bezier: B(t) = u²·p0 + 2ut·p1 + t²·p2
		b := p0.scale(u * u).add(p1.scale(2 * u * t)).add(p2.scale(t * t))
		total += b.sub(prev).length()
		prev = b
	}
	if total < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return total
}
