package Wiring

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// testInFlightMs is the per-bead in-flight time used across these wire tests.
// Delivery is timed on the (fake) clock: place a bead, Advance the clock by this
// amount, and the wire delivers it into the delivered FIFO.
const testInFlightMs = 50

// newFakeWire builds a PacedWire backed by a FakeClock the test advances.
func newFakeWire() (*PacedWire, *FakeClock) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	return pw, clk
}

// waitDelivered advances clk past one bead's in-flight time and waits until at
// least `want` values sit in the delivered FIFO (a bead landed). timeout guards
// against a missed wake.
func waitDelivered(t *testing.T, pw *PacedWire, clk *FakeClock, want int) {
	t.Helper()
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("clock delivery did not produce %d delivered values (have %d)", want, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestMultiBeadFIFO: a wire carries several beads at once. Send 3 distinct
// values, advance the clock past all deadlines, and Recv must return them in
// SEND ORDER with none dropped.
func TestMultiBeadFIFO(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	for _, v := range []int{10, 20, 30} {
		if err := pw.Send(ctx, v, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
			t.Fatalf("Send %d: %v", v, err)
		}
	}
	if !pw.InFlight() {
		t.Fatal("expected beads in flight after 3 sends")
	}

	// Advance past every bead's deadline; all three deliver.
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 3 delivered, have %d", n)
		}
		time.Sleep(time.Millisecond)
	}

	// Recv is now refractory-gated (one accepted bead per recvGateMs train window):
	// beads read back-to-back within the window collapse to one fire. To verify the
	// transport FIFO still hands values back in SEND ORDER with none dropped in
	// transport, advance the clock past recvGateMs before each Recv so every bead
	// falls in its own window and is accepted.
	for _, want := range []int{10, 20, 30} {
		clk.Advance(recvGateMs * time.Millisecond)
		v, err := pw.Recv(ctx)
		if err != nil || v != want {
			t.Fatalf("Recv: v=%v err=%v want %d", v, err, want)
		}
	}
}

// TestSendNeverBlocks: Send 5 with no Recv. All 5 are placed (inflight grows,
// none dropped) and each Send returns immediately.
func TestSendNeverBlocks(t *testing.T) {
	pw, _ := newFakeWire()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		done := make(chan error, 1)
		go func() { done <- pw.Send(ctx, i, beadPlacement{InFlightMs: testInFlightMs}) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Send %d: %v", i, err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Send %d blocked — wire must never park", i)
		}
	}

	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 5 {
		t.Fatalf("expected 5 in-flight beads (none dropped), got %d", n)
	}
}

// TestSendRecvClockDelivery: happy-path send→clock-deliver→recv.
func TestSendRecvClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, 42, beadPlacement{InFlightMs: testInFlightMs}) }()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvDone:
		if v != 42 {
			t.Fatalf("got %v, want 42", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advanced past in-flight time")
	}
}

