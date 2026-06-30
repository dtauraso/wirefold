// curve_params.go — single source of truth for curve-shape constants shared
// between the Go network and the TS visual layer.
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
// millisecond.  Both Go (simLatencyMs) and TS visual layer (travel
// duration) derive timing from this value.
//
// Matches PULSE_SPEED_WU_PER_MS in the generated curve-params.ts.
const CurveParamPulseSpeedWuPerMs = 0.04

// CurveParamMinArcLength is the minimum arc length in world units.
// Prevents zero-duration pulses when two nodes are co-located.
const CurveParamMinArcLength = 1.0


// CurveParamNodeRadiusDivisor is the divisor applied to min(width,height)
// to obtain the node sphere radius.  Matches nodeRadius in geometry-helpers.ts
// (Math.min(width, height) / 4); port endpoints sit on this sphere surface.
const CurveParamNodeRadiusDivisor = 4

// vec3 is a minimal 3-D vector used by port-geometry math.
type vec3 struct{ X, Y, Z float64 }

// wireSegment is one edge's straight-line segment from the source OUT-port world
// position to the dest IN-port world position. It is per-edge geometry threaded
// from the loader onto the source Out so each placed bead carries the segment it
// is drawn on. P(t) = Start + t*(End-Start).
type wireSegment struct{ Start, End vec3 }

func (a vec3) sub(b vec3) vec3 { return vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a vec3) add(b vec3) vec3 { return vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a vec3) scale(s float64) vec3 {
	return vec3{a.X * s, a.Y * s, a.Z * s}
}
func (a vec3) dot(b vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
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

// lerp linearly interpolates between a and b at parameter t.
// P(t) = a + t*(b-a). Used by the position stream to evaluate a bead's position.
func lerp(a, b vec3, t float64) vec3 {
	return a.add(b.sub(a).scale(t))
}

// chordLength returns the straight-line distance |b - a|, floored at
// CurveParamMinArcLength. This is the arc length of a straight-segment edge.
func chordLength(a, b vec3) float64 {
	l := b.sub(a).length()
	if l < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return l
}
