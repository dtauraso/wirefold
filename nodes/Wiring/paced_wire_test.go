package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// testInFlightMs is the per-bead in-flight time used across these wire tests.
// Delivery is timed on the (fake) clock: place a bead, Advance the clock by this
// amount, and the wire delivers into the slot.
const testInFlightMs = 50

// newFakeWire builds a PacedWire backed by a FakeClock the test advances. Returns
// the wire and its clock. This replaces the old NotifyDelivered-driven setup:
// delivery is now triggered by advancing the clock past a bead's in-flight time.
func newFakeWire() (*PacedWire, *FakeClock) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	return pw, clk
}

// deliverNext advances clk past one bead's in-flight time and waits until the
// wire's clock-delivery goroutine has cleared inFlight (the bead landed in the
// slot, or the deferred-delivery path took over). Synchronous like the old
// NotifyDelivered helper. timeout guards against a missed wake.
func deliverNext(t *testing.T, pw *PacedWire, clk *FakeClock) {
	t.Helper()
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("clock delivery did not clear inFlight after Advance")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSendRecvClockDelivery: happy-path send→clock-deliver→recv→done.
// Send returns immediately once the bead is placed; Recv returns after the clock
// advances past the in-flight time and the wire delivers into the slot; Done
// clears the slot.
func TestSendRecvClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, 42, beadPlacement{InFlightMs: testInFlightMs}) }()

	// Send should return immediately once bead is placed (no delivery needed).
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	// Recv blocks until the clock advances past the in-flight time.
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

	pw.Done() // clears slot
}

// TestPollRecvEmpty: PollRecv returns (nil,false) immediately when no value is
// present, and does not block.
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

