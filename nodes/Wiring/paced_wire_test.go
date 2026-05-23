package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// instantStub auto-signals delivery and done whenever a value is placed,
// simulating a buffered-1 chan without a visual layer.
func instantStub(pw *PacedWire) {
	go func() {
		for {
			time.Sleep(time.Millisecond)
			pw.mu.Lock()
			hasSend := pw.hasSend
			pw.mu.Unlock()
			if hasSend {
				pw.NotifyDelivered()
				time.Sleep(time.Millisecond)
				pw.Done()
			}
		}
	}()
}

func TestSendRecvInstantDelivery(t *testing.T) {
	pw := NewPacedWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, 42) }()

	// Deliver and then recv.
	time.Sleep(5 * time.Millisecond)
	pw.NotifyDelivered()
	v, err := pw.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if v != 42 {
		t.Fatalf("got %v, want 42", v)
	}

	// Done unblocks Send.
	pw.Done()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not unblock after Done")
	}
}

func TestBackPressureSecondSenderWaits(t *testing.T) {
	pw := NewPacedWire()
	ctx := context.Background()

	// First send without delivery — slot stays full.
	sent1 := make(chan error, 1)
	go func() { sent1 <- pw.Send(ctx, 1) }()
	time.Sleep(5 * time.Millisecond) // let first send place value

	// Second send must block while slot is full.
	sent2 := make(chan error, 1)
	go func() { sent2 <- pw.Send(ctx, 2) }()

	time.Sleep(5 * time.Millisecond)
	select {
	case <-sent2:
		t.Fatal("second Send returned before slot was free")
	default:
	}

	// Recv the first value (requires NotifyDelivered first).
	recvDone := make(chan struct{})
	go func() {
		pw.Recv(ctx)
		close(recvDone)
	}()
	pw.NotifyDelivered()
	<-recvDone

	// Send1 still blocked — slot not cleared until Done.
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sent1:
		t.Fatal("first Send returned before Done was called")
	default:
	}

	// Done clears the slot and unblocks Send1.
	pw.Done()
	select {
	case err := <-sent1:
		if err != nil {
			t.Fatalf("first Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first Send did not unblock after Done")
	}

	// Give the second sender time to place its value (claim the now-free slot).
	time.Sleep(10 * time.Millisecond)
	pw.NotifyDelivered()
	pw.Done() // unblock second send
	select {
	case err := <-sent2:
		if err != nil {
			t.Fatalf("second Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second Send did not unblock after slot freed")
	}
}

func TestRecvBlocksWhenEmpty(t *testing.T) {
	pw := NewPacedWire()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := pw.Recv(ctx)
	if err != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

func TestContextCancelUnblocksSend(t *testing.T) {
	pw := NewPacedWire()

	// Fill the slot manually.
	pw.mu.Lock()
	pw.slot = "blocker"
	pw.hasSend = true
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

func TestVisualPacingDeliveryGate(t *testing.T) {
	pw := NewPacedWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "ping") }()
	time.Sleep(5 * time.Millisecond) // let Send place value

	// Recv runs concurrently; it blocks until NotifyDelivered.
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

	// Neither Send nor Recv should have returned yet.
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sendDone:
		t.Fatal("Send returned before Done was called")
	default:
	}
	select {
	case <-recvResult:
		t.Fatal("Recv returned before NotifyDelivered")
	default:
	}

	// NotifyDelivered unblocks Recv but not Send.
	pw.NotifyDelivered()

	select {
	case r := <-recvResult:
		if r.err != nil || r.v != "ping" {
			t.Fatalf("Recv: v=%v err=%v", r.v, r.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}

	// Send still blocked.
	time.Sleep(5 * time.Millisecond)
	select {
	case <-sendDone:
		t.Fatal("Send returned before Done was called")
	default:
	}

	// Done unblocks Send.
	pw.Done()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not unblock after Done")
	}
}

func TestRecvBlocksUntilDelivered(t *testing.T) {
	pw := NewPacedWire()
	ctx := context.Background()

	go pw.Send(ctx, "hello")
	time.Sleep(5 * time.Millisecond) // let Send place value

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

	pw.NotifyDelivered()
	select {
	case v := <-recvDone:
		if v != "hello" {
			t.Fatalf("got %v, want hello", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}
	pw.Done() // clean up so Send goroutine can exit
}

// TestSendBlocksUntilDone: Send fills slot, Recv returns value,
// NotifyDelivered fires, but Send still blocks until Done is called.
func TestSendBlocksUntilDone(t *testing.T) {
	pw := NewPacedWire()
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() { sendDone <- pw.Send(ctx, "value") }()
	time.Sleep(5 * time.Millisecond)

	// NotifyDelivered and Recv complete.
	pw.NotifyDelivered()
	v, err := pw.Recv(ctx)
	if err != nil || v != "value" {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}

	// Send must still be blocked.
	time.Sleep(20 * time.Millisecond)
	select {
	case <-sendDone:
		t.Fatal("Send returned before Done was called")
	default:
	}

	// Done unblocks Send.
	pw.Done()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not unblock after Done")
	}
}

// TestRecvUnblocksOnDelivery: Recv returns at NotifyDelivered, not at Done.
func TestRecvUnblocksOnDelivery(t *testing.T) {
	pw := NewPacedWire()
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
	pw.NotifyDelivered()
	select {
	case v := <-recvDone:
		if v != "data" {
			t.Fatalf("got %v, want data", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after NotifyDelivered")
	}

	// Done not yet called — Recv already returned, sender blocked separately.
	pw.Done()
}
