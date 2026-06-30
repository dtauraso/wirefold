// Package gatetesthelper provides shared test utilities for the
// WindowAndInhibitLeftGate and WindowAndInhibitRightGate firing-rule tests.
// It is a real (non-_test) package so both test packages can import it.
package gatetesthelper

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// ClearSink is a thread-safe io.Writer that counts window_clear breadcrumbs
// written to the trace sink, so a test can observe sim-time window timeouts.
type ClearSink struct {
	mu sync.Mutex
	n  int
}

func (s *ClearSink) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "window_clear") {
		s.mu.Lock()
		s.n++
		s.mu.Unlock()
	}
	return len(p), nil
}

func (s *ClearSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// NewInputWire creates a PacedWire for use in gate tests.
func NewInputWire(arcLength float64, tr *T.Trace, target, handle string) *Wiring.PacedWire {
	pw := Wiring.NewPacedWire(arcLength, Wiring.PulseSpeedWuPerMs)
	pw.Target = target
	pw.TargetHandle = handle
	pw.Trace = tr
	return pw
}

// Send places a value on a paced In wire and drives Go's clock past the bead's
// in-flight time so the wire delivers it into the slot. It uses a per-call
// FakeClock, advances past the deadline, then waits until the bead has landed
// (InFlight cleared) so the helper is synchronous.
func Send(t *testing.T, pw *Wiring.PacedWire, v int) {
	t.Helper()
	ctx := context.Background()
	clk := Wiring.NewFakeClock()
	pw.SetClock(clk)
	const inFlightMs = 10
	if !pw.PlaceAndDriveDeliverOnly(ctx, v, inFlightMs) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	clk.Advance(inFlightMs * time.Millisecond)
	// Wait for the clock-delivery goroutine to fill the slot.
	deadline := time.Now().Add(time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("clock delivery did not fill slot after Advance")
		}
		time.Sleep(time.Millisecond)
	}
}