// TestPollRecvPresentConsumeContract: after clock delivery, PollRecv returns the
// value with ok=true and leaves the slot full (a repeat poll still sees it) until
// Done. This matches Recv's consume contract: PollRecv reads, Done acknowledges.
func TestPollRecvPresentConsumeContract(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 7, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deliverNext(t, pw, clk)

	v, ok := pw.PollRecv()
	if !ok || v != 7 {
		t.Fatalf("PollRecv: got (%v,%v), want (7,true)", v, ok)
	}
	// Slot stays full until Done (consume contract): a repeat poll still sees it.
	if v2, ok2 := pw.PollRecv(); !ok2 || v2 != 7 {
		t.Fatalf("PollRecv before Done: got (%v,%v), want (7,true)", v2, ok2)
	}

	// WaitConsumed must return once Done is called (consume-gated source releases).
	consumed := make(chan error, 1)
	go func() { consumed <- pw.WaitConsumed(ctx) }()
	pw.Done()
	select {
	case err := <-consumed:
		if err != nil {
			t.Fatalf("WaitConsumed: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitConsumed did not return after Done")
	}

	// After Done the slot is empty again.
	if v3, ok3 := pw.PollRecv(); ok3 || v3 != nil {
		t.Fatalf("PollRecv after Done: got (%v,%v), want (nil,false)", v3, ok3)
	}
}

// TestSendGatedOnInFlight: Send returns once the bead is placed (inFlight=true).
// A SECOND Send blocks until the clock advance delivers bead 1 into the empty
// slot (clearing inFlight), NOT until Done is called.
func TestSendGatedOnInFlight(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	// First Send places the bead and returns.
	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if !pw.InFlight() {
		t.Fatal("expected inFlight=true after first Send")
	}

	// Second Send must block while inFlight is true.
	sent2 := make(chan error, 1)
	go func() { sent2 <- pw.Send(ctx, 2, beadPlacement{InFlightMs: testInFlightMs}) }()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sent2:
		t.Fatal("second Send returned before inFlight cleared")
	default:
	}

	// Clock advance delivers bead 1 into the (empty) slot → inFlight cleared.
	clk.Advance(testInFlightMs * time.Millisecond)

	// Second Send should now unblock (wire is clear even though slot is filled).
	select {
	case err := <-sent2:
		if err != nil {
			t.Fatalf("second Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second Send did not unblock after inFlight cleared")
	}

	// Bead 1 is in the slot (hasSend=true). Consumer calls Done to clear it.
	pw.Done() // clears hasSend for bead 1

	// Deliver bead 2 (slot is now empty, delivery can proceed).
	deliverNext(t, pw, clk)
	// Recv bead 2.
	v2, err2 := pw.Recv(ctx)
	if err2 != nil || v2 != 2 {
		t.Fatalf("Recv bead 2: v=%v err=%v", v2, err2)
	}
	pw.Done()
}

// TestBackPressureSecondSenderWaits: end-to-end backpressure with deferred
// delivery. When the destination slot is full (hasSend=true), the clock delivery
// for bead 2 sets pendingDelivered=true and keeps inFlight=true — so a third Send
// stays blocked. Done completes the deferred delivery, clearing inFlight and
// unblocking the third Send.
func TestBackPressureSecondSenderWaits(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	// Bead 1: place and deliver into slot.
	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	deliverNext(t, pw, clk) // slot now full (hasSend=true), inFlight cleared

	// Bead 2: Send should not block — wire is clear (inFlight=false).
	sent2 := make(chan error, 1)
	go func() { sent2 <- pw.Send(ctx, 2, beadPlacement{InFlightMs: testInFlightMs}) }()
	select {
	case err := <-sent2:
		if err != nil {
			t.Fatalf("second Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second Send should not block — wire is clear")
	}

	// Clock delivery for bead 2: deadline reached while slot is full → deferred.
	// pendingDelivered is set and inFlight stays true. Advancing the clock past
	// bead 2's in-flight time triggers the deferred path; we observe its effect
	// via inFlight staying true below.
	clk.Advance(testInFlightMs * time.Millisecond)
	// Give the delivery goroutine a moment to run the deferred path.
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("expected inFlight=true while deferred delivery is pending")
	}

	// A third Send must block because inFlight is still true.
	sent3 := make(chan error, 1)
	go func() { sent3 <- pw.Send(ctx, 3, beadPlacement{InFlightMs: testInFlightMs}) }()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sent3:
		t.Fatal("third Send returned before inFlight cleared")
	default:
	}

	// Consume bead 1: Done clears slot and completes deferred delivery of bead 2,
	// clearing inFlight so the third Send can proceed.
	pw.Done()

	// inFlight cleared by Done's deferred-delivery completion; third Send unblocks.
	select {
	case err := <-sent3:
		if err != nil {
			t.Fatalf("third Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("third Send did not unblock after Done completed deferred delivery")
	}

	// Bead 2 is now in the slot; consume it.
	v2, err2 := pw.Recv(ctx)
	if err2 != nil || v2 != 2 {
		t.Fatalf("Recv bead 2: v=%v err=%v", v2, err2)
	}
	pw.Done()

	// Deliver and consume bead 3.
	deliverNext(t, pw, clk)
	v3, err3 := pw.Recv(ctx)
	if err3 != nil || v3 != 3 {
		t.Fatalf("Recv bead 3: v=%v err=%v", v3, err3)
	}
	pw.Done()
}

// TestRecvBlocksWhenEmpty: Recv with a short-timeout context must time out
// when nothing is sent.
func TestRecvBlocksWhenEmpty(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := pw.Recv(ctx)
	if err != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

// TestContextCancelUnblocksSend: a Send blocked on inFlight must return
// ErrCanceled when the context is canceled.
func TestContextCancelUnblocksSend(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)

	// Manually set inFlight so Send blocks.
	pw.mu.Lock()
	pw.inFlight = true
	pw.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var sendErr error
	go func() {
		defer wg.Done()
		sendErr = pw.Send(ctx, "new", beadPlacement{InFlightMs: testInFlightMs})
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	wg.Wait()
	if sendErr != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", sendErr)
	}
}

// TestClockDeliveryGate: Recv is gated on the clock advance, not on Send.
// Send returns immediately; Recv returns only after the clock advances past the
// in-flight time and the wire delivers.
func TestClockDeliveryGate(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "ping", beadPlacement{InFlightMs: testInFlightMs}) }()

	// Send should return immediately (bead placed, inFlight=true).
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	// Recv blocks until the clock advances to delivery.
	recvResult := make(chan struct {
		v   any
		err error
	}, 1)
	go func() {
		v, err := pw.Recv(ctx)
		recvResult <- struct {
			v   any
			err error
		}{v, err}
	}()

	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvResult:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	// Clock advance unblocks Recv.
	clk.Advance(testInFlightMs * time.Millisecond)

	select {
	case r := <-recvResult:
		if r.err != nil || r.v != "ping" {
			t.Fatalf("Recv: v=%v err=%v", r.v, r.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advanced to delivery")
	}

	pw.Done()
}

// TestRecvBlocksUntilDelivered: Recv must not return before the clock advances
// past the in-flight time.
func TestRecvBlocksUntilDelivered(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	go pw.Send(ctx, "hello", beadPlacement{InFlightMs: testInFlightMs})
	time.Sleep(5 * time.Millisecond) // let Send return

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()

	// Recv must not return before the clock advances.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvDone:
		if v != "hello" {
			t.Fatalf("got %v, want hello", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advance")
	}
	pw.Done() // clean up
}

// TestSendReturnsOnPlacement: Send returns when the bead is placed (not on
// delivery, not on Done). After placement, the clock advance delivers and Recv
// returns the value.
func TestSendReturnsOnPlacement(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "value", beadPlacement{InFlightMs: testInFlightMs}) }()

	// Send returns immediately once bead is placed — before any delivery/Done.
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after bead placement")
	}

	// Deliver, receive, and clean up.
	deliverNext(t, pw, clk)
	v, err := pw.Recv(ctx)
	if err != nil || v != "value" {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}
	pw.Done()
}