// TestPollRecvEmpty: PollRecv returns (nil,false) immediately when empty.
func TestPollRecvEmpty(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	done := make(chan struct{})
	go func() {
		v, ok := pw.PollRecv()
		if ok || v != nil {
			t.Errorf("PollRecv on empty: got (%v,%v), want (nil,false)", v, ok)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PollRecv blocked on empty slot")
	}
}

// TestPollRecvConsumes: after clock delivery, PollRecv returns the value and
// CONSUMES it (a repeat poll sees the next value, or empty). Recv/PollRecv now
// consume on read — there is no Done step.
func TestPollRecvConsumes(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 7, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitDelivered(t, pw, clk, 1)

	v, ok := pw.PollRecv()
	if !ok || v != 7 {
		t.Fatalf("PollRecv: got (%v,%v), want (7,true)", v, ok)
	}
	// Consumed: a repeat poll is empty.
	if v2, ok2 := pw.PollRecv(); ok2 || v2 != nil {
		t.Fatalf("PollRecv after consume: got (%v,%v), want (nil,false)", v2, ok2)
	}
}

// TestMultipleSendsNoDrop: with one bead already in flight, a second Send is NOT
// dropped — both deliver and both are received in order (replaces the old
// single-bead "second send drops/blocks" coverage).
func TestMultipleSendsNoDrop(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := pw.Send(ctx, 2, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight (no drop), got %d", n)
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	v1, err1 := pw.Recv(ctx)
	// Recv is refractory-gated: the second bead, read within recvGateMs of the
	// first, would collapse into the same fire. Advance past the window so both
	// distinct values are accepted, proving transport dropped neither.
	clk.Advance(recvGateMs * time.Millisecond)
	v2, err2 := pw.Recv(ctx)
	if err1 != nil || err2 != nil || v1 != 1 || v2 != 2 {
		t.Fatalf("Recv order: v1=%v v2=%v err1=%v err2=%v", v1, v2, err1, err2)
	}
}

// TestRecvBlocksWhenEmpty: Recv with a short-timeout context times out when
// nothing is sent.
func TestRecvBlocksWhenEmpty(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := pw.Recv(ctx)
	if err != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

// TestRecvCancelUnblocks: a Recv blocked on an empty wire returns ErrCanceled
// when its context is canceled.
func TestRecvCancelUnblocks(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx, cancel := context.WithCancel(context.Background())

	result := make(chan error, 1)
	go func() {
		_, err := pw.Recv(ctx)
		result <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err != ErrCanceled {
			t.Fatalf("expected ErrCanceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Recv did not unblock on ctx cancel")
	}
}

// TestClockDeliveryGate: Recv is gated on the clock advance, not on Send.
func TestClockDeliveryGate(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, 5, beadPlacement{InFlightMs: testInFlightMs}) }()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	recvResult := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvResult <- v
	}()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvResult:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvResult:
		if v != 5 {
			t.Fatalf("Recv: got %v want 5", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advanced to delivery")
	}
}

// TestFadedSendSkips: a faded wire returns nil from Send immediately without
// placing a bead.
func TestFadedSendSkips(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	pw.SetFaded(true)

	sendErr := make(chan error, 1)
	go func() { sendErr <- pw.Send(context.Background(), 99, beadPlacement{InFlightMs: testInFlightMs}) }()
	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("Send on faded wire: expected nil, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send on faded wire did not return immediately")
	}

	if pw.InFlight() {
		t.Fatal("faded Send placed a bead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := pw.Recv(ctx); err != ErrCanceled {
		t.Fatalf("Recv after faded Send: expected ErrCanceled, got %v", err)
	}
}

// TestUnfadedAfterSetFaded: after SetFaded(false), Send works normally again.
func TestUnfadedAfterSetFaded(t *testing.T) {
	pw, clk := newFakeWire()
	pw.SetFaded(true)
	pw.SetFaded(false)
	ctx := context.Background()

	if err := pw.Send(ctx, 11, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitDelivered(t, pw, clk, 1)
	v, err := pw.Recv(ctx)
	if err != nil || v != 11 {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}
}

// TestPauseFreezesDelivery: while halted, advancing the clock does not move
// active elapsed, so a bead's deadline is never reached and it stays in flight.
func TestPauseFreezesDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 5, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	clk.Halt()
	clk.Advance(10 * testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered while clock was halted; pause must stop the arithmetic")
	}

	clk.Resume()
	waitDelivered(t, pw, clk, 1)
	v, err := pw.Recv(ctx)
	if err != nil || v != 5 {
		t.Fatalf("Recv after resume: v=%v err=%v", v, err)
	}
}

// TestDeliveryAtExactInFlightTime: the bead delivers exactly when active elapsed
// reaches the in-flight time.
func TestDeliveryAtExactInFlightTime(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 9, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	clk.Advance((testInFlightMs - 1) * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("bead delivered before elapsed reached in-flight time (%d ms)", testInFlightMs)
	}

	clk.Advance(1 * time.Millisecond)
	v, err := pw.Recv(ctx)
	if err != nil || v != 9 {
		t.Fatalf("Recv at exact in-flight time: v=%v err=%v", v, err)
	}
}

// TestTryPlaceNeverDrops: TryPlace always places (multi-bead model) — a second
// TryPlace on a busy wire still succeeds and both beads are in flight.
func TestTryPlaceNeverDrops(t *testing.T) {
	pw, _ := newFakeWire()

	if !pw.TryPlace(1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("TryPlace on clear wire: expected true")
	}
	if !pw.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("TryPlace on busy wire: expected true (multi-bead, no drop)")
	}
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight, got %d", n)
	}
}

// TestTryEmitFireAndForget: an Out with RuleFireAndForget places each bead; a
// second emit on a busy wire is NOT dropped (multi-bead model).
func TestTryEmitFireAndForget(t *testing.T) {
	pw, _ := newFakeWire()
	o := NewOutPaced(pw, context.Background(), "n", "p", T.New(16), RuleFireAndForget, 100, 100/PulseSpeedWuPerMs, wireSegment{}, "")

	if !o.TryEmit(7) {
		t.Fatal("first TryEmit: expected true")
	}
	if !o.TryEmit(8) {
		t.Fatal("second TryEmit: expected true (multi-bead, no drop)")
	}
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight, got %d", n)
	}
}

// TestDeleteSilencesWire: after Delete(), Send/TryPlace place no bead.
func TestDeleteSilencesWire(t *testing.T) {
	pw, _ := newFakeWire()
	ctx := context.Background()

	pw.Delete()

	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send after Delete: got err %v, want nil", err)
	}
	if pw.InFlight() {
		t.Fatalf("Send after Delete placed a bead")
	}
	if pw.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("TryPlace after Delete: got true, want false")
	}
	if pw.InFlight() {
		t.Fatalf("TryPlace after Delete placed a bead")
	}
}

