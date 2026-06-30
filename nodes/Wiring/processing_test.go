package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// processHarness wires a single paced INPUT wire + In port and a single paced
// OUTPUT wire + Out port onto one FakeClock, plus a ProcessingGuard whose
// EmitStatus appends to a captured slice. It is the deterministic rig for the
// processing-window cases: place/deliver input beads by advancing the fake clock,
// then drive the output transit through the guard.
type processHarness struct {
	clk    *FakeClock
	tr     *T.Trace
	inPw   *PacedWire
	in     *In
	outPw  *PacedWire
	out    *Out
	guard  *ProcessingGuard
	cancel context.CancelFunc

	mu     sync.Mutex
	status []T.Event
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
	h.guard = &ProcessingGuard{
		In: in,
		EmitStatus: func(torusRed bool, missedValue int) {
			// Mirror the real injected closure's event shape so the test asserts on a
			// node-status event, not a bespoke struct.
			h.mu.Lock()
			h.status = append(h.status, T.Event{Kind: T.KindNodeStatus, Node: "n", TorusRed: torusRed, Value: missedValue})
			h.mu.Unlock()
		},
	}
	return h
}

func (h *processHarness) close() {
	h.cancel()
	h.tr.Close()
}

func (h *processHarness) statusEvents() []T.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]T.Event, len(h.status))
	copy(out, h.status)
	return out
}

// deliverInput places value v on the input wire and advances the fake clock by
// stepMs so the bead is delivered into the input wire's delivered queue.
func (h *processHarness) deliverInput(ctx context.Context, v int, stepMs float64) {
	h.inPw.PlaceAndDriveDeliverOnly(ctx, v, 0) // 0 latency: delivered on the next advance
	h.clk.Advance(time.Duration(stepMs * float64(time.Millisecond)))
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
// window is consumed + discarded with no torus event, and the output is unaffected.
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
	if !waitFor(func() bool { return !h.inPw.Occupied() }, time.Second) {
		t.Fatal("same-color mid-processing bead was never consumed")
	}
	if got := h.statusEvents(); len(got) != 0 {
		t.Fatalf("same-color bead produced %d status events, want 0: %+v", len(got), got)
	}

	// Complete the output transit → window finishes.
	h.clk.Advance(2000 * time.Millisecond)
	<-done

	if got := h.statusEvents(); len(got) != 0 {
		t.Fatalf("after finish: %d status events, want 0 (no error entered): %+v", len(got), got)
	}
	// Output unaffected: the delivered output value is 0.
	if dv, ok := h.outPw.PollRecv(); !ok || dv.(int) != 0 {
		t.Fatalf("output: got (%v,%v), want (0,true)", dv, ok)
	}
}

// TestProcessingDifferentColorErrors: a DIFFERENT-color bead during processing →
// exactly one torus-red NodeStatus carrying the missed value, the bead is discarded
// (not output), and a revert-to-normal event fires when processing completes.
func TestProcessingDifferentColorErrors(t *testing.T) {
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
	if !waitFor(func() bool { return len(h.statusEvents()) >= 1 }, time.Second) {
		t.Fatal("different-color bead produced no torus-red event")
	}
	got := h.statusEvents()
	if len(got) != 1 {
		t.Fatalf("got %d status events mid-processing, want exactly 1: %+v", len(got), got)
	}
	if !got[0].TorusRed || got[0].Value != 1 {
		t.Fatalf("torus-red event = %+v, want TorusRed=true Value=1 (missed)", got[0])
	}

	// Complete the output transit → window finishes → revert event.
	h.clk.Advance(2000 * time.Millisecond)
	<-done

	got = h.statusEvents()
	if len(got) != 2 {
		t.Fatalf("after finish: %d status events, want 2 (red + revert): %+v", len(got), got)
	}
	if got[1].TorusRed {
		t.Fatalf("revert event = %+v, want TorusRed=false", got[1])
	}
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

// TestProcessingSteadyNoSpuriousEvents: steady single-bead operation (one input,
// one output, no mid-processing arrival) emits no torus events.
func TestProcessingSteadyNoSpuriousEvents(t *testing.T) {
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

	h.clk.Advance(200 * time.Millisecond)
	<-done

	if got := h.statusEvents(); len(got) != 0 {
		t.Fatalf("steady operation produced %d status events, want 0: %+v", len(got), got)
	}
	if dv, ok := h.outPw.PollRecv(); !ok || dv.(int) != 0 {
		t.Fatalf("output: got (%v,%v), want (0,true)", dv, ok)
	}
}
