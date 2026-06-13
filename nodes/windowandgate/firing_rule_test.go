package andgate

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// clearSink is a thread-safe io.Writer that counts window_clear breadcrumbs
// written to the trace sink, so a test can observe sim-time window timeouts.
type clearSink struct {
	mu sync.Mutex
	n  int
}

func (s *clearSink) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "window_clear") {
		s.mu.Lock()
		s.n++
		s.mu.Unlock()
	}
	return len(p), nil
}

func (s *clearSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func run(left, right int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	fromLeft := make(chan int, 1)
	fromRight := make(chan int, 1)
	toPassed := make(chan int, 1)
	node := &Node{
		Fire:      func() { tr.Fire("irg") },
		FromLeft:  Wiring.NewIn(fromLeft, "irg", "FromLeft", tr),
		FromRight: Wiring.NewIn(fromRight, "irg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(toPassed, "irg", "ToPassed", tr),
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	fromLeft <- left
	fromRight <- right
	select {
	case v := <-toPassed:
		cancel()
		wg.Wait()
		return v, nil
	case <-time.After(1000 * time.Millisecond):
		cancel()
		wg.Wait()
		return -1, nil
	}
}

// AND(1,1)=1.
func TestAndBothActive(t *testing.T) {
	got, _ := run(1, 1)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// AND(1,0)=0.
func TestAndLeftOnly(t *testing.T) {
	got, _ := run(1, 0)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// AND(0,1)=0.
func TestAndRightOnly(t *testing.T) {
	got, _ := run(0, 1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// AND(0,0)=0.
func TestAndNeitherActive(t *testing.T) {
	got, _ := run(0, 0)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// deliver places a value on a paced In wire and delivers it into the slot so a
// PollRecv on the matching In returns it. arcLength sets the wire's SimLatencyMs.
func newInputWire(arcLength float64, tr *T.Trace, target, handle string) *Wiring.PacedWire {
	pw := Wiring.NewPacedWire(arcLength, Wiring.PulseSpeedWuPerMs)
	pw.Target = target
	pw.TargetHandle = handle
	pw.Trace = tr
	return pw
}

// send places a value on a paced In wire and drives Go's clock past the bead's
// in-flight time so the wire delivers it into the slot (clock-delivery contract;
// replaces the old NotifyDelivered trigger). It uses a per-call FakeClock and a
// fixed inFlightMs, advances past the deadline, then waits until the bead has
// landed (InFlight cleared) so the helper is synchronous like the old one.
func send(t *testing.T, pw *Wiring.PacedWire, v int) {
	t.Helper()
	ctx := context.Background()
	clk := Wiring.NewFakeClock()
	pw.SetClock(clk)
	const inFlightMs = 10
	if err := pw.SendDeliverOnly(ctx, v, inFlightMs); err != nil {
		t.Fatalf("Send: %v", err)
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

// TestPauseFreezesWindowAndDwell drives the window + dwell off an injected
// active-elapsed clock and asserts:
//   - while the clock does NOT advance (paused), a single held input does NOT
//     time out, even though real wall-time passes well past W;
//   - advancing the clock past W with only one input held DOES clear it
//     (window_clear), proving the timeout is measured in sim time;
//   - the dwell only completes once the clock advances past fireDwellMs.
func TestPauseFreezesWindowAndDwell(t *testing.T) {
	// Sink the trace so we can observe the window_clear breadcrumb (breadcrumbs
	// are sink-only, not buffered events). cleared counts how many fired.
	var clears clearSink
	tr := T.NewWithSink(0, &clears)
	defer tr.Close()

	// arcLength 8 → SimLatencyMs = 100ms → W = 150ms.
	left := newInputWire(8, tr, "irg", "FromLeft")
	right := newInputWire(8, tr, "irg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	// Sim clock the node times against. It stays PAUSED (no Advance) until we
	// choose to step it, while real wall-time keeps running underneath.
	simClk := Wiring.NewFakeClock()

	fired := make(chan struct{}, 4)
	node := &Node{
		Fire:      func() { fired <- struct{}{} },
		Now:       func() time.Duration { return simClk.Now() },
		FromLeft:  Wiring.NewInPaced(left, ctx, "irg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "irg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "irg", "ToPassed", tr),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// One input held; sim clock frozen. A window_clear breadcrumb signals a timeout.
	send(t, left, 1)

	// Real wall-time elapses well past W (150ms), but sim time is frozen → no clear.
	time.Sleep(400 * time.Millisecond)
	if clears.count() != 0 {
		t.Fatal("window cleared while sim clock was paused (timed on wall-clock)")
	}
	select {
	case <-fired:
		t.Fatal("node fired with only one input")
	default:
	}

	// Advance sim time past W (300ms here) with only one input held → must clear.
	simClk.Advance(400 * time.Millisecond)
	deadline := time.Now().Add(1 * time.Second)
	for clears.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("window did not clear after sim clock advanced past W")
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	// Now exercise the dwell on a fresh node/wires: deliver a full pair, then
	// prove the fire waits on the sim clock advancing past fireDwellMs (800ms),
	// not on wall-time. The window-timeout is gated off once both inputs are held,
	// so a frozen clock holds the dwell open indefinitely without clearing.
	dctx, dcancel := context.WithCancel(context.Background())
	dClk := Wiring.NewFakeClock()
	dLeft := newInputWire(8, tr, "irg2", "FromLeft")
	dRight := newInputWire(8, tr, "irg2", "FromRight")
	dFired := make(chan struct{}, 4)
	dNode := &Node{
		Fire:      func() { dFired <- struct{}{} },
		Now:       func() time.Duration { return dClk.Now() },
		FromLeft:  Wiring.NewInPaced(dLeft, dctx, "irg2", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(dRight, dctx, "irg2", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "irg2", "ToPassed", tr),
	}
	var dwg sync.WaitGroup
	dwg.Add(1)
	go func() { defer dwg.Done(); dNode.Update(dctx) }()
	defer func() { dcancel(); dwg.Wait() }()

	send(t, dLeft, 1)
	send(t, dRight, 1)

	// Both held, sim clock frozen → dwell never completes despite wall-time.
	select {
	case <-dFired:
		t.Fatal("node fired before sim clock advanced past fireDwellMs")
	case <-time.After(300 * time.Millisecond):
		// good: dwell not satisfied while sim time held below 800ms
	}

	// Advance sim time past fireDwellMs (800ms) → the dwell completes and fires.
	dClk.Advance((fireDwellMs + 50) * time.Millisecond)
	select {
	case <-dFired:
		// good: dwell completed once sim time crossed fireDwellMs
	case <-time.After(1 * time.Second):
		t.Fatal("node did not fire after sim clock advanced past fireDwellMs")
	}
}

// TestWindowFire: both inputs delivered within W → node fires once, both consumed.
func TestWindowFire(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	// arcLength 100 → SimLatencyMs = 100/0.08 = 1250ms → W = 1.5*1250 = 1875ms.
	left := newInputWire(100, tr, "irg", "FromLeft")
	right := newInputWire(100, tr, "irg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	fired := make(chan struct{}, 4)
	node := &Node{
		Fire:      func() { fired <- struct{}{} },
		FromLeft:  Wiring.NewInPaced(left, ctx, "irg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "irg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "irg", "ToPassed", tr),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	send(t, left, 1)
	send(t, right, 0)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("node did not fire when both inputs arrived within W")
	}

	cancel()
	wg.Wait()

	// Both wires consumed (slot empty): a fresh poll sees nothing.
	if _, ok := left.PollRecv(); ok {
		t.Fatal("left wire not consumed after fire")
	}
	if _, ok := right.PollRecv(); ok {
		t.Fatal("right wire not consumed after fire")
	}
}

// TestWindowClear: one input delivered, second never arrives → after W the held
// input is Done'd (drained), no fire, flags reset; a subsequent fresh pair fires.
func TestWindowClear(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	// arcLength 8 → SimLatencyMs = 8/0.08 = 100ms → W = 150ms (fast clear).
	left := newInputWire(8, tr, "irg", "FromLeft")
	right := newInputWire(8, tr, "irg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	fired := make(chan struct{}, 4)
	node := &Node{
		Fire:      func() { fired <- struct{}{} },
		FromLeft:  Wiring.NewInPaced(left, ctx, "irg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "irg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "irg", "ToPassed", tr),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// Only the left input arrives; right never does.
	// WaitConsumed must return once the node clears (Done drains the wire).
	send(t, left, 1)
	consumed := make(chan struct{}, 1)
	go func() { left.WaitConsumed(ctx); consumed <- struct{}{} }()

	select {
	case <-fired:
		t.Fatal("node fired with only one input present")
	case <-consumed:
		// held input was Done'd by the window clear
	case <-time.After(1 * time.Second):
		t.Fatal("window did not clear the held input within W")
	}

	// No fire happened.
	select {
	case <-fired:
		t.Fatal("node fired after clear")
	default:
	}

	// A subsequent fresh pair fires normally (flags reset).
	send(t, left, 1)
	send(t, right, 0)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("node did not fire on a fresh pair after clear")
	}
}
