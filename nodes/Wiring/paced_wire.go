package Wiring

import (
	"context"
	"errors"
	"sync"
)

// ErrCanceled is returned by Send or Recv when the context is canceled.
var ErrCanceled = errors.New("paced wire: context canceled")

// PacedWire is a single-slot wire whose delivery is paced by an external
// visual signal. Send blocks until the slot is empty, places the value, then
// blocks until the visual calls NotifyDelivered. Recv unblocks once a value
// is placed, takes it, and re-opens the send gate.
//
// Delivery confirmation uses a per-send channel (deliveryCh) so that
// NotifyDelivered targets exactly the in-flight send, not a shared flag
// that can be reset by a racing second sender.
type PacedWire struct {
	mu         sync.Mutex
	cond       *sync.Cond
	slot       any
	hasSend    bool       // slot holds an undelivered value
	deliveryCh chan struct{} // closed by NotifyDelivered to confirm delivery
}

// NewPacedWire creates an empty PacedWire.
func NewPacedWire() *PacedWire {
	pw := &PacedWire{}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// Send places value into the slot, then blocks until NotifyDelivered is called.
// Returns ErrCanceled if ctx is done before the delivery gate opens.
func (pw *PacedWire) Send(ctx context.Context, value any) error {
	// Phase 1: wait for slot to be empty, then atomically claim it.
	done := pw.watchCtx(ctx)
	defer close(done)

	pw.mu.Lock()
	for pw.hasSend {
		if ctx.Err() != nil {
			pw.mu.Unlock()
			return ErrCanceled
		}
		pw.cond.Wait()
	}
	if ctx.Err() != nil {
		pw.mu.Unlock()
		return ErrCanceled
	}
	// Atomically claim slot and allocate a fresh per-send delivery channel.
	pw.slot = value
	pw.hasSend = true
	myCh := make(chan struct{})
	pw.deliveryCh = myCh
	pw.cond.Broadcast()
	pw.mu.Unlock()

	// Phase 2: wait for this send's delivery confirmation.
	select {
	case <-myCh:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}

// Recv blocks until a value is available, then takes it and re-opens the send
// gate. Returns the value and ErrCanceled if ctx is done.
func (pw *PacedWire) Recv(ctx context.Context) (any, error) {
	done := pw.watchCtx(ctx)
	defer close(done)

	pw.mu.Lock()
	for !pw.hasSend {
		if ctx.Err() != nil {
			pw.mu.Unlock()
			return nil, ErrCanceled
		}
		pw.cond.Wait()
	}
	if ctx.Err() != nil {
		pw.mu.Unlock()
		return nil, ErrCanceled
	}
	v := pw.slot
	pw.slot = nil
	pw.hasSend = false
	pw.cond.Broadcast()
	pw.mu.Unlock()

	return v, nil
}

// NotifyDelivered is called by the visual layer to signal delivery-complete,
// unblocking the corresponding Send.
func (pw *PacedWire) NotifyDelivered() {
	pw.mu.Lock()
	ch := pw.deliveryCh
	pw.deliveryCh = nil
	pw.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// watchCtx starts a goroutine that broadcasts on pw.cond when ctx is done.
// The caller must close the returned channel when done to stop the goroutine.
func (pw *PacedWire) watchCtx(ctx context.Context) chan struct{} {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			pw.cond.Broadcast()
		case <-done:
		}
	}()
	return done
}
