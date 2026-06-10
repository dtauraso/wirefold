package Wiring

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
// Clock-driven delivery (Phase 1): the wire times its own delivery on the one
// monotonic clock (MODEL.md). When a bead is placed (Send/TryPlace with the
// bead's per-edge inFlightMs), the wire records the placement elapsed reading and
// starts a delivery goroutine that calls clock.WaitUntil(placementElapsed +
// inFlightMs). When that pause-aware deadline is reached, the goroutine delivers
// the bead into the slot. There is no TS "delivered" signal and no central
// scheduler — every wire reads the same clock independently. Pause freezes the
// arithmetic (WaitUntil does not advance while halted), so no delivery fires
// while halted; Reset/Delete cancel a pending clock-delivery.
//
// Deferred delivery: delivery NEVER blocks the deliverer. If the slot is full
// when the deadline is reached, the delivery is deferred: pendingDelivered is set
// to true. inFlight stays true so the source remains blocked. When Done clears
// the slot, it checks pendingDelivered and completes the deferred delivery right
// there (under the same lock), filling the slot again and closing the fresh
// slotReadyCh so the next Recv unblocks.
//
// Lifecycle per value:
//
//	Send:            blocks while inFlight is true, places bead into pending,
//	                 sets inFlight=true, starts the clock-delivery goroutine,
//	                 returns. After Send, the source may call WaitConsumed to wait
//	                 until Done is called by the consumer (consume-gated policy
//	                 lives in the node, not here).
//	clock delivery:  when elapsed reaches placementElapsed+inFlightMs, calls
//	                 tryDeliverLocked: if slot empty — deliverLocked (moves
//	                 pending→slot, sets hasSend=true, clears inFlight, closes
//	                 slotReadyCh); if slot full — sets pendingDelivered=true.
//	Recv:            blocks until slotReadyCh is closed (slot filled), returns
//	                 value (slot stays full).
//	Done:            consumer finished; clears slot (hasSend=false), resets
//	                 slotReadyCh for the next cycle; if pendingDelivered, calls
//	                 deliverLocked to complete the deferred delivery immediately.
//	                 Also closes consumedCh so any caller of WaitConsumed unblocks.
//
// Deleting a wire destroys both bits and cancels any pending clock-delivery; a
// fresh wire has inFlight=false so the source can send immediately.
type PacedWire struct {
	mu               sync.Mutex
	cond             *sync.Cond
	pending          any          // value placed by Send, not yet delivered into slot
	slot             any          // value in destination slot (set by clock delivery)
	hasSend          bool         // destination slot holds a value (not yet Done'd)
	inFlight         bool         // bead is on the wire, not yet delivered into slot
	pendingDelivered bool         // delivery deadline reached while slot was full; delivery deferred to Done
	faded            bool         // when true, Send skips without sending
	deleted          bool         // when true, the edge was deleted; source stops placing beads (distinct origin from faded)
	slotReadyCh      chan struct{} // closed when slot is filled; reset by Done
	consumedCh       chan struct{} // closed by Done to signal the source that the slot was consumed
	// clock is the one monotonic clock this wire reads to time its own delivery.
	// Injected by the loader (real) or set by tests (fake). Defaults to a real
	// clock from NewPacedWire so direct-construction call sites work unconfigured.
	clock Clock
	// deliverGen is bumped on every placement and on every teardown (Reset/Delete/
	// Restore). A delivery goroutine captures its generation at launch and only
	// delivers if the generation still matches — so a teardown or re-placement
	// cancels a stale waiter's effect. Guarded by mu.
	deliverGen uint64
	// deliverCancel cancels the context the current delivery goroutine waits on,
	// so Reset/Delete wake it promptly instead of leaving it parked on the clock.
	deliverCancel context.CancelFunc
	// MaxIncomingSimLatencyMs is the per-port aggregate max(SimLatencyMs) over
	// every edge feeding this destination port. It is NOT the travel-time of any
	// one bead — per-edge travel-time lives on the source Out. This aggregate is
	// read only by In.SimLatencyMs() to derive a windowed node's coincidence
	// window W. Set at load and recomputed on node-move. Guarded by mu.
	MaxIncomingSimLatencyMs float64
	Target           string       // destination node id — authoritative slot identity (set by loader)
	TargetHandle     string       // destination input-port name — authoritative slot identity (set by loader)
	Trace            *T.Trace     // injected by loader; used for breadcrumb diagnostics only
}

