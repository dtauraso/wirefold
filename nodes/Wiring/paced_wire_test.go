package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// instantStub auto-signals delivery after Recv, simulating a buffered-1 chan.
func instantStub(pw *PacedWire) {
	go func() {
		for {
			time.Sleep(time.Millisecond)
			pw.mu.Lock()
			if pw.hasSend && !pw.delivered {
				pw.delivered = true
				pw.cond.Broadcast()
			}
			pw.mu.Unlock()
		}
	}()
}

func TestSendRecvInstantDelivery(t *testing.T) {
	pw := NewPacedWire()
	instantStub(pw)
	ctx := context.Background()

	if err := pw.Send(ctx, 42); err != nil {
		t.Fatalf("Send: %v", err)
	}
	v, err := pw.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if v != 42 {
		t.Fatalf("got %v, want 42", v)
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

	// Recv first value and notify delivery — frees slot for second send.
	pw.Recv(ctx)
	pw.NotifyDelivered()

	select {
	case err := <-sent1:
		if err != nil {
			t.Fatalf("first Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first Send did not unblock")
	}

	pw.NotifyDelivered() // unblock second send
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

	// Recv takes the value — but Send must still be blocking.
	time.Sleep(5 * time.Millisecond)
	v, err := pw.Recv(ctx)
	if err != nil || v != "ping" {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}

	select {
	case <-sendDone:
		t.Fatal("Send returned before NotifyDelivered")
	default:
	}

	pw.NotifyDelivered()
	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Send did not unblock after NotifyDelivered")
	}
}
