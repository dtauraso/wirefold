package Wiring

import (
	"context"
	"errors"
	"fmt"
	"sync"

	T "github.com/dtauraso/wirefold/Trace"
)

// PulseSpeedWuPerMs aliases CurveParamPulseSpeedWuPerMs for call sites that
// pass it to NewPacedWire.  The canonical value lives in curve_params.go so
// the codegen tool can export it to TS.
const PulseSpeedWuPerMs = CurveParamPulseSpeedWuPerMs

// ErrCanceled is returned by Send or Recv when the context is canceled.
var ErrCanceled = errors.New("paced wire: context canceled")

// PacedWire is a single-slot wire with two distinct backpressure bits.
//
// Two bits, two clear events:
//
//   - inFlight: a bead is traversing THIS wire. Set when the source places the
//     bead (Send). The source gates only on this bit — it never observes the
//     destination slot and does not wait on Done.
//   - hasSend: the destination slot holds a value. Cleared by the consumer's
//     Done call.
//
// Delivery into the destination slot (hasSend=true) happens only when the slot
// is empty (!hasSend); that delivery is what clears inFlight. Backpressure
// propagates wire-locally: a full destination slot keeps the bead on the wire,
// so inFlight stays true, so the source blocks — with no cross-node handshake.
//
// Internally, the wire separates the in-flight staging area (pending/inFlight)
// from the delivered slot (slot/hasSend) so that a new bead can be placed on
// the wire while the prior bead's value sits in the slot waiting for Done.
//
// Deferred delivery: NotifyDelivered NEVER blocks. If the slot is full when
// NotifyDelivered is called, the delivery is deferred: pendingDelivered is set
// to true and the call returns immediately. inFlight stays true so the source
// remains blocked. When Done clears the slot, it checks pendingDelivered and
// completes the deferred delivery right there (under the same lock), filling
// the slot again and closing the fresh slotReadyCh so the next Recv unblocks.
//
// Lifecycle per value:
//
//	Send:            blocks while inFlight is true, places bead into pending,
//	                 sets inFlight=true, returns. After Send, the source may
//	                 call WaitConsumed to wait until Done is called by the
//	                 consumer (consume-gated policy lives in the node, not here).
//	NotifyDelivered: if deleted — no-op (pulse from a deleted wire is discarded).
//	                 if slot empty — calls deliverLocked (moves pending→slot,
//	                 sets hasSend=true, clears inFlight, closes slotReadyCh).
//	                 if slot full — sets pendingDelivered=true, returns immediately.
//	Recv:            blocks until slotReadyCh is closed (slot filled), returns
//	                 value (slot stays full).
//	Done:            consumer finished; clears slot (hasSend=false), resets
//	                 slotReadyCh for the next cycle; if pendingDelivered, calls
//	                 deliverLocked to complete the deferred delivery immediately.
//	                 Also closes consumedCh so any caller of WaitConsumed unblocks.
//
// Deleting a wire destroys both bits; a fresh wire has inFlight=false so the
// source can send immediately.
type PacedWire struct {
	mu               sync.Mutex
	cond             *sync.Cond
	pending          any          // value placed by Send, not yet delivered into slot
	slot             any          // value in destination slot (set by NotifyDelivered)
	hasSend          bool         // destination slot holds a value (not yet Done'd)
	inFlight         bool         // bead is on the wire, not yet delivered into slot
	pendingDelivered bool         // NotifyDelivered arrived while slot was full; delivery deferred to Done
	faded            bool         // when true, Send skips without sending
	deleted          bool         // when true, the edge was deleted; source stops placing beads (distinct origin from faded)
	slotReadyCh      chan struct{} // closed when slot is filled; reset by Done
	consumedCh       chan struct{} // closed by Done to signal the source that the slot was consumed
	ArcLength        float64      // straight-line distance between source and target nodes (world units)
	SimLatencyMs     float64      // ArcLength / pulseSpeed (ms); how long a pulse takes to traverse the wire
	Target           string       // destination node id — authoritative slot identity (set by loader)
	TargetHandle     string       // destination input-port name — authoritative slot identity (set by loader)
	Trace            *T.Trace     // injected by loader; used for breadcrumb diagnostics only
}

