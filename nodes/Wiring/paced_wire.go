package Wiring

import (
	"context"
	"errors"
	"sync"
)

// ErrCanceled is returned by Send or Recv when the context is canceled.
var ErrCanceled = errors.New("paced wire: context canceled")

// PacedWire is a single-slot wire whose delivery is paced by an external
// visual signal. The slot stays occupied until the receiver calls Done,
// keeping the sender blocked until the receiver has finished using the value.
//
// Lifecycle per value:
//   Send: blocks until slot empty, fills slot, blocks until Done is called.
//   Recv: blocks until NotifyDelivered fires, returns value (slot stays full).
//   Done: receiver signals it is finished; clears slot, unblocks Send.
//   NotifyDelivered: visual layer signals delivery-complete, unblocks Recv.
type PacedWire struct {
	mu         sync.Mutex
	cond       *sync.Cond
	slot       any
	hasSend    bool        // slot holds a value (not yet Done'd)
	deliveryCh chan struct{} // closed by NotifyDelivered
	doneCh     chan struct{} // closed by Done
}

// NewPacedWire creates an empty PacedWire.
func NewPacedWire() *PacedWire {
	pw := &PacedWire{}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// Send places value into the slot, then blocks until Done is called by the
// receiver. Returns ErrCanceled if ctx is done before Done fires.
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
	// Claim slot; allocate fresh per-send delivery and done channels.
	pw.slot = value
	pw.hasSend = true
	myCh := make(chan struct{})
	pw.deliveryCh = myCh
	myDone := make(chan struct{})
	pw.doneCh = myDone
	pw.cond.Broadcast()
	pw.mu.Unlock()

	// Phase 2: wait for receiver to call Done (slot cleared by Done).
	select {
	case <-myDone:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}

// Recv blocks until NotifyDelivered fires, then returns the value.
// The slot is NOT cleared; the sender stays blocked until Done is called.
// Returns ErrCanceled if ctx is done.
func (pw *PacedWire) Recv(ctx context.Context) (any, error) {
	done := pw.watchCtx(ctx)
	defer close(done)

	// Phase 1: wait for slot to be filled.
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
	// Capture value and delivery channel before releasing the mutex.
	v := pw.slot
	myCh := pw.deliveryCh
	pw.mu.Unlock()

	// Phase 2: wait for visual delivery confirmation.
	if myCh != nil {
		select {
		case <-myCh:
		case <-ctx.Done():
			return nil, ErrCanceled
		}
	}

	return v, nil
}

// Done is called by the receiver when it has finished using the value.
// Clears the slot and unblocks the corresponding Send.
func (pw *PacedWire) Done() {
	pw.mu.Lock()
	ch := pw.doneCh
	pw.doneCh = nil
	pw.slot = nil
	pw.hasSend = false
	pw.cond.Broadcast()
	pw.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// NotifyDelivered is called by the visual layer to signal delivery-complete,
// unblocking Recv.
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
