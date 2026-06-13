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

// trainDurationMs / beadSpacingMs define the paced emission train a node fire
// starts on an Out: the fired value is placed every beadSpacingMs for
// trainDurationMs, so ~trainDurationMs/beadSpacingMs + 1 beads ride the
// multi-bead wire at once (the first places immediately on fire). Spacing
// (400 ms) × pulse speed (0.04 wu/ms) = 16 wu — 2× the 8 wu bead diameter, so
// the beads never intersect. Timed on the one clock (the same active-elapsed
// reading the wire walkers use); the pause gate freezes the pacer with them.
const (
	trainDurationMs = 2000
	beadSpacingMs   = 400
)

// recvGateMs is the Recv-side refractory window. A node fire emits a train of
// beads (trainDurationMs long); without a gate the receiver would consume EACH
// bead and fire once per bead, so an N-bead train caused N fires (the ring
// thrashed, feedback values flipped mid-train). After a bead is consumed, Recv
// DROPS every further bead that arrives within recvGateMs, so one incoming train
// collapses to exactly one consumed bead (one fire). The window matches the train
// duration (2000 ms) — the span over which one train's beads arrive — so a single
// train yields a single fire and the next train (after the window) fires again.
// Measured on the one clock (active-elapsed), so it freezes on pause.
const recvGateMs = trainDurationMs

// RecvGateMs exports the refractory window for cross-package node tests that drive
// the feedback wire and must step the clock past it between distinct signals.
const RecvGateMs = recvGateMs

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
