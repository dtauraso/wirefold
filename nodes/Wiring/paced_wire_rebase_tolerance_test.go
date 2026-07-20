// paced_wire_rebase_tolerance_test.go — docs/planning/visual-editor/
// wire-owns-itself.md settled the two-copy skew question this file used to
// document: before the restructure, a bead's placementTick was stamped by the
// SOURCE goroutine's own clock copy while ReviseInFlightGeometry read nowTick
// from the EDGE MOVER's own (different) clock copy, so the two subtracted
// readings could disagree by about one speed-change's worth of skew. After the
// restructure there is only ONE clock copy involved on either side of that
// subtraction: the wire's own (edgeMover.run's clk), which now stamps
// placementTick itself (drainPlacements) AND is the same reading
// ReviseInFlightGeometry uses. There is no second copy left to disagree with.
//
// This test proves that directly: place a bead, apply a speed change, and rebase
// mid-flight, all against ONE clock copy — the recovered fraction t must match
// the true elapsed-fraction EXACTLY (to float rounding), not merely within the
// old accepted skew band, because the skew source no longer exists.
package Wiring

import (
	"context"
	"testing"
	"time"
)

func TestReviseInFlightGeometryNoSkewWithSingleClockCopy(t *testing.T) {
	const crossTicks = 40.0
	pw := NewPacedWire(0, PulseSpeedWuPerTick)
	arc := crossTicks * PulseSpeedWuPerTick
	inFlightMs := crossTicks * MsPerTick

	ctx := context.Background()
	clk := NewRealClock()

	// Place via the production path (Send + DriveOneCycle): the WIRE's own
	// clock reading stamps placementTick.
	if pw.Send(0, beadPlacement{InFlightMs: inFlightMs, Start: vec3{}, End: vec3{X: 1}}) != SendPlaced {
		t.Fatalf("Send failed")
	}
	placementTick := clk.Tick()
	pw.DriveOneCycle(ctx, placementTick)

	// A speed change banked on THIS SAME clock copy — no second copy to skew
	// against.
	clk.SetSpeed(2)

	time.Sleep(10 * tickPeriod)

	nowTick := clk.Tick()
	trueT := (float64(nowTick) - float64(placementTick)) / crossTicks

	newSeg := wireSegment{Start: vec3{}, End: vec3{X: 1}}
	pw.ReviseInFlightGeometry(nowTick, arc, newSeg)

	segs := pw.InFlightSegments()
	if len(segs) != 1 {
		t.Fatalf("expected 1 in-flight bead, got %d", len(segs))
	}
	rebasedPlacementTick := pw.inflight[0].placementTick
	gotT := (float64(nowTick) - rebasedPlacementTick) / crossTicks

	diff := gotT - trueT
	if diff < 0 {
		diff = -diff
	}
	// Single clock copy on both sides of the subtraction: the only slack left is
	// float rounding, not a cross-goroutine skew band.
	const tolerance = 1e-9
	if diff > tolerance {
		t.Fatalf("rebase fraction drifted %.9f beyond float tolerance %.9f: trueT=%.6f gotT=%.6f",
			diff, tolerance, trueT, gotT)
	}
}