// TestDeleteCancelsClockDelivery: a clock-delivery deadline reached AFTER Delete
// must be a no-op — nothing is delivered.
func TestDeleteCancelsClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 42, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	pw.Delete()

	clk.Advance(testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	pw.mu.Lock()
	n := len(pw.delivered)
	pw.mu.Unlock()
	if n != 0 {
		t.Fatal("clock delivery fired on deleted wire; Delete must cancel pending deliveries")
	}
}

// TestDeleteCancelsAllInFlight: Delete with multiple beads in flight drops them
// all and none are delivered after the deadline.
func TestDeleteCancelsAllInFlight(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	for _, v := range []int{1, 2, 3} {
		if err := pw.Send(ctx, v, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
			t.Fatalf("Send %d: %v", v, err)
		}
	}
	pw.Delete()

	clk.Advance(testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	pw.mu.Lock()
	in, del := len(pw.inflight), len(pw.delivered)
	pw.mu.Unlock()
	if in != 0 || del != 0 {
		t.Fatalf("Delete left beads: inflight=%d delivered=%d", in, del)
	}
}

func TestRestoreUnsilencesWire(t *testing.T) {
	pw, _ := newFakeWire()
	ctx := context.Background()

	pw.Delete()
	pw.Restore()

	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send after Restore: got err %v, want nil", err)
	}
	if !pw.InFlight() {
		t.Fatalf("Send after Restore did not place a bead")
	}

	pw2, _ := newFakeWire()
	pw2.Delete()
	pw2.Restore()
	if !pw2.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("TryPlace after Restore: got false, want true")
	}
	if !pw2.InFlight() {
		t.Fatalf("TryPlace after Restore did not place a bead")
	}
}

// TestRecvGateCollapsesTrain: a node fire emits a TRAIN of same-value beads; the
// receiver must collapse that train to exactly ONE fire. Feed ~5 same-value beads
// spaced ~400 ms apart (the real train cadence) on a FakeClock and assert Recv
// accepts exactly ONE within the recvGateMs window, dropping the rest — then, once
// the clock passes the window, a fresh train's first bead is accepted again.
func TestRecvGateCollapsesTrain(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	// Place a 5-bead train, one bead every beadSpacingMs (400 ms), all same value.
	// Advance the clock by each bead's in-flight time after placement so it lands in
	// delivered, then by the remaining spacing to reach the next placement instant.
	const trainVal = 7
	const beads = 5
	waitDelivered := func(want int) {
		dl := time.Now().Add(time.Second)
		for {
			pw.mu.Lock()
			n := len(pw.delivered)
			pw.mu.Unlock()
			if n >= want {
				return
			}
			if time.Now().After(dl) {
				t.Fatalf("only %d/%d beads delivered", n, want)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// First train bead: place, deliver, accept (lastConsumed == never).
	if err := pw.Send(ctx, trainVal, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	clk.Advance(testInFlightMs * time.Millisecond)
	waitDelivered(1)
	if v, err := pw.Recv(ctx); err != nil || v != trainVal {
		t.Fatalf("first train bead: v=%v err=%v want %d", v, err, trainVal)
	}

	// Remaining 4 train beads arrive within recvGateMs (spacing 400 ms ≪ 2000 ms
	// window). Each must be DROPPED by the refractory gate. Drive them through
	// PollRecv (the windowed-node path), which returns false because every bead is
	// inside the window.
	for i := 1; i < beads; i++ {
		// Advance one spacing interval (still inside recvGateMs) and place+deliver.
		clk.Advance(beadSpacingMs * time.Millisecond)
		if err := pw.Send(ctx, trainVal, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		clk.Advance(testInFlightMs * time.Millisecond)
		waitDelivered(1)
		if v, ok := pw.PollRecv(); ok {
			t.Fatalf("train follower #%d accepted (v=%v) — refractory gate did not collapse the train", i, v)
		}
		// Confirm the gate consumed/dropped it (delivered drained, not parked).
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n != 0 {
			t.Fatalf("train follower #%d left %d beads in delivered (should be dropped)", i, n)
		}
	}

	// After the window elapses, a NEW train fires again: place a fresh bead past
	// recvGateMs from the last accept and assert it IS accepted.
	clk.Advance(recvGateMs * time.Millisecond)
	if err := pw.Send(ctx, 9, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send next-train: %v", err)
	}
	clk.Advance(testInFlightMs * time.Millisecond)
	waitDelivered(1)
	if v, err := pw.Recv(ctx); err != nil || v != 9 {
		t.Fatalf("next train first bead: v=%v err=%v want 9 (gate must re-open after window)", v, err)
	}
}