// NewPacedWire creates an empty PacedWire with geometry-derived timing.
// arcLength is the straight-line distance between source and target (world units).
// pulseSpeed is in world-units per millisecond (use PulseSpeedWuPerMs).
func NewPacedWire(arcLength float64, pulseSpeed float64) *PacedWire {
	pw := &PacedWire{
		ArcLength:    arcLength,
		SimLatencyMs: arcLength / pulseSpeed,
		slotReadyCh:  make(chan struct{}),
		consumedCh:   make(chan struct{}),
	}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// SetFaded sets the faded flag. When faded is true, Send returns nil immediately
// without placing a bead. In-flight values already past the gate are unaffected.
func (pw *PacedWire) SetFaded(v bool) {
	pw.mu.Lock()
	pw.faded = v
	pw.mu.Unlock()
}

// Send places a bead on the wire and returns immediately once the bead is
// placed. Returns ErrCanceled if ctx is done before the wire clears.
// If the wire is faded, Send returns nil immediately without placing a bead.
//
// Backpressure: Send blocks while inFlight is true (wire occupied by a prior
// bead not yet delivered into its destination slot). It does NOT wait on Done
// and does NOT observe the destination slot directly.
func (pw *PacedWire) Send(ctx context.Context, value any) error {
	// Fade/delete gate: skip benignly when the wire is faded or deleted.
	pw.mu.Lock()
	if pw.faded {
		pw.mu.Unlock()
		return nil
	}
	if pw.deleted {
		// Edge deleted: place no bead, do not block.
		pw.mu.Unlock()
		return nil
	}
	pw.mu.Unlock()

	// Wait for wire to be clear (inFlight == false), then place the bead.
	done := pw.watchCtx(ctx)
	defer close(done)

	pw.mu.Lock()
	for pw.inFlight {
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
	// Place bead into staging area; set inFlight.
	// Allocate a fresh consumedCh so WaitConsumed can block until Done fires.
	pw.pending = value
	pw.inFlight = true
	pw.consumedCh = make(chan struct{})
	pw.cond.Broadcast()
	pw.mu.Unlock()

	return nil
}

// TryPlace is the non-blocking placement used by the fire-and-forget send rule.
// It never blocks and never overwrites an in-flight bead: if the wire is faded
// or already in-flight, it returns false (the send is dropped). Otherwise it
// places the bead exactly like Send's placement block and returns true.
func (pw *PacedWire) TryPlace(value any) bool {
	pw.mu.Lock()
	if pw.faded {
		pw.mu.Unlock()
		return false
	}
	if pw.deleted {
		// Edge deleted: drop, place nothing.
		pw.mu.Unlock()
		return false
	}
	if pw.inFlight {
		// Wire busy: drop the bead. Do NOT block, do NOT overwrite.
		pw.mu.Unlock()
		return false
	}
	pw.pending = value
	pw.inFlight = true
	pw.consumedCh = make(chan struct{})
	pw.cond.Broadcast()
	pw.mu.Unlock()
	return true
}

// Recv blocks until the slot is filled (NotifyDelivered delivers the bead),
// then returns the value. The slot is NOT cleared; the consumer must call Done.
// Returns ErrCanceled if ctx is done.
func (pw *PacedWire) Recv(ctx context.Context) (any, error) {
	// Capture the current slotReadyCh. If the slot is already filled, this
	// channel was already closed and select returns immediately.
	pw.mu.Lock()
	ch := pw.slotReadyCh
	pw.mu.Unlock()

	select {
	case <-ch:
	case <-ctx.Done():
		return nil, ErrCanceled
	}

	pw.mu.Lock()
	v := pw.slot
	pw.mu.Unlock()
	return v, nil
}

// PollRecv is the non-blocking variant of Recv. It returns (value, true) if the
// destination slot is currently filled (hasSend), without blocking; otherwise it
// returns (nil, false) immediately. Like Recv, a successful poll does NOT clear
// the slot — the consumer must call Done to acknowledge consumption (same
// consume/Done/WaitConsumed contract as Recv). This lets a windowed node check
// each input without parking so it can drive its window timer.
func (pw *PacedWire) PollRecv() (any, bool) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if !pw.hasSend {
		return nil, false
	}
	return pw.slot, true
}

// deliverLocked moves the pending value into the slot and signals Recv.
// Must be called with pw.mu held. Precondition: hasSend==false.
// Sets hasSend=true, clears inFlight and pendingDelivered, broadcasts, and
// closes the current slotReadyCh so any waiting Recv unblocks.
func (pw *PacedWire) deliverLocked() {
	pw.slot = pw.pending
	pw.pending = nil
	pw.hasSend = true
	pw.inFlight = false
	pw.pendingDelivered = false
	ch := pw.slotReadyCh
	pw.cond.Broadcast()
	// Close outside the broadcast but still under the lock is fine; Recv only
	// reads the channel after capturing it while holding the lock.
	close(ch)
}

// Done is called by the receiver when it has finished using the value.
// Clears the destination slot (hasSend=false) and resets slotReadyCh for the
// next delivery cycle. If a delivery was deferred (pendingDelivered==true),
// completes it immediately by calling deliverLocked so the next Recv unblocks
// without waiting for another NotifyDelivered.
func (pw *PacedWire) Done() {
	pw.mu.Lock()
	pw.slot = nil
	pw.hasSend = false
	pw.slotReadyCh = make(chan struct{})
	if pw.pendingDelivered {
		// A NotifyDelivered arrived while the slot was full; complete it now.
		pw.deliverLocked()
	} else {
		pw.cond.Broadcast()
	}
	// Signal any WaitConsumed caller that the slot has been consumed.
	ch := pw.consumedCh
	pw.consumedCh = nil
	pw.mu.Unlock()

	if ch != nil {
		close(ch)
	}
}

// WaitConsumed blocks until the consumer calls Done on the value placed by the
// most recent Send, or until ctx is canceled. Returns nil on consumption,
// ErrCanceled on context cancellation. If no bead is outstanding (consumedCh is
// nil), WaitConsumed returns nil immediately.
//
// This lets a source node implement a consume-gated send policy without the wire
// enforcing it: call TrySend to place the bead, then call WaitConsumed to wait
// for the consumer to finish — the policy lives in the node loop, not the wire.
func (pw *PacedWire) WaitConsumed(ctx context.Context) error {
	pw.mu.Lock()
	if pw.deleted {
		// Edge deleted: never park a gated source on a dead wire.
		pw.mu.Unlock()
		return nil
	}
	ch := pw.consumedCh
	pw.mu.Unlock()

	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ErrCanceled
	}
}

