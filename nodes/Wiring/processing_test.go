package Wiring

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// processHarness wires a single paced INPUT wire + In port and a single paced
// OUTPUT wire + Out port onto one FakeClock and a ProcessingGuard. It is the
// deterministic rig for the processing-window cases: place/deliver input beads by
// advancing the fake clock, then drive the output transit through the guard.
type processHarness struct {
	clk    *FakeClock
	tr     *T.Trace
	inPw   *PacedWire
	in     *In
	outPw  *PacedWire
	out    *Out
	guard  *ProcessingGuard
	cancel context.CancelFunc
}

func newProcessHarness(t *testing.T, inLatencyMs, outLatencyMs float64) *processHarness {
	t.Helper()
	clk := NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := NewPacedWire(inLatencyMs*PulseSpeedWuPerMs, PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr
	in := NewInPaced(inPw, ctx, "n", "in", tr)

	outPw := NewPacedWire(outLatencyMs*PulseSpeedWuPerMs, PulseSpeedWuPerMs)
	outPw.SetClock(clk)
	outPw.Trace = tr
	out := NewOutPaced(outPw, ctx, "n", "out", tr, RuleFireAndForget,
		outLatencyMs*PulseSpeedWuPerMs, outLatencyMs, wireSegment{}, "")

	h := &processHarness{clk: clk, tr: tr, inPw: inPw, in: in, outPw: outPw, out: out, cancel: cancel}
	h.guard = &ProcessingGuard{In: in}
	return h
}

func (h *processHarness) close() {
	h.cancel()
	h.tr.Close()
}

// inputDrained reports whether the input wire is empty (no bead in flight and no
// delivered value waiting) — i.e. the guard has consumed the mid-processing bead.
func (h *processHarness) inputDrained() bool {
	h.inPw.mu.Lock()
	defer h.inPw.mu.Unlock()
	return len(h.inPw.inflight) == 0 && len(h.inPw.delivered) == 0
}

// deliverInput places value v on the input wire and advances the fake clock by
// stepMs so the bead is delivered into the input wire's delivered queue.
func (h *processHarness) deliverInput(ctx context.Context, v int, stepMs float64) {
	h.inPw.PlaceAndDriveDeliverOnly(ctx, v, 0) // 0 latency: delivered on the next advance
	// Wires here use pulseSpeed = PulseSpeedWuPerMs, so one tick == one old ms-unit.
	h.clk.AdvanceTicks(int64(stepMs))
}

// waitFor polls cond up to timeout; returns true if cond became true.
func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Microsecond)
	}
	return cond()
}

// TestProcessingSameColorIgnored: a SAME-color bead arriving during the processing
// window is consumed + discarded silently, and the output is unaffected.
func TestProcessingSameColorIgnored(t *testing.T) {
	h := newProcessHarness(t, 0, 1000)
	defer h.close()
	ctx := context.Background()

	// Consume the first input (value 0) and place its output bead.
	h.deliverInput(ctx, 0, 1)
	v, ok := h.in.TryRecv()
	if !ok || v != 0 {
		t.Fatalf("first input: got (%d,%v), want (0,true)", v, ok)
	}
	item := h.out.PlaceDriven(0)

	// Run the processing window in the background (it blocks until output delivery).
	done := make(chan struct{})
	go func() { h.guard.Process(ctx, 0, []DriveItem{item}); close(done) }()

	// A SAME-color bead (0) arrives mid-processing (output deadline is 1000 ms away).
	h.deliverInput(ctx, 0, 1)
	// Wait until the guard has consumed it (input wire drained).
	if !waitFor(h.inputDrained, time.Second) {
		t.Fatal("same-color mid-processing bead was never consumed")
	}

	// Complete the output transit → window finishes.
	h.clk.AdvanceTicks(2000)
	<-done

	// Output unaffected: the delivered output value is 0.
	if dv, ok := h.outPw.PollRecv(); !ok || dv.(int) != 0 {
		t.Fatalf("output: got (%v,%v), want (0,true)", dv, ok)
	}
}

// TestProcessingDifferentColorDiscarded: a DIFFERENT-color bead during processing
// is consumed + discarded silently (NOT output), and the output from the original
// input is unaffected. No second output bead exists.
func TestProcessingDifferentColorDiscarded(t *testing.T) {
	h := newProcessHarness(t, 0, 1000)
	defer h.close()
	ctx := context.Background()

	h.deliverInput(ctx, 0, 1)
	v, ok := h.in.TryRecv()
	if !ok || v != 0 {
		t.Fatalf("first input: got (%d,%v), want (0,true)", v, ok)
	}
	item := h.out.PlaceDriven(0)

	done := make(chan struct{})
	go func() { h.guard.Process(ctx, 0, []DriveItem{item}); close(done) }()

	// A DIFFERENT-color bead (1) arrives mid-processing.
	h.deliverInput(ctx, 1, 1)
	// Wait until the guard has consumed it (input wire drained = bead discarded).
	if !waitFor(h.inputDrained, time.Second) {
		t.Fatal("different-color mid-processing bead was never consumed")
	}

	// Complete the output transit → window finishes.
	h.clk.AdvanceTicks(2000)
	<-done

	// The different bead was DISCARDED, not output: the delivered output value is 0,
	// and there is no second output bead.
	dv, ok := h.outPw.PollRecv()
	if !ok || dv.(int) != 0 {
		t.Fatalf("output: got (%v,%v), want (0,true) — different bead must not be output", dv, ok)
	}
	if _, ok := h.outPw.PollRecv(); ok {
		t.Fatal("a second output bead exists — the missed bead was wrongly processed")
	}
}

// TestProcessingSteadyOutputDelivered: steady single-bead operation (one input,
// one output, no mid-processing arrival) delivers output correctly.
func TestProcessingSteadyOutputDelivered(t *testing.T) {
	h := newProcessHarness(t, 0, 100)
	defer h.close()
	ctx := context.Background()

	h.deliverInput(ctx, 0, 1)
	v, ok := h.in.TryRecv()
	if !ok || v != 0 {
		t.Fatalf("input: got (%d,%v), want (0,true)", v, ok)
	}
	item := h.out.PlaceDriven(0)

	done := make(chan struct{})
	go func() { h.guard.Process(ctx, 0, []DriveItem{item}); close(done) }()

	h.clk.AdvanceTicks(200)
	<-done

	if dv, ok := h.outPw.PollRecv(); !ok || dv.(int) != 0 {
		t.Fatalf("output: got (%v,%v), want (0,true)", dv, ok)
	}
}
