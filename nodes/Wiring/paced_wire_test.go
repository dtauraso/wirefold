package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestSendRecvInstantDelivery: happy-path send→deliver→recv→done.
// Send returns immediately once the bead is placed; Recv returns after
// NotifyDelivered delivers into slot; Done clears the slot.
func TestSendRecvInstantDelivery(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, 42) }()

	// Send should return immediately once bead is placed (no Done needed).
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	// Recv blocks until NotifyDelivered fires.
	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before NotifyDelivered")
	default:
	}

	pw.NotifyDelivered(ctx)
	select {
	case v := <-recvDone:
		if v != 42 {
			t.Fatalf("got %v, want 42", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}

	pw.Done() // clears slot
}

// TestSendGatedOnInFlight: Send returns once the bead is placed (inFlight=true).
// A SECOND Send blocks until NotifyDelivered clears inFlight (delivers into
// an empty slot), NOT until Done is called.
func TestSendGatedOnInFlight(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	// First Send places the bead and returns.
	if err := pw.Send(ctx, 1); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if !pw.InFlight() {
		t.Fatal("expected inFlight=true after first Send")
	}

	// Second Send must block while inFlight is true.
	sent2 := make(chan error, 1)
	go func() { sent2 <- pw.Send(ctx, 2) }()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sent2:
		t.Fatal("second Send returned before inFlight cleared")
	default:
	}

	// NotifyDelivered delivers bead 1 into the (empty) slot → inFlight cleared.
	pw.NotifyDelivered(ctx)

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
	// (In production, Recv would read the slot first, but here we just clean up.)
	pw.Done() // clears hasSend for bead 1

	// NotifyDelivered for bead 2 (slot is now empty, delivery can proceed).
	pw.NotifyDelivered(ctx)
	// Recv bead 2.
	v2, err2 := pw.Recv(ctx)
	if err2 != nil || v2 != 2 {
		t.Fatalf("Recv bead 2: v=%v err=%v", v2, err2)
	}
	pw.Done()
}

// TestBackPressureSecondSenderWaits: end-to-end backpressure with deferred delivery.
// When the destination slot is full (hasSend=true), NotifyDelivered for bead 2
// returns IMMEDIATELY (non-blocking), sets pendingDelivered=true, and keeps
// inFlight=true — so a third Send stays blocked. Done completes the deferred
// delivery, clearing inFlight and unblocking the third Send.
func TestBackPressureSecondSenderWaits(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	// Bead 1: place and deliver into slot.
	if err := pw.Send(ctx, 1); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	pw.NotifyDelivered(ctx) // slot now full (hasSend=true), inFlight cleared

	// Bead 2: Send should not block — wire is clear (inFlight=false).
	sent2 := make(chan error, 1)
	go func() { sent2 <- pw.Send(ctx, 2) }()
	select {
	case err := <-sent2:
		if err != nil {
			t.Fatalf("second Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second Send should not block — wire is clear")
	}

	// NotifyDelivered for bead 2 returns immediately (slot full → deferred).
	notifyDone := make(chan struct{})
	go func() {
		pw.NotifyDelivered(ctx)
		close(notifyDone)
	}()
	select {
	case <-notifyDone:
		// good: returned immediately
	case <-time.After(100 * time.Millisecond):
		t.Fatal("NotifyDelivered blocked instead of returning immediately")
	}
	// inFlight must still be true (deferred delivery pending).
	if !pw.InFlight() {
		t.Fatal("expected inFlight=true while deferred delivery is pending")
	}

	// A third Send must block because inFlight is still true.
	sent3 := make(chan error, 1)
	go func() { sent3 <- pw.Send(ctx, 3) }()
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
	pw.NotifyDelivered(ctx)
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
		sendErr = pw.Send(ctx, "new")
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	wg.Wait()
	if sendErr != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", sendErr)
	}
}