// TestFadedSendSkips: a faded wire returns nil from Send immediately without
// filling the slot. A concurrent Recv with a short-timeout context must time
// out (proving no slot fill).
func TestFadedSendSkips(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	pw.SetFaded(true)

	// Send must return immediately with nil.
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

	// Slot must not be filled — Recv with a short timeout should time out.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := pw.Recv(ctx)
	if err != ErrCanceled {
		t.Fatalf("Recv after faded Send: expected ErrCanceled, got %v", err)
	}
}

// TestUnfadedAfterSetFaded: after SetFaded(false), Send works normally again.
func TestUnfadedAfterSetFaded(t *testing.T) {
	pw, clk := newFakeWire()
	pw.SetFaded(true)
	pw.SetFaded(false)

	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "resumed", beadPlacement{InFlightMs: testInFlightMs}) }()

	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after bead placement")
	}

	deliverNext(t, pw, clk)
	v, err := pw.Recv(ctx)
	if err != nil || v != "resumed" {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}

	pw.Done()
}

// TestRecvUnblocksOnDelivery: Recv returns when the clock delivers, not at Done.
func TestRecvUnblocksOnDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	go pw.Send(ctx, "data", beadPlacement{InFlightMs: testInFlightMs})
	time.Sleep(5 * time.Millisecond)

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()

	// Recv must not return before the clock advances.
	time.Sleep(10 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before clock advance")
	default:
	}

	// Clock advance unblocks Recv immediately.
	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvDone:
		if v != "data" {
			t.Fatalf("got %v, want data", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advance")
	}

	// Done not yet called — Recv already returned, slot still held.
	pw.Done()
}

// TestPauseFreezesDelivery: while the clock is halted, advancing it does not move
// active elapsed, so a bead's delivery deadline is never reached and Recv stays
// blocked. After Resume + Advance, the bead delivers. (MODEL.md: pause stops the
// arithmetic.)
func TestPauseFreezesDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 5, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	clk.Halt()
	// Advancing while halted is a no-op — delivery must not fire.
	clk.Advance(10 * testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered while clock was halted; pause must stop the arithmetic")
	}

	clk.Resume()
	deliverNext(t, pw, clk)
	v, err := pw.Recv(ctx)
	if err != nil || v != 5 {
		t.Fatalf("Recv after resume: v=%v err=%v", v, err)
	}
	pw.Done()
}

