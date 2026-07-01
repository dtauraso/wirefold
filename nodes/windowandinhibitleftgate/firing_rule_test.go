package windowandinhibitleftgate

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
	"github.com/dtauraso/wirefold/nodes/gatetesthelper"
)

func run(left, right int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	fromLeft := make(chan int, 1)
	fromRight := make(chan int, 1)
	toPassed := make(chan int, 1)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() { tr.Fire("ilg") },
		FromLeft:  Wiring.NewIn(fromLeft, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewIn(fromRight, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(toPassed, "ilg", "ToPassed", tr),
	}}
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

// Gate is (NOT left) AND right. left=1, right=1 → ¬1 AND 1 = 0 AND 1 = 0.
func TestAndBothActive(t *testing.T) {
	got, _ := run(1, 1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// left=1, right=0 → ¬1 AND 0 = 0 AND 0 = 0.
func TestAndLeftOnly(t *testing.T) {
	got, _ := run(1, 0)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// left=0, right=1 → ¬0 AND 1 = 1 AND 1 = 1.
func TestAndRightOnly(t *testing.T) {
	got, _ := run(0, 1)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// left=0, right=0 → ¬0 AND 0 = 1 AND 0 = 0.
func TestAndNeitherActive(t *testing.T) {
	got, _ := run(0, 0)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// TestPauseFreezesWindowAndDwell drives the window + dwell off an injected
// active-elapsed clock and asserts:
//   - while the clock does NOT advance (paused), a single held input does NOT
//     time out, even though real wall-time passes well past W;
//   - advancing the clock past W with only one input held DOES clear it
//     (window_clear), proving the timeout is measured in sim time;
//   - the dwell only completes once the clock advances past FireDwellMs.
func TestPauseFreezesWindowAndDwell(t *testing.T) {
	var clears gatetesthelper.ClearSink
	tr := T.NewWithSink(0, &clears)
	defer tr.Close()

	left := gatetesthelper.NewInputWire(8, tr, "ilg", "FromLeft")
	right := gatetesthelper.NewInputWire(8, tr, "ilg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	simClk := Wiring.NewFakeClock()

	fired := make(chan struct{}, 4)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() { fired <- struct{}{} },
		Now:       func() time.Duration { return simClk.Now() },
		FromLeft:  Wiring.NewInPaced(left, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "ilg", "ToPassed", tr),
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// One input held; sim clock frozen → no clear.
	gatetesthelper.Send(t, left, 1)

	time.Sleep(400 * time.Millisecond)
	if clears.Count() != 0 {
		t.Fatal("window cleared while sim clock was paused (timed on wall-clock)")
	}
	select {
	case <-fired:
		t.Fatal("node fired with only one input")
	default:
	}

	// Advance sim time past W (3000ms = 120wu/0.04) with one input held → must clear.
	simClk.Advance(3500 * time.Millisecond)
	deadline := time.Now().Add(1 * time.Second)
	for clears.Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("window did not clear after sim clock advanced past W")
		}
		time.Sleep(2 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	// Dwell test: deliver a full pair, prove fire waits on sim clock past FireDwellMs.
	dctx, dcancel := context.WithCancel(context.Background())
	dClk := Wiring.NewFakeClock()
	dLeft := gatetesthelper.NewInputWire(8, tr, "ilg2", "FromLeft")
	dRight := gatetesthelper.NewInputWire(8, tr, "ilg2", "FromRight")
	dFired := make(chan struct{}, 4)
	dNode := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() { dFired <- struct{}{} },
		Now:       func() time.Duration { return dClk.Now() },
		FromLeft:  Wiring.NewInPaced(dLeft, dctx, "ilg2", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(dRight, dctx, "ilg2", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "ilg2", "ToPassed", tr),
	}}
	var dwg sync.WaitGroup
	dwg.Add(1)
	go func() { defer dwg.Done(); dNode.Update(dctx) }()
	defer func() { dcancel(); dwg.Wait() }()

	// left=0 → stored Left=1; right=1 → stored Right=1 → both held → dwell starts.
	gatetesthelper.Send(t, dLeft, 0)
	gatetesthelper.Send(t, dRight, 1)

	select {
	case <-dFired:
		t.Fatal("node fired before sim clock advanced past FireDwellMs")
	case <-time.After(300 * time.Millisecond):
		// good: dwell not satisfied while sim time held below FireDwellMs
	}

	dClk.Advance((gatecommon.FireDwellMs + 50) * time.Millisecond)
	select {
	case <-dFired:
		// good
	case <-time.After(1 * time.Second):
		t.Fatal("node did not fire after sim clock advanced past FireDwellMs")
	}
}

// TestSkipMinusOnePlaceholder: -1 ("no value") beads on an input are discarded, not
// held. After two -1 placeholders then a real 0 arrive on the left, the slot holds 0
// (the latest). The gate NOTs left: stored Left = 1 - 0 = 1. Right=1 → AND(1,1)=1.
func TestSkipMinusOnePlaceholder(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	left := gatetesthelper.NewInputWire(100, tr, "ilg", "FromLeft")
	right := gatetesthelper.NewInputWire(100, tr, "ilg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan int, 4)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() {},
		FromLeft:  Wiring.NewInPaced(left, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(out, "ilg", "ToPassed", tr),
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	gatetesthelper.Send(t, left, -1) // placeholder — must be discarded
	gatetesthelper.Send(t, left, -1) // placeholder — must be discarded
	gatetesthelper.Send(t, left, 0)  // real value — fills the slot
	gatetesthelper.Send(t, right, 1)

	select {
	case v := <-out:
		if v != 1 {
			t.Fatalf("AND after discarding -1 placeholders: got %d, want 1 (left holds real 0 → ¬0=1, AND(1,1)=1)", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node did not fire after -1 placeholders discarded + real value held")
	}
}

// TestLatestPerSide: a side tracks the MOST-RECENT real bead. Left gets 0 then 1;
// the slot must hold 1 (the latest). The gate NOTs left: stored Left = 1 - 1 = 0.
// Right=1 → AND(0,1)=0.
func TestLatestPerSide(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	left := gatetesthelper.NewInputWire(100, tr, "ilg", "FromLeft")
	right := gatetesthelper.NewInputWire(100, tr, "ilg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan int, 4)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() {},
		FromLeft:  Wiring.NewInPaced(left, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(out, "ilg", "ToPassed", tr),
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	gatetesthelper.Send(t, left, 0) // first real value
	gatetesthelper.Send(t, left, 1) // newer real value — the slot must update to this
	gatetesthelper.Send(t, right, 1)

	select {
	case v := <-out:
		if v != 0 {
			t.Fatalf("latest-per-side: got AND=%d, want 0 (left holds latest 1 → ¬1=0, AND(0,1)=0)", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("node did not fire")
	}
}

// TestWindowFire: both inputs delivered within W → node fires once, both consumed.
func TestWindowFire(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	left := gatetesthelper.NewInputWire(100, tr, "ilg", "FromLeft")
	right := gatetesthelper.NewInputWire(100, tr, "ilg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	fired := make(chan struct{}, 4)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() { fired <- struct{}{} },
		FromLeft:  Wiring.NewInPaced(left, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "ilg", "ToPassed", tr),
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	gatetesthelper.Send(t, left, 0)
	gatetesthelper.Send(t, right, 1)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("node did not fire when both inputs arrived within W")
	}

	cancel()
	wg.Wait()

	if _, ok := left.PollRecv(); ok {
		t.Fatal("left wire not consumed after fire")
	}
	if _, ok := right.PollRecv(); ok {
		t.Fatal("right wire not consumed after fire")
	}
}

// TestWindowClear: one input delivered, second never arrives → after W the held
// input is cleared (window_clear breadcrumb), no fire, flags reset; a subsequent
// fresh pair fires.
func TestWindowClear(t *testing.T) {
	var clears gatetesthelper.ClearSink
	tr := T.NewWithSink(0, &clears)
	defer tr.Close()

	left := gatetesthelper.NewInputWire(8, tr, "ilg", "FromLeft")
	right := gatetesthelper.NewInputWire(8, tr, "ilg", "FromRight")
	ctx, cancel := context.WithCancel(context.Background())

	simClk := Wiring.NewFakeClock()

	fired := make(chan struct{}, 4)
	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() { fired <- struct{}{} },
		Now:       func() time.Duration { return simClk.Now() },
		FromLeft:  Wiring.NewInPaced(left, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(right, ctx, "ilg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(make(chan int, 4), "ilg", "ToPassed", tr),
	}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// Only the left input arrives; right never does.
	gatetesthelper.Send(t, left, 1)
	// Wait until the gate has actually opened the window (t0 captured against the
	// frozen clock) before advancing. This replaces a fixed 50ms sleep that raced
	// the t0 = now() read: if the advance beat t0's capture, t0 would be measured
	// AFTER the advance and the window would never time out.
	gatetesthelper.WaitCount(t, clears.OpenCount, 1, "window_open")

	simClk.Advance(3500 * time.Millisecond)

	// Wait for window_clear breadcrumb (proves the node cleared rather than fired).
	deadline := time.Now().Add(1 * time.Second)
	for clears.Count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("window did not clear the held input within W")
		}
		time.Sleep(2 * time.Millisecond)
	}

	select {
	case <-fired:
		t.Fatal("node fired with only one input present")
	default:
	}

	select {
	case <-fired:
		t.Fatal("node fired after clear")
	default:
	}

	gatetesthelper.Send(t, left, 0)
	gatetesthelper.Send(t, right, 1)
	// Wait until the gate holds both inputs and has captured dwellStart against the
	// clock before advancing past the dwell (replaces a fixed 50ms sleep that raced
	// the dwellStart = now() read).
	gatetesthelper.WaitCount(t, clears.DwellCount, 1, "dwell_start")
	simClk.Advance((gatecommon.FireDwellMs + 50) * time.Millisecond)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("node did not fire on a fresh pair after clear")
	}
}
