package Wiring

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// Regression guard for the synchronous-drive throughput bug: a node that drives
// several outbound edges must drive them CONCURRENTLY (one DriveAll), so the
// goroutine blocks ~once for the longest edge — NOT once per edge in series.
//
// Two wires with different flight times (200ms, 100ms). Driving them together via
// DriveAll must finish in ~max(200,100)=200ms, well under the 300ms+ a sequential
// EmitOneDriven-per-edge would take. Uses a real clock (the paced path the unit
// suite otherwise never exercises — chan-mode tests skip the drive loop entirely).
func TestDriveAllIsConcurrent(t *testing.T) {
	tr := T.New(4096)
	clk := NewRealClock()

	mk := func(flightMs float64) *Out {
		pw := NewPacedWire(flightMs, 1) // pulseSpeed 1 -> arc==flight ms
		pw.SetClock(clk)
		pw.Trace = tr
		seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{flightMs, 0, 0}}
		return NewOutPaced(pw, context.Background(), "n", "p", tr, RuleFireAndForget, flightMs, flightMs, seg, "e")
	}
	o1 := mk(200)
	o2 := mk(100)

	items := []DriveItem{o1.PlaceDriven(7), o2.PlaceDriven(7)}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		DriveAll(context.Background(), items)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("DriveAll did not return within 3s (HANG)")
	}
	elapsed := time.Since(start)

	// Concurrent: ~200ms. Sequential would be ~300ms. Allow generous slack but
	// stay well below the sequential floor so the assertion is meaningful.
	if elapsed > 280*time.Millisecond {
		t.Fatalf("DriveAll took %v — beads appear to have been driven SEQUENTIALLY (expected ~200ms concurrent)", elapsed)
	}
}
