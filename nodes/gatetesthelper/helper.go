// Package gatetesthelper provides shared test utilities for the
// WindowAndInhibitLeftGate and WindowAndInhibitRightGate firing-rule tests.
// It is a real (non-_test) package so both test packages can import it.
package gatetesthelper

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// ClearSink is a thread-safe io.Writer that counts the gate's lifecycle
// breadcrumbs (window_open, dwell_start, window_clear) written to the trace
// sink, so a test can observe sim-time gate transitions deterministically
// instead of sleeping. window_open / dwell_start let a test wait until the gate
// has captured t0 / dwellStart against the clock BEFORE it advances the clock,
// eliminating the read-vs-advance ordering race.
type ClearSink struct {
	mu     sync.Mutex
	n      int // window_clear
	opens  int // window_open
	dwells int // dwell_start
}

var (
	windowClearBytes = []byte("window_clear")
	windowOpenBytes  = []byte("window_open")
	dwellStartBytes  = []byte("dwell_start")
)

func (s *ClearSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	if bytes.Contains(p, windowClearBytes) {
		s.n++
	}
	if bytes.Contains(p, windowOpenBytes) {
		s.opens++
	}
	if bytes.Contains(p, dwellStartBytes) {
		s.dwells++
	}
	s.mu.Unlock()
	return len(p), nil
}

// Count returns the number of window_clear breadcrumbs seen.
func (s *ClearSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// OpenCount returns the number of window_open breadcrumbs seen.
func (s *ClearSink) OpenCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens
}

// DwellCount returns the number of dwell_start breadcrumbs seen.
func (s *ClearSink) DwellCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dwells
}

// WaitCount polls get() until it reaches at least want, or fails after a bounded
// timeout. It replaces fixed wall-clock sleeps used to let the gate goroutine
// reach a known state: the poll is on real gate state (a breadcrumb counter), so
// the wait is deterministic and the timeout is only a missed-wake guard.
func WaitCount(t *testing.T, get func() int, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for get() < want {
		if time.Now().After(deadline) {
			t.Fatalf("gate did not reach %d %s (have %d)", want, what, get())
		}
		time.Sleep(time.Millisecond)
	}
}

// NewInputWire creates a PacedWire for use in gate tests.
func NewInputWire(arcLength float64, tr *T.Trace, target, handle string) *Wiring.PacedWire {
	pw := Wiring.NewPacedWire(arcLength, Wiring.PulseSpeedWuPerMs)
	pw.Target = target
	pw.TargetHandle = handle
	pw.Trace = tr
	return pw
}

// Send places a value on a paced In wire and steps Go's clock, one tick and
// one StepOnce at a time, past the bead's in-flight time so the wire delivers
// it into the slot. It uses a per-call FakeClock advanced tick-by-tick (the
// only delivery path is per-cycle StepOnce; there is no blocking drive).
func Send(t *testing.T, pw *Wiring.PacedWire, v int) {
	t.Helper()
	ctx := context.Background()
	clk := Wiring.NewFakeClock()
	pw.SetClock(clk)
	const inFlightMs = 10
	if !pw.PlaceDeliverOnly(v, inFlightMs) {
		t.Fatal("PlaceDeliverOnly returned false")
	}
	// ticksToCross = arc/pulseSpeed; the input wire uses pulseSpeed = PulseSpeedWuPerMs
	// so cross == inFlightMs ticks (the worst case). Step past it to force delivery.
	for i := 0; i < inFlightMs+2; i++ {
		clk.AdvanceTicks(1)
		pw.StepOnce(ctx)
	}
	// Wait for the delivery to fill the slot (StepOnce above already ran the
	// non-blocking handoff synchronously, but poll briefly for safety/parity
	// with the previous async-goroutine version).
	deadline := time.Now().Add(time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("StepOnce delivery did not fill slot after Advance")
		}
		time.Sleep(time.Millisecond)
	}
}