// NewPacedWire creates an empty PacedWire. arcLength is the straight-line
// distance between source and target (world units); pulseSpeed is in world-units
// per millisecond (use PulseSpeedWuPerMs). The derived latency seeds
// MaxIncomingSimLatencyMs (the per-port window aggregate); the loader raises it
// as further edges bind to this destination port. Per-bead travel-time is NOT
// stored here — it lives on the source Out.
func NewPacedWire(arcLength float64, pulseSpeed float64) *PacedWire {
	pw := &PacedWire{
		MaxIncomingSimLatencyMs: arcLength / pulseSpeed,
		slotReadyCh:             make(chan struct{}),
		consumedCh:              make(chan struct{}),
		clock:                   NewRealClock(),
	}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// SetClock injects the monotonic clock this wire reads to time delivery. The
// loader calls it so every wire shares ONE clock; tests call it with a FakeClock
// for deterministic delivery. Safe to call before any bead is placed.
func (pw *PacedWire) SetClock(c Clock) {
	pw.mu.Lock()
	pw.clock = c
	pw.mu.Unlock()
}

// SetFaded sets the faded flag. When faded is true, Send returns nil immediately
// without placing a bead. In-flight values already past the gate are unaffected.
func (pw *PacedWire) SetFaded(v bool) {
	pw.mu.Lock()
	pw.faded = v
	pw.mu.Unlock()
}

// Send places a bead on the wire and returns immediately once the bead is
// placed, then schedules the bead's clock-timed delivery into the destination
// slot at placementElapsed+inFlightMs. inFlightMs is the bead's per-edge travel
// time (arcLength/pulseSpeed on the SOURCE edge). Returns ErrCanceled if ctx is
// done before the wire clears. If the wire is faded, Send returns nil immediately
// without placing a bead.
//
// Backpressure: Send blocks while inFlight is true (wire occupied by a prior
// bead not yet delivered into its destination slot). It does NOT wait on Done
// and does NOT observe the destination slot directly.
func (pw *PacedWire) Send(ctx context.Context, value any, inFlightMs float64) error {
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
	pw.startDeliveryLocked(inFlightMs)
	pw.mu.Unlock()

	return nil
}

// TryPlace is the non-blocking placement used by the fire-and-forget send rule.
// It never blocks and never overwrites an in-flight bead: if the wire is faded
// or already in-flight, it returns false (the send is dropped). Otherwise it
// places the bead exactly like Send's placement block, schedules the bead's
// clock-timed delivery at placementElapsed+inFlightMs, and returns true.
func (pw *PacedWire) TryPlace(value any, inFlightMs float64) bool {
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
	pw.startDeliveryLocked(inFlightMs)
	pw.mu.Unlock()
	return true
}

// Recv blocks until the slot is filled (the clock-timed delivery puts the bead
// into the slot), then returns the value. The slot is NOT cleared; the consumer
// must call Done. Returns ErrCanceled if ctx is done.
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

// startDeliveryLocked launches the clock-timed delivery goroutine for the bead
// just placed. Must be called with pw.mu held, immediately after setting
// inFlight=true. It bumps deliverGen, captures the placement elapsed reading from
// the wire's clock, computes the delivery deadline (placementElapsed+inFlightMs),
// and spawns a goroutine that WaitUntil's that deadline (pause-aware) then calls
// tryDeliverLocked. The goroutine self-cancels if its generation no longer
// matches — so a Reset/Delete/Restore (which bump deliverGen and cancel the
// delivery context) cancels this delivery without delivering a stale bead.
func (pw *PacedWire) startDeliveryLocked(inFlightMs float64) {
	pw.deliverGen++
	gen := pw.deliverGen
	clk := pw.clock
	placement := clk.Now()
	deadline := placement + time.Duration(inFlightMs*float64(time.Millisecond))

	// Per-delivery cancellable context so teardown wakes the parked WaitUntil.
	dctx, cancel := context.WithCancel(context.Background())
	pw.deliverCancel = cancel

	go func() {
		// Block on the one clock until the pause-aware deadline is reached.
		if err := clk.WaitUntil(dctx, deadline); err != nil {
			// Context canceled by teardown (Reset/Delete) — do not deliver.
			return
		}
		pw.mu.Lock()
		// Only deliver if this is still the live placement (no teardown / no
		// newer bead) — deliverGen guards against a stale waiter.
		if pw.deliverGen == gen {
			pw.tryDeliverLocked()
		}
		pw.mu.Unlock()
	}()
}

// tryDeliverLocked completes a bead's traversal: if the slot is empty it delivers
// (deliverLocked), otherwise it defers the delivery to Done (pendingDelivered).
// A no-op when the wire is deleted or no bead is in flight (idempotent against a
// duplicate trigger). Must be called with pw.mu held. This is the single delivery
// path shared by the clock goroutine and (manual) NotifyDelivered.
func (pw *PacedWire) tryDeliverLocked() {
	if pw.deleted {
		return
	}
	if !pw.inFlight {
		// Already delivered (or never placed): nothing to do.
		return
	}
	if pw.hasSend {
		// Slot full: defer delivery to Done.
		pw.pendingDelivered = true
		return
	}
	pw.deliverLocked()
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
// without waiting for another delivery cycle.
func (pw *PacedWire) Done() {
	pw.mu.Lock()
	pw.slot = nil
	pw.hasSend = false
	pw.slotReadyCh = make(chan struct{})
	if pw.pendingDelivered {
		// A clock delivery's deadline was reached while the slot was full;
		// complete it now.
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

// NotifyDelivered is a manual immediate-delivery hook. As of Phase 1, Go's clock
// drives delivery (see startDeliveryLocked); the TS "delivered" stdin message no
// longer calls this — stdin_reader parses and ignores it. The method is retained
// (Phase 5 removes it) and shares the single tryDeliverLocked path, so it is
// idempotent: a call after the clock already delivered is a no-op (inFlight is
// false). It NEVER blocks. If the wire is deleted, the pulse is discarded.
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
	pw.tryDeliverLocked()
	pw.mu.Unlock()
	return nil
}

// resetLocked performs the shared teardown for Reset and Delete: drops any
// in-flight/held value, cancels any pending clock-delivery, installs a fresh
// slotReadyCh, broadcasts, and returns the consumedCh (now nil'd) so the caller
// can close it to free a parked WaitConsumed. Must be called with pw.mu held;
// the caller closes the returned channel after unlocking.
func (pw *PacedWire) resetLocked() chan struct{} {
	pw.inFlight = false
	pw.pending = nil
	pw.slot = nil
	pw.hasSend = false
	pw.pendingDelivered = false
	// Cancel any pending clock-delivery: bump the generation so an in-flight
	// delivery goroutine's tryDeliverLocked becomes a no-op, and cancel its
	// WaitUntil context so it wakes and exits promptly instead of parking.
	pw.deliverGen++
	if pw.deliverCancel != nil {
		pw.deliverCancel()
		pw.deliverCancel = nil
	}
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
// by Send but not yet delivered into the destination slot by the clock).
func (pw *PacedWire) InFlight() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return pw.inFlight
}

// Occupied reports whether the wire is non-empty: either a bead is in flight
// (inFlight=true) or a delivered pulse is parked in the slot waiting to be
// consumed (hasSend=true). Returns true if either condition holds.
func (pw *PacedWire) Occupied() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return pw.inFlight || pw.hasSend
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
