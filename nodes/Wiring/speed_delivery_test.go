// speed_delivery_test.go — behavioral coverage for the speed-delivery primitives
// (ApplySpeedNonBlocking / SendSpeedNonBlocking, clock.go) that per-goroutine-clock.md
// "Delivery" builds on: a buffered-1, latest-wins channel per clock-owning goroutine,
// built once at load and touched thereafter by exactly the two ends that own it (the
// sender — stdin_reader's own goroutine — and the one receiver that owns the channel).
package Wiring

import (
	"testing"
	"time"
)

// TestSendSpeedNonBlockingNeverBlocks: sending speed changes onto a channel whose
// receiver never reads must never block the sender — a goroutine that is asleep, or
// simply never built (an unwired node), must not be able to hang stdin_reader's own
// goroutine. Send far more times than the buffer (1) can hold, on a bare timer, and
// require every send to return well under a generous deadline.
func TestSendSpeedNonBlockingNeverBlocks(t *testing.T) {
	ch := make(chan float64, 1)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			SendSpeedNonBlocking(ch, float64(i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendSpeedNonBlocking blocked with no reader draining the channel")
	}
}

// TestSendSpeedNonBlockingLatestWins: several rapid sends with no reader draining in
// between must coalesce to the LATEST value, never a stale earlier one — speed is
// absolute state, not an event stream, so dropping intermediate values is correct,
// not lossy (per-goroutine-clock.md "Delivery").
func TestSendSpeedNonBlockingLatestWins(t *testing.T) {
	ch := make(chan float64, 1)
	SendSpeedNonBlocking(ch, 1)
	SendSpeedNonBlocking(ch, 2)
	SendSpeedNonBlocking(ch, 3)

	select {
	case got := <-ch:
		if got != 3 {
			t.Fatalf("coalesced value = %v, want 3 (the latest send)", got)
		}
	default:
		t.Fatal("expected a pending value after 3 sends, channel was empty")
	}
	// And nothing further is queued: buffer holds exactly the coalesced one value.
	select {
	case extra := <-ch:
		t.Fatalf("unexpected second queued value %v — buffer should hold only the latest", extra)
	default:
	}
}

// TestApplySpeedNonBlockingAppliesOnWake proves delivery is NOT lossy: a speed change
// sent while the "goroutine" is asleep (not polling) must still be applied the next
// time it calls ApplySpeedNonBlocking on waking — the buffered-1 channel holds it
// across the sleep window.
func TestApplySpeedNonBlockingAppliesOnWake(t *testing.T) {
	clk := NewRealClock()
	ch := make(chan float64, 1)

	// Measure the tick advance at speed 1 over one window ("asleep").
	time.Sleep(2 * tickPeriod)
	before := clk.Tick()

	// Send while nobody is polling — simulates a goroutine parked in SleepCycle.
	SendSpeedNonBlocking(ch, 4)

	// The goroutine "wakes" and, per the Delivery model, checks its channel before
	// (or instead of) blocking again.
	ApplySpeedNonBlocking(clk, ch)

	time.Sleep(2 * tickPeriod)
	after := clk.Tick()
	advanced := after - before
	// At the OLD speed (1) two periods would advance ~2 ticks; at the delivered
	// speed (4) it should be closer to ~8. Require clearly more than the old rate
	// would have produced, so this fails if the speed change was dropped.
	if advanced < 5 {
		t.Fatalf("speed change sent during 'sleep' was not applied on wake: advanced=%d ticks (want >=5, i.e. faster than the pre-change speed-1 rate)", advanced)
	}
}

// TestApplySpeedNonBlockingNilChannelIsNoop: a node with no speed channel (test builds
// with no loader, or a future kind that doesn't declare one) must not panic and must
// leave the clock's speed untouched — reading from a nil channel in a select with a
// default case is never selected, which is exactly what makes this safe.
func TestApplySpeedNonBlockingNilChannelIsNoop(t *testing.T) {
	clk := NewRealClock()
	var nilCh <-chan float64 // no goroutine holds a channel to send on
	ApplySpeedNonBlocking(clk, nilCh)
	// No panic is the primary assertion; also confirm the clock still ticks at the
	// default speed (1), i.e. nothing mutated it.
	time.Sleep(2 * tickPeriod)
	if clk.Tick() < 1 {
		t.Fatalf("clock did not advance at default speed after a nil-channel ApplySpeedNonBlocking: tick=%d", clk.Tick())
	}
}

// TestApplySpeedNonBlockingEmptyChannelIsNoop: an empty (but non-nil) channel must
// also be a no-op — no pending value means nothing to apply.
func TestApplySpeedNonBlockingEmptyChannelIsNoop(t *testing.T) {
	clk := NewRealClock()
	ch := make(chan float64, 1)
	time.Sleep(2 * tickPeriod)
	before := clk.Tick()
	ApplySpeedNonBlocking(clk, ch) // nothing pending
	time.Sleep(2 * tickPeriod)
	after := clk.Tick()
	// Advance should reflect the unchanged default speed 1 (~2 ticks), not a jump.
	if after-before > 5 {
		t.Fatalf("clock advanced as if a speed change were applied from an empty channel: before=%d after=%d", before, after)
	}
}