// TestVisualPacingDeliveryGate: Recv is gated on NotifyDelivered, not on Send.
// Send returns immediately; Recv returns only after NotifyDelivered fires.
func TestVisualPacingDeliveryGate(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "ping") }()

	// Send should return immediately (bead placed, inFlight=true).
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after placing bead")
	}

	// Recv blocks until NotifyDelivered.
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
		t.Fatal("Recv returned before NotifyDelivered")
	default:
	}

	// NotifyDelivered unblocks Recv.
	pw.NotifyDelivered(ctx)

	select {
	case r := <-recvResult:
		if r.err != nil || r.v != "ping" {
			t.Fatalf("Recv: v=%v err=%v", r.v, r.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}

	pw.Done()
}

// TestRecvBlocksUntilDelivered: Recv must not return before NotifyDelivered.
func TestRecvBlocksUntilDelivered(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	go pw.Send(ctx, "hello")
	time.Sleep(5 * time.Millisecond) // let Send return

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()

	// Recv must not return before NotifyDelivered.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before NotifyDelivered was called")
	default:
	}

	pw.NotifyDelivered(ctx)
	select {
	case v := <-recvDone:
		if v != "hello" {
			t.Fatalf("got %v, want hello", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}
	pw.Done() // clean up
}

// TestSendBlocksUntilDone was: "Send blocks until Done." New contract: Send
// returns when the bead is placed (not on Done). This test verifies the new
// contract: Send returns before Done, and a second Send blocks on inFlight
// until NotifyDelivered, not on Done.
func TestSendBlocksUntilDone(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "value") }()

	// Send returns immediately once bead is placed — before any Done.
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after bead placement")
	}

	// Deliver, receive, and clean up.
	pw.NotifyDelivered(ctx)
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
	go func() { sendErr <- pw.Send(context.Background(), 99) }()

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
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	pw.SetFaded(true)
	pw.SetFaded(false)

	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "resumed") }()

	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not return after bead placement")
	}

	pw.NotifyDelivered(ctx)
	v, err := pw.Recv(ctx)
	if err != nil || v != "resumed" {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}

	pw.Done()
}

// TestRecvUnblocksOnDelivery: Recv returns at NotifyDelivered, not at Done.
func TestRecvUnblocksOnDelivery(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	go pw.Send(ctx, "data")
	time.Sleep(5 * time.Millisecond)

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()

	// Recv must not return before NotifyDelivered.
	time.Sleep(10 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before NotifyDelivered")
	default:
	}

	// NotifyDelivered unblocks Recv immediately.
	pw.NotifyDelivered(ctx)
	select {
	case v := <-recvDone:
		if v != "data" {
			t.Fatalf("got %v, want data", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}

	// Done not yet called — Recv already returned, slot still held.
	pw.Done()
}

// TestTryPlaceDropsWhenInFlight: with inFlight already set, TryPlace returns
// false and does not touch pending/inFlight; with the wire clear, it returns
// true and sets inFlight.
func TestTryPlaceDropsWhenInFlight(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)

	// Clear wire: TryPlace succeeds and sets inFlight.
	if !pw.TryPlace(1) {
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
	if pw.TryPlace(2) {
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
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	o := NewOutPaced(pw, context.Background(), "n", "p", T.New(16), RuleFireAndForget)

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
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	pw.Delete()

	if err := pw.Send(ctx, 1); err != nil {
		t.Fatalf("Send after Delete: got err %v, want nil", err)
	}
	if pw.InFlight() {
		t.Fatalf("Send after Delete placed a bead: inFlight=true, want false")
	}
	if pw.TryPlace(2) {
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

// TestDeleteDropsPulse: a NotifyDelivered that arrives after Delete must be a
// no-op — hasSend must stay false and no node receives the pulse.
func TestDeleteDropsPulse(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	// Place a bead (inFlight=true).
	if err := pw.Send(ctx, 42); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Delete drops the pulse before NotifyDelivered fires.
	pw.Delete()

	// A late NotifyDelivered for the deleted bead must be a no-op.
	pw.NotifyDelivered(ctx)

	// hasSend must still be false — no value was delivered.
	pw.mu.Lock()
	hasSend := pw.hasSend
	pw.mu.Unlock()
	if hasSend {
		t.Fatal("NotifyDelivered on deleted wire set hasSend=true; pulse should be discarded")
	}
}

func TestRestoreUnsilencesWire(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx := context.Background()

	pw.Delete()
	pw.Restore()

	if err := pw.Send(ctx, 1); err != nil {
		t.Fatalf("Send after Restore: got err %v, want nil", err)
	}
	if !pw.InFlight() {
		t.Fatalf("Send after Restore did not place a bead: inFlight=false, want true")
	}

	// Drain and verify TryPlace works on a fresh restored wire too.
	pw.Done()
	pw2 := NewPacedWire(100, PulseSpeedWuPerMs)
	pw2.Delete()
	pw2.Restore()
	if !pw2.TryPlace(2) {
		t.Fatalf("TryPlace after Restore: got false, want true")
	}
	if !pw2.InFlight() {
		t.Fatalf("TryPlace after Restore did not place a bead: inFlight=false, want true")
	}
}