// NotifyDelivered is called by the visual layer when the pulse animation
// completes. It NEVER blocks. If the wire is deleted, the pulse is discarded.
// If the slot is empty, it delivers immediately (moves pending→slot, sets
// hasSend=true, clears inFlight, closes slotReadyCh). If the slot is full, it
// sets pendingDelivered=true and returns immediately — inFlight stays true so
// the source remains blocked until Done completes the deferred delivery.
func (pw *PacedWire) NotifyDelivered(ctx context.Context) error {
	pw.mu.Lock()
	if pw.deleted {
		// Wire was deleted: discard the pulse. A late NotifyDelivered for a bead
		// that was in-flight when Delete was called must NOT set hasSend — no node
		// is allowed to receive a pulse from a removed wire.
		pw.Trace.Breadcrumb("notify_on_deleted_wire_ignored", pw.Target, pw.TargetHandle, "")
		pw.mu.Unlock()
		return nil
	}
	if pw.hasSend {
		// Slot full: defer delivery to Done.
		pw.pendingDelivered = true
		pw.mu.Unlock()
		return nil
	}
	// Slot empty: deliver immediately.
	pw.deliverLocked()
	pw.mu.Unlock()
	return nil
}

// resetLocked performs the shared teardown for Reset and Delete: drops any
// in-flight/held value, installs a fresh slotReadyCh, broadcasts, and returns
// the consumedCh (now nil'd) so the caller can close it to free a parked
// WaitConsumed. Must be called with pw.mu held; the caller closes the returned
// channel after unlocking.
func (pw *PacedWire) resetLocked() chan struct{} {
	pw.inFlight = false
	pw.pending = nil
	pw.slot = nil
	pw.hasSend = false
	pw.pendingDelivered = false
	pw.slotReadyCh = make(chan struct{})
	pw.cond.Broadcast()
	ch := pw.consumedCh
	pw.consumedCh = nil
	return ch
}

// Reset drops any in-flight/held value and frees a parked sender; used when an
// edge is deleted in the editor.
func (pw *PacedWire) Reset() {
	pw.mu.Lock()
	ch := pw.resetLocked()
	pw.mu.Unlock()

	if ch != nil {
		close(ch)
	}
}

// Delete persistently silences the wire: sets deleted=true so the source stops
// placing beads, then performs the shared Reset teardown to drop the in-flight
// value and free any parked WaitConsumed. Currently one-way — no restore
// message exists yet.
func (pw *PacedWire) Delete() {
	pw.mu.Lock()
	pw.deleted = true
	// Capture pulse state BEFORE resetLocked zeroes them; log breadcrumb while
	// still under the lock so the snapshot is coherent.
	hadPulse := pw.inFlight || pw.hasSend
	pw.Trace.Breadcrumb("wire_delete_drop_pulse",
		pw.Target, pw.TargetHandle,
		fmt.Sprintf("had_pulse=%v inFlight=%v hasSend=%v", hadPulse, pw.inFlight, pw.hasSend))
	ch := pw.resetLocked()
	pw.mu.Unlock()

	if ch != nil {
		close(ch)
	}
}

// Restore clears the deleted flag set by Delete so the wire carries pulses
// again (edge re-added in the editor). It runs the shared resetLocked teardown
// so the wire starts clean and the source resumes placing beads.
func (pw *PacedWire) Restore() {
	pw.mu.Lock()
	pw.deleted = false
	ch := pw.resetLocked()
	pw.mu.Unlock()

	if ch != nil {
		close(ch)
	}
}

// InFlight reports whether a bead is currently traversing this wire (placed
// by Send but not yet delivered into the destination slot by NotifyDelivered).
func (pw *PacedWire) InFlight() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return pw.inFlight
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