// TestDeliveryAtExactInFlightTime: the bead delivers exactly when active elapsed
// reaches the in-flight time — one tick short leaves it in flight; the final tick
// delivers it.
func TestDeliveryAtExactInFlightTime(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if err := pw.Send(ctx, 9, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// One millisecond short of the deadline: still in flight.
	clk.Advance((testInFlightMs - 1) * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("bead delivered before elapsed reached in-flight time (%d ms)", testInFlightMs)
	}

	// The final millisecond reaches elapsed == in-flight time → delivery.
	clk.Advance(1 * time.Millisecond)
	v, err := pw.Recv(ctx)
	if err != nil || v != 9 {
		t.Fatalf("Recv at exact in-flight time: v=%v err=%v", v, err)
	}
	pw.Done()
}

// TestTryPlaceDropsWhenInFlight: with inFlight already set, TryPlace returns
// false and does not touch pending/inFlight; with the wire clear, it returns
// true and sets inFlight.
func TestTryPlaceDropsWhenInFlight(t *testing.T) {
	pw, _ := newFakeWire()

	// Clear wire: TryPlace succeeds and sets inFlight.
	if !pw.TryPlace(1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("TryPlace on clear wire: expected true")
	}
	if !pw.InFlight() {
		t.Fatal("TryPlace should set inFlight")
	}
	pw.mu.Lock()
	if pw.pending != 1 {
		t.Fatalf("pending: got %v want 1", pw.pending)
	}
	pw.mu.Unlock()

	// Busy wire: TryPlace drops and leaves pending/inFlight unchanged.
	if pw.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("TryPlace on busy wire: expected false (dropped)")
	}
	pw.mu.Lock()
	if pw.pending != 1 {
		t.Fatalf("dropped TryPlace overwrote pending: got %v want 1", pw.pending)
	}
	if !pw.inFlight {
		t.Fatal("dropped TryPlace cleared inFlight")
	}
	pw.mu.Unlock()
}

// TestTryEmitFireAndForget: an Out with RuleFireAndForget uses TryPlace
// semantics — first emit places the bead (true), a second emit while the wire
// is busy is dropped (false) without overwriting.
func TestTryEmitFireAndForget(t *testing.T) {
	pw, _ := newFakeWire()
	o := NewOutPaced(pw, context.Background(), "n", "p", T.New(16), RuleFireAndForget, 100, 100/PulseSpeedWuPerMs, wireSegment{}, "")

	if !o.TryEmit(7) {
		t.Fatal("first TryEmit: expected true")
	}
	if o.TryEmit(8) {
		t.Fatal("second TryEmit on busy wire: expected false (dropped)")
	}
	pw.mu.Lock()
	if pw.pending != 7 {
		t.Fatalf("dropped TryEmit overwrote pending: got %v want 7", pw.pending)
	}
	pw.mu.Unlock()
}

// TestDeleteSilencesWire: after Delete(), Send places no bead (inFlight stays
// false), TryPlace drops, and WaitConsumed returns immediately. Verifies the
// edge-deletion gate persistently silences the source.
func TestDeleteSilencesWire(t *testing.T) {
	pw, _ := newFakeWire()
	ctx := context.Background()

	pw.Delete()

	if err := pw.Send(ctx, 1, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send after Delete: got err %v, want nil", err)
	}
	if pw.InFlight() {
		t.Fatalf("Send after Delete placed a bead: inFlight=true, want false")
	}
	if pw.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("TryPlace after Delete: got true, want false (dropped)")
	}
	if pw.InFlight() {
		t.Fatalf("TryPlace after Delete placed a bead: inFlight=true, want false")
	}

	done := make(chan error, 1)
	go func() { done <- pw.WaitConsumed(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitConsumed after Delete: got err %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("WaitConsumed after Delete did not return immediately")
	}
}

// TestDeleteCancelsClockDelivery: a clock-delivery deadline reached AFTER Delete
// must be a no-op — hasSend must stay false and no node receives the pulse. This
// exercises the Reset/Delete cancellation of a pending clock-delivery.
func TestDeleteCancelsClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	// Place a bead (inFlight=true) with a pending clock delivery.
	if err := pw.Send(ctx, 42, beadPlacement{InFlightMs: testInFlightMs}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Delete cancels the pending clock-delivery before its deadline.
	pw.Delete()

	// Advancing the clock past the original deadline must NOT deliver.
	clk.Advance(testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// hasSend must still be false — no value was delivered.
	pw.mu.Lock()
	hasSend := pw.hasSend
	pw.mu.Unlock()
	if hasSend {
		t.Fatal("clock delivery fired on deleted wire; Delete must cancel a pending delivery")
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
		t.Fatalf("Send after Restore did not place a bead: inFlight=false, want true")
	}

	// Drain and verify TryPlace works on a fresh restored wire too.
	pw.Done()
	pw2, _ := newFakeWire()
	pw2.Delete()
	pw2.Restore()
	if !pw2.TryPlace(2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("TryPlace after Restore: got false, want true")
	}
	if !pw2.InFlight() {
		t.Fatalf("TryPlace after Restore did not place a bead: inFlight=false, want true")
	}
}
