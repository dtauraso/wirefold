package inhibitrightgate

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

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
	case <-time.After(100 * time.Millisecond):
		cancel()
		wg.Wait()
		return -1, nil
	}
}

// left=1, right=0 → passes (1).
func TestPassWhenLeftOnlyActive(t *testing.T) {
	got, _ := run(1, 0)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// left=1, right=1 → inhibited (0).
func TestInhibitedWhenRightActive(t *testing.T) {
	got, _ := run(1, 1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// left=0, right=0 → 0.
func TestZeroWhenLeftInactive(t *testing.T) {
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
