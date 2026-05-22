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
type PacedWire struct {
	mu        sync.Mutex
	cond      *sync.Cond
	slot      interface{}
	hasSend   bool // slot holds an undelivered value
	delivered bool // visual has signaled delivery-complete
}

// NewPacedWire creates an empty PacedWire.
func NewPacedWire() *PacedWire {
	pw := &PacedWire{}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// Send places value into the slot, then blocks until NotifyDelivered is called.
// Returns ErrCanceled if ctx is done before either gate opens.
func (pw *PacedWire) Send(ctx context.Context, value interface{}) error {
	// Wait for slot to be empty.
	if err := pw.waitCond(ctx, func() bool { return !pw.hasSend }); err != nil {
		return err
	}

	pw.mu.Lock()
	pw.slot = value
	pw.hasSend = true
	pw.delivered = false
	pw.cond.Broadcast()
	pw.mu.Unlock()

	// Wait for visual to signal delivery-complete.
	return pw.waitCond(ctx, func() bool { return pw.delivered })
}

// Recv blocks until a value is available, then takes it and re-opens the send
// gate. Returns the value and ErrCanceled if ctx is done.
func (pw *PacedWire) Recv(ctx context.Context) (interface{}, error) {
	if err := pw.waitCond(ctx, func() bool { return pw.hasSend }); err != nil {
		return nil, err
	}

	pw.mu.Lock()
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
	pw.delivered = true
	pw.cond.Broadcast()
	pw.mu.Unlock()
}

// waitCond waits until ready() returns true (checked under mu), or ctx is done.
func (pw *PacedWire) waitCond(ctx context.Context, ready func() bool) error {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			pw.cond.Broadcast()
		case <-done:
		}
	}()
	pw.mu.Lock()
	for !ready() {
		if ctx.Err() != nil {
			pw.mu.Unlock()
			close(done)
			return ErrCanceled
		}
		pw.cond.Wait()
	}
	pw.mu.Unlock()
	close(done)
	return nil
}
