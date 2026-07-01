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
// Reformulated onto the FakeClock so the assertion is about sim-time / delivery
// ordering, not wall-clock elapsed. Two wires with different in-flight times: a
// SHORT edge (100ms) and a LONG edge (200ms), both placed at clock 0. DriveAll
// runs on a background goroutine; the test then advances the fake clock to 100ms
// — past the short edge's deadline but before the long edge's.
//
// Concurrent drive touches both beads in the same loop, so the short bead
// delivers as soon as the clock reaches its 100ms deadline, while the long bead
// is still in flight. A SERIAL drive (drive the long edge fully to delivery,
// THEN start the short edge) could not deliver the short bead at clock 100: the
// long edge's drive parks until the clock reaches 200ms, and the short edge is
// never even touched before then. So "short delivered AND long not, at clock
// 100" holds for the concurrent impl and fails for a serial one.
func TestDriveAllIsConcurrent(t *testing.T) {
	tr := T.New(4096)
	clk := NewFakeClock()

	mk := func(flightMs float64) *Out {
		pw := NewPacedWire(flightMs, 1) // pulseSpeed 1 -> arc==flight ms -> deadline==flightMs
		pw.SetClock(clk)
		pw.Trace = tr
		seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{flightMs, 0, 0}}
		return NewOutPaced(pw, context.Background(), "n", "p", tr, RuleFireAndForget, flightMs, flightMs, seg, "e")
	}
	long := mk(200)
	short := mk(100)

	// Order the items long-first: a serial DriveAll would drive the long edge
	// (index 0) to delivery before ever starting the short edge, so this ordering
	// makes the serial failure mode maximally observable.
	items := []DriveItem{long.PlaceDriven(7), short.PlaceDriven(7)}

	done := make(chan struct{})
	go func() {
		DriveAll(context.Background(), items)
		close(done)
	}()

	delivered := func(o *Out) int {
		o.pw.mu.Lock()
		defer o.pw.mu.Unlock()
		return len(o.pw.delivered)
	}

	// Advance to the SHORT edge's deadline (100ms), still short of the long
	// edge's (200ms). Concurrent drive delivers the short bead here; the long
	// bead stays in flight.
	clk.Advance(100 * time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for delivered(short) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("short edge (100ms) was not delivered after clock reached 100ms — " +
				"beads appear to be driven SEQUENTIALLY (the long edge's drive is blocking the short edge)")
		}
		time.Sleep(time.Millisecond)
	}
	if delivered(long) != 0 {
		t.Fatalf("long edge (200ms) delivered at clock 100ms — expected still in flight (have %d delivered)", delivered(long))
	}

	// Advance to the long edge's deadline; both are now delivered and DriveAll
	// returns.
	clk.Advance(100 * time.Millisecond)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("DriveAll did not return after clock reached 200ms (HANG)")
	}
	if delivered(long) != 1 {
		t.Fatalf("long edge not delivered after clock reached 200ms (have %d)", delivered(long))
	}
}
