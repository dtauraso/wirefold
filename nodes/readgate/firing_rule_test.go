package readgate

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

func recv(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for output")
		return 0
	}
}

// FiresWhenBothPresent: value from FromInput is forwarded on ToChainInhibitor
// when FromChainInhibitor also arrives; inhibitor value is ignored.
func TestFiresWhenBothPresent(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromInput := make(chan int, 1)
	fromCI := make(chan int, 1)
	toCI := make(chan int, 1)

	node := &Node{
		Fire:               func() { tr.Fire("rg") },
		FromInput:          Wiring.NewIn(fromInput, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewIn(fromCI, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromInput <- 42
	fromCI <- 1
	got := recv(t, toCI)
	cancel()
	wg.Wait()

	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

// NoFireWithoutInhibitor: value alone must not emit.
func TestNoFireWithoutInhibitor(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromInput := make(chan int, 1)
	fromCI := make(chan int, 1)
	toCI := make(chan int, 1)

	node := &Node{
		Fire:               func() { tr.Fire("rg") },
		FromInput:          Wiring.NewIn(fromInput, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewIn(fromCI, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromInput <- 7
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case v := <-toCI:
		t.Fatalf("unexpected emission %d", v)
	default:
	}
}

// newInputWire builds a paced input wire whose arcLength sets its SimLatencyMs,
// so W = 1.5 * max(SimLatencyMs) is controllable per test.
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

// TestWindowFire: both inputs delivered within W → node fires once, both consumed,
// and the FromInput value is forwarded on ToChainInhibitor.
func TestWindowFire(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	// arcLength 100 → SimLatencyMs = 100/0.08 = 1250ms → W = 1.5*1250 = 1875ms.
	input := newInputWire(100, tr, "rg", "FromInput")
	ci := newInputWire(100, tr, "rg", "FromChainInhibitor")
	ctx, cancel := context.WithCancel(context.Background())

	fired := make(chan struct{}, 4)
	toCI := make(chan int, 4)
	node := &Node{
		Fire:               func() { fired <- struct{}{} },
		FromInput:          Wiring.NewInPaced(input, ctx, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewInPaced(ci, ctx, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	send(t, input, 42)
	send(t, ci, 1)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("node did not fire when both inputs arrived within W")
	}

	got := recv(t, toCI)
	cancel()
	wg.Wait()

	if got != 42 {
		t.Fatalf("expected forwarded value 42, got %d", got)
	}

	// Both wires consumed (slot empty): a fresh poll sees nothing.
	if _, ok := input.PollRecv(); ok {
		t.Fatal("input wire not consumed after fire")
	}
	if _, ok := ci.PollRecv(); ok {
		t.Fatal("chain-inhibitor wire not consumed after fire")
	}
}

// TestWindowClear: only one required input arrives → after W the held input is
// Done'd (drained), no fire, flags reset; a subsequent fresh pair fires.
func TestWindowClear(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	// arcLength 8 → SimLatencyMs = 8/0.08 = 100ms → W = 150ms (fast clear).
	input := newInputWire(8, tr, "rg", "FromInput")
	ci := newInputWire(8, tr, "rg", "FromChainInhibitor")
	ctx, cancel := context.WithCancel(context.Background())

	fired := make(chan struct{}, 4)
	toCI := make(chan int, 4)
	node := &Node{
		Fire:               func() { fired <- struct{}{} },
		FromInput:          Wiring.NewInPaced(input, ctx, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewInPaced(ci, ctx, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// Only the value input arrives; the chain-inhibitor never does.
	send(t, input, 7)
	consumed := make(chan struct{}, 1)
	go func() { input.WaitConsumed(ctx); consumed <- struct{}{} }()

	select {
	case <-fired:
		t.Fatal("node fired with only one input present")
	case <-consumed:
		// held input was Done'd by the window clear
	case <-time.After(1 * time.Second):
		t.Fatal("window did not clear the held input within W")
	}

	// No fire and no emission happened.
	select {
	case <-fired:
		t.Fatal("node fired after clear")
	default:
	}
	select {
	case v := <-toCI:
		t.Fatalf("unexpected emission %d after clear", v)
	default:
	}

	// A subsequent fresh pair fires normally (flags reset).
	send(t, input, 9)
	send(t, ci, 1)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("node did not fire on a fresh pair after clear")
	}
}
