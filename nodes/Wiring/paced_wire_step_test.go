package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// stepUntilAllDelivered advances the given wire's own fake clock one tick at a
// time, calling pw.StepOnce(ctx) after each advance, until wantCount beads sit
// in pw.delivered (or maxTicks is exhausted). It records the tick at which each
// NEW delivery appears, in delivery order — the sequence a caller compares
// against the blocking path's delivery ticks.
func stepUntilAllDelivered(t *testing.T, ctx context.Context, pw *PacedWire, clk *FakeClock, wantCount int, maxTicks int64) []int64 {
	t.Helper()
	var ticks []int64
	seen := 0
	for tick := int64(1); tick <= maxTicks; tick++ {
		clk.AdvanceTicks(1)
		pw.StepOnce(ctx)

		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		for n > seen {
			ticks = append(ticks, tick)
			seen++
		}
		if seen >= wantCount {
			return ticks
		}
	}
	t.Fatalf("StepOnce: only %d/%d beads delivered within %d ticks", seen, wantCount, maxTicks)
	return ticks
}

// driveUntilAllDelivered runs the BLOCKING DriveBeadToDelivery on one background
// goroutine PER item (matching production usage — PlaceAndDrive spawns one
// goroutine per placed bead; DriveBeadsToDelivery with several items sharing one
// wire is not a production combination and deadlocks on the FIFO-head park, since
// a later item finalizing before the earlier item delivers would park the SAME
// driver goroutine that needs to keep advancing the earlier item). It advances
// the wire's fake clock one tick at a time (with a short settle sleep so the
// driver goroutines can process the wake before the next advance), recording the
// tick at which each NEW delivery appears — the same shape of result as
// stepUntilAllDelivered, for direct comparison.
func driveUntilAllDelivered(t *testing.T, ctx context.Context, pw *PacedWire, clk *FakeClock, items []driveItem, wantCount int, maxTicks int64) []int64 {
	t.Helper()
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, it := range items {
			wg.Add(1)
			go func(it driveItem) {
				defer wg.Done()
				it.pw.DriveBeadToDelivery(ctx, it.gen)
			}(it)
		}
		wg.Wait()
		close(done)
	}()

	var ticks []int64
	seen := 0
	for tick := int64(1); tick <= maxTicks; tick++ {
		clk.AdvanceTicks(1)
		// Settle: let the driver goroutine wake, process this tick's advance
		// (and possibly deliver), before we check delivered length.
		deadline := time.Now().Add(50 * time.Millisecond)
		for {
			pw.mu.Lock()
			n := len(pw.delivered)
			pw.mu.Unlock()
			if n > seen || time.Now().After(deadline) {
				break
			}
			time.Sleep(200 * time.Microsecond)
		}
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		for n > seen {
			ticks = append(ticks, tick)
			seen++
		}
		if seen >= wantCount {
			break
		}
	}
	if seen < wantCount {
		t.Fatalf("blocking drive: only %d/%d beads delivered within %d ticks", seen, wantCount, maxTicks)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocking drive: DriveBeadsToDelivery did not return after all beads delivered")
	}
	return ticks
}

// TestStepOnceMatchesBlockingDelivery_SingleBead: one bead on one wire. StepOnce
// called once per tick must deliver it at the EXACT same tick the blocking
// DriveBeadsToDelivery path delivers an identical bead on an identical wire.
func TestStepOnceMatchesBlockingDelivery_SingleBead(t *testing.T) {
	ctx := context.Background()

	pwB, clkB := newFakeWire()
	pwS, clkS := newFakeWire()

	genB, ok := pwB.placeBeadNoWalker(7, beadPlacement{InFlightMs: 42})
	if !ok {
		t.Fatal("placeBeadNoWalker (blocking wire) failed")
	}
	genS, ok := pwS.placeBeadNoWalker(7, beadPlacement{InFlightMs: 42})
	if !ok {
		t.Fatal("placeBeadNoWalker (stepped wire) failed")
	}
	if genB != genS {
		t.Fatalf("gens diverged: blocking=%d stepped=%d (test setup bug)", genB, genS)
	}

	ticksB := driveUntilAllDelivered(t, ctx, pwB, clkB, []driveItem{{pw: pwB, gen: genB}}, 1, 200)
	ticksS := stepUntilAllDelivered(t, ctx, pwS, clkS, 1, 200)

	if len(ticksB) != 1 || len(ticksS) != 1 || ticksB[0] != ticksS[0] {
		t.Fatalf("delivery tick mismatch: blocking=%v stepped=%v", ticksB, ticksS)
	}
	if ticksB[0] != 42 {
		t.Fatalf("expected delivery at tick 42, blocking path delivered at %d", ticksB[0])
	}

	vB, err := pwB.Recv(ctx)
	if err != nil || vB != 7 {
		t.Fatalf("blocking Recv: v=%v err=%v", vB, err)
	}
	vS, err := pwS.Recv(ctx)
	if err != nil || vS != 7 {
		t.Fatalf("stepped Recv: v=%v err=%v", vS, err)
	}
}

