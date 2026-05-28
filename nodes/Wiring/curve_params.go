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
	n := samples
	if n < 1 {
		n = 1
	}
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