// TestStepOnceMatchesBlockingDelivery_FIFOStagger: two beads placed on the same
// tick, FIFO-ordered, with the SECOND bead's own in-flight time SHORTER than the
// first's — so the second bead reaches its own deadline early but must wait
// behind the first (FIFO head) before it can actually deliver. This exercises
// the exact case that separates a naive "deliver whenever due" implementation
// from the FIFO-respecting one: both StepOnce and the blocking path must defer
// the second bead's delivery to the FIRST bead's delivery tick.
func TestStepOnceMatchesBlockingDelivery_FIFOStagger(t *testing.T) {
	ctx := context.Background()

	pwB, clkB := newFakeWire()
	pwS, clkS := newFakeWire()

	genB0, ok := pwB.placeBeadNoWalker(1, beadPlacement{InFlightMs: 100})
	if !ok {
		t.Fatal("place bead0 (blocking) failed")
	}
	genB1, ok := pwB.placeBeadNoWalker(2, beadPlacement{InFlightMs: 20})
	if !ok {
		t.Fatal("place bead1 (blocking) failed")
	}
	genS0, ok := pwS.placeBeadNoWalker(1, beadPlacement{InFlightMs: 100})
	if !ok {
		t.Fatal("place bead0 (stepped) failed")
	}
	genS1, ok := pwS.placeBeadNoWalker(2, beadPlacement{InFlightMs: 20})
	if !ok {
		t.Fatal("place bead1 (stepped) failed")
	}
	if genB0 != genS0 || genB1 != genS1 {
		t.Fatalf("gens diverged (test setup bug): B=%d,%d S=%d,%d", genB0, genB1, genS0, genS1)
	}

	items := []driveItem{{pw: pwB, gen: genB0}, {pw: pwB, gen: genB1}}
	ticksB := driveUntilAllDelivered(t, ctx, pwB, clkB, items, 2, 200)
	ticksS := stepUntilAllDelivered(t, ctx, pwS, clkS, 2, 200)

	if len(ticksB) != 2 || len(ticksS) != 2 {
		t.Fatalf("expected 2 deliveries each: blocking=%v stepped=%v", ticksB, ticksS)
	}
	if ticksB[0] != ticksS[0] || ticksB[1] != ticksS[1] {
		t.Fatalf("delivery tick mismatch: blocking=%v stepped=%v", ticksB, ticksS)
	}
	// Both beads must deliver at tick 100 — bead1's own 20-tick deadline is
	// masked by having to wait behind bead0 (the FIFO head).
	if ticksB[0] != 100 || ticksB[1] != 100 {
		t.Fatalf("expected both deliveries at tick 100 (FIFO-masked), blocking path gave %v", ticksB)
	}

	// FIFO order preserved on both wires: bead0 (val 1) before bead1 (val 2).
	vB0, _ := pwB.Recv(ctx)
	vB1, _ := pwB.Recv(ctx)
	vS0, _ := pwS.Recv(ctx)
	vS1, _ := pwS.Recv(ctx)
	if vB0 != 1 || vB1 != 2 {
		t.Fatalf("blocking Recv order: got %v, %v", vB0, vB1)
	}
	if vS0 != 1 || vS1 != 2 {
		t.Fatalf("stepped Recv order: got %v, %v", vS0, vS1)
	}
}
