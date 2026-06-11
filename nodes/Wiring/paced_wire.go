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

// positionEmitIntervalMs is the per-frame position-stream cadence (~60 Hz).
// MODEL.md: "The ~16 ms position emit is a render cadence, not a clock." The
// delivery goroutine wakes on the one clock every interval to emit the bead's
// evaluated position; this is the report rate, not the bead speed (speed stays
// pulseSpeed; arrival stays at inFlightMs regardless of interval).
const positionEmitIntervalMs = 16

// beadPlacement bundles everything one placement needs. The in-flight time times
// delivery (Phase 1); the segment endpoints + source identity drive the
// per-frame position stream (Phase 2). Geometry travels WITH the bead, never
// stored on the shared wire, so fan-in is safe: each in-flight bead evaluates the
// exact segment it is drawn on. The zero value (empty segment + identity) means "no
// position stream" — unit tests that only exercise delivery pass just InFlightMs.
type beadPlacement struct {
	InFlightMs float64
	// Position-stream context. Start/End are this edge's straight-segment endpoints
	// (source OUT-port world pos, dest IN-port world pos). Node/Port are the SOURCE
	// node id + output port — the position trace key, matching the send event so the
	// renderer routes by source+sourceHandle (fan-out).
	Start, End vec3
	Node, Port string
}

// streams reports whether this placement carries position-stream context. False
// for the bare-delivery placements used by unit tests (empty Node).
func (bp beadPlacement) streams() bool {
	return bp.Node != ""
}

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
	// In-flight bead geometry (Phase 3, MODEL.md "Geometry and time"). Held on the
	// wire so a mid-flight geometry edit (node-move) can re-derive the remaining
	// travel from the NEW arc and the distance already covered. Distance is NOT
	// stored: it is a pure function of the one clock — distanceCovered =
	// pulseSpeed × (clock.Now() − inFlightPlacement) — so a geometry edit changes
	// only inFlightArc/inFlightSegment and the deadline re-derives from the same
	// covered distance (deliver immediately when newArc ≤ covered). pulseSpeed is
	// the uniform speed; inFlightStreams gates the position emit (false for the
	// bare-delivery placements used by unit tests). All guarded by mu, valid only
	// while inFlight is true.
	pulseSpeed        float64
	inFlightPlacement time.Duration // clock active-elapsed reading when the bead was placed
	inFlightArc       float64       // current arc length of the bead's edge (re-derived on geometry edit)
	inFlightSegment   wireSegment   // current straight-segment endpoints of the bead's edge (re-derived on geometry edit)
	inFlightVal       int           // bead value, echoed on the position stream
	inFlightNode      string        // source node id — the position/cancel routing key
	inFlightPort      string        // source output port — the position/cancel routing key
	inFlightStreams   bool          // whether this bead carries position-stream context
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
		pulseSpeed:              pulseSpeed,
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
// slot at placementElapsed+bp.InFlightMs. bp carries the bead's per-edge travel
// time (arcLength/pulseSpeed on the SOURCE edge) and, for the position stream, the
// bead's own curve control points + source identity. Returns ErrCanceled if ctx
// is done before the wire clears. If the wire is faded, Send returns nil
// immediately without placing a bead.
//
// Backpressure: Send blocks while inFlight is true (wire occupied by a prior
// bead not yet delivered into its destination slot). It does NOT wait on Done
// and does NOT observe the destination slot directly.
func (pw *PacedWire) Send(ctx context.Context, value any, bp beadPlacement) error {
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
	pw.startDeliveryLocked(value, bp)
	pw.mu.Unlock()

	return nil
}

// SendDeliverOnly places a bead timed for delivery at placement+inFlightMs with
// NO position stream (empty curve/identity). It exists for cross-package tests
// (node firing-rule tests, the headless cascade) that exercise delivery timing
// only; the position stream is covered by Wiring's own deterministic verifier.
// Production code places beads via the typed Out (TrySend/TryEmit), which always
// carries the full curve.
func (pw *PacedWire) SendDeliverOnly(ctx context.Context, value any, inFlightMs float64) error {
	return pw.Send(ctx, value, beadPlacement{InFlightMs: inFlightMs})
}

// TryPlace is the non-blocking placement used by the fire-and-forget send rule.
// It never blocks and never overwrites an in-flight bead: if the wire is faded
// or already in-flight, it returns false (the send is dropped). Otherwise it
// places the bead exactly like Send's placement block, schedules the bead's
// clock-timed delivery at placementElapsed+bp.InFlightMs, and returns true.
func (pw *PacedWire) TryPlace(value any, bp beadPlacement) bool {
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
	pw.startDeliveryLocked(value, bp)
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

// startDeliveryLocked captures the placed bead's in-flight geometry onto the wire
// and launches the clock-timed delivery goroutine. Must be called with pw.mu held,
// immediately after setting inFlight=true. It records the placement elapsed reading
// and the bead's arc/curve/identity (the distance-based state a mid-flight geometry
// edit re-derives from), then relaunches the walker via relaunchDeliveryLocked.
//
// Distance-based model (MODEL.md "Geometry and time"): the only stored quantity is
// inFlightArc — distanceCovered is a pure function of the one clock, pulseSpeed ×
// (clock.Now() − inFlightPlacement). So `inFlightTime = arcLength / pulseSpeed` and
// `t = distanceCovered / arcLength` are both derived, never a second timer. A
// delivery-only placement (no stream geometry, used by unit tests) carries a bare
// InFlightMs; we express it as an equivalent arc (InFlightMs × pulseSpeed) so the
// single deadline formula `placement + arc/pulseSpeed` reproduces it exactly.
func (pw *PacedWire) startDeliveryLocked(value any, bp beadPlacement) {
	placement := pw.clock.Now()
	beadVal, _ := value.(int)

	pw.inFlightPlacement = placement
	pw.inFlightVal = beadVal
	pw.inFlightNode = bp.Node
	pw.inFlightPort = bp.Port
	pw.inFlightSegment = wireSegment{Start: bp.Start, End: bp.End}
	pw.inFlightStreams = bp.streams()
	// Express the per-edge in-flight time as an arc so deadline = placement +
	// arc/pulseSpeed is the one formula for both streamed and delivery-only beads.
	pw.inFlightArc = bp.InFlightMs * pw.pulseSpeed

	pw.relaunchDeliveryLocked()
}

// relaunchDeliveryLocked (re)spawns the delivery walker for the current in-flight
// bead from the wire's held in-flight geometry. Must be called with pw.mu held and
// inFlight true. It bumps deliverGen (invalidating any prior walker), installs a
// fresh cancellable context, and starts a goroutine that walks the ONE clock in
// ~16 ms steps. Each wake reads the LIVE inFlightArc/inFlightCurve under the lock,
// so a mid-flight ReviseInFlightGeometry relaunch picks up the new arc: t and the
// deadline are computed distance-based (t = covered/arc, deadline = placement +
// arc/pulseSpeed), and the new arc may put the deadline already behind Now() — in
// which case the next tick clamps to the deadline and delivery fires immediately.
//
// Go shape (MODEL.md): the wire reads the ONE clock in its OWN goroutine —
// no central tick loop or scheduler. WaitUntil is pause-aware (active elapsed
// freezes while halted), so NO position is emitted while halted and the bead
// resumes where it left off. The walker self-cancels if its generation no longer
// matches — so a Reset/Delete/Restore/Revise (which bump deliverGen and cancel the
// context) stops the stream without emitting/delivering a stale bead.
func (pw *PacedWire) relaunchDeliveryLocked() {
	pw.deliverGen++
	gen := pw.deliverGen
	clk := pw.clock
	tr := pw.Trace
	placement := pw.inFlightPlacement
	pulseSpeed := pw.pulseSpeed

	// Cancel any prior walker's parked WaitUntil, then install a fresh context.
	if pw.deliverCancel != nil {
		pw.deliverCancel()
	}
	dctx, cancel := context.WithCancel(context.Background())
	pw.deliverCancel = cancel

	go func() {
		interval := time.Duration(positionEmitIntervalMs * float64(time.Millisecond))
		// next is the active-elapsed instant of the upcoming ~16 ms tick.
		next := placement + interval
		for {
			// Read the live arc/curve: a mid-flight geometry edit relaunches this
			// walker after revising them, but a tick that races the relaunch still
			// re-reads here so the deadline tracks the current arc.
			pw.mu.Lock()
			if pw.deliverGen != gen {
				pw.mu.Unlock()
				return
			}
			arc := pw.inFlightArc
			seg := pw.inFlightSegment
			beadVal := pw.inFlightVal
			node, port := pw.inFlightNode, pw.inFlightPort
			stream := pw.inFlightStreams && tr != nil && arc > 0
			pw.mu.Unlock()

			// inFlightTime = arc / pulseSpeed (derived, MODEL.md). deadline is the
			// active-elapsed instant the traversal completes.
			deadline := placement
			if pulseSpeed > 0 {
				deadline += time.Duration(arc / pulseSpeed * float64(time.Millisecond))
			}

			// Wait for the next tick, but never past the delivery deadline. When a
			// shrink put the deadline behind now, target clamps to deadline and the
			// traversal completes immediately on this wake.
			target := next
			final := false
			if target >= deadline {
				target = deadline
				final = true
			}
			if err := clk.WaitUntil(dctx, target); err != nil {
				// Context canceled by teardown (Reset/Delete/Revise) — stop the
				// stream, do not emit, do not deliver.
				return
			}

			if stream {
				// distanceCovered = pulseSpeed × (active elapsed since placement).
				// Use the tick TARGET (WaitUntil guarantees Now() >= target) so the
				// emitted position is deterministic. t = covered/arc keeps the bead
				// on its current curve even after the arc was revised mid-flight.
				covered := pulseSpeed * float64(target-placement) / float64(time.Millisecond)
				t := 0.0
				if arc > 0 {
					t = covered / arc
				}
				if t > 1 {
					t = 1
				}
				pos := lerp(seg.Start, seg.End, t)
				// Emit the bead's fractional progress t alongside the world position:
				// Go owns progress; the editor places the bead at lerp(liveStart,
				// liveEnd, t) on its LOCAL (dragged) node port positions so the bead
				// rides the live wire with no round-trip lag.
				tr.Position(node, port, beadVal, pos.X, pos.Y, pos.Z, t)
			}

			if final {
				// Deadline reached (final position already emitted at t==1 above):
				// complete the Phase-1 delivery under the lock, guarding once more
				// against a stale waiter (teardown/revise between WaitUntil and here).
				pw.mu.Lock()
				if pw.deliverGen == gen {
					pw.tryDeliverLocked()
				}
				pw.mu.Unlock()
				return
			}
			next += interval
		}
	}()
}

// ReviseInFlightGeometry re-derives an in-flight bead's remaining travel after a
// geometry edit (node-move) changed the bead's edge (MODEL.md "Geometry and time").
// It preserves the bead's FRACTIONAL progress t (its proportion along the wire),
// NOT the absolute distance covered: on a geometry change the bead stays at the same
// fraction t and the remaining time is recomputed from the NEW arc so UNIFORM PULSE
// SPEED is preserved — remaining = (1−t)·newArc/pulseSpeed. Preserving distance
// instead would let t swing as the arc length changes, racing the bead up/down the
// wire as the user drags a node.
//
// Implementation: capture the current fraction t (= covered/oldArc on the one clock),
// then rebase inFlightPlacement to Now() − t·newArc/pulseSpeed. After the rebase the
// walker's distance-based arithmetic (covered = pulseSpeed×elapsed, t = covered/arc,
// deadline = placement + arc/pulseSpeed) reproduces the SAME fraction t against the
// new arc and a deadline (1−t)·newArc/pulseSpeed out — uniform speed, no t swing.
//
// No-op when no bead is in flight (the new geometry is read off the source Out by the
// next placement) or the wire is deleted. The position stream now evaluates the new
// segment, so the bead tracks the re-drawn wire at the same fraction.
func (pw *PacedWire) ReviseInFlightGeometry(newArc float64, newSeg wireSegment) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if pw.deleted || !pw.inFlight {
		return
	}
	// Capture the bead's current fraction t along the OLD arc before revising.
	oldArc := pw.inFlightArc
	t := 0.0
	if oldArc > 0 && pw.pulseSpeed > 0 {
		covered := pw.pulseSpeed * float64(pw.clock.Now()-pw.inFlightPlacement) / float64(time.Millisecond)
		t = covered / oldArc
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
	}
	pw.inFlightArc = newArc
	pw.inFlightSegment = newSeg
	// Rebase placement so elapsed-since-placement maps to the same fraction t on the
	// NEW arc: covered' = t·newArc ⇒ placement' = Now() − (t·newArc/pulseSpeed).
	if pw.pulseSpeed > 0 {
		coveredNew := t * newArc
		pw.inFlightPlacement = pw.clock.Now() - time.Duration(coveredNew/pw.pulseSpeed*float64(time.Millisecond))
	}
	pw.relaunchDeliveryLocked()
}

// tryDeliverLocked completes a bead's traversal: if the slot is empty it delivers
// (deliverLocked), otherwise it defers the delivery to Done (pendingDelivered).
// A no-op when the wire is deleted or no bead is in flight (idempotent against a
// duplicate trigger). Must be called with pw.mu held. This is the single delivery
// path; the clock goroutine is its only caller (Go times its own delivery — there
// is no cross-boundary delivery signal, MODEL.md).
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
	// Bead delivered normally — drop its in-flight identity so a later Delete on
	// this (now empty) wire does not echo a spurious pulse-cancelled.
	pw.inFlightArc = 0
	pw.inFlightSegment = wireSegment{}
	pw.inFlightVal = 0
	pw.inFlightNode, pw.inFlightPort = "", ""
	pw.inFlightStreams = false
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
	// Drop the in-flight bead's geometry/identity so a later Delete can't echo a
	// stale pulse-cancelled for a bead that already left this wire.
	pw.inFlightArc = 0
	pw.inFlightSegment = wireSegment{}
	pw.inFlightVal = 0
	pw.inFlightNode, pw.inFlightPort = "", ""
	pw.inFlightStreams = false
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
//
// Delete-mid-flight (Phase 3): if a bead is in flight when the edge is deleted, Go
// atomically cancels the clock-delivery (resetLocked bumps deliverGen + cancels the
// walker context), drops the bead, and emits a pulse-cancelled trace keyed by the
// bead's SOURCE node+port — the same routing key as the send/position stream — so
// the renderer removes the bead sprite. The cancel routing identity is captured
// under the lock; the trace is emitted after unlocking (Trace.PulseCancelled sends
// on a channel and must not hold the wire lock).
func (pw *PacedWire) Delete() {
	pw.mu.Lock()
	pw.deleted = true
	// Capture pulse state BEFORE resetLocked zeroes them; log breadcrumb while
	// still under the lock so the snapshot is coherent.
	hadPulse := pw.inFlight || pw.hasSend
	cancelInFlight := pw.inFlight
	cancelNode, cancelPort, cancelVal := pw.inFlightNode, pw.inFlightPort, pw.inFlightVal
	pw.Trace.Breadcrumb("wire_delete_drop_pulse",
		pw.Target, pw.TargetHandle,
		fmt.Sprintf("had_pulse=%v inFlight=%v hasSend=%v", hadPulse, pw.inFlight, pw.hasSend))
	ch := pw.resetLocked()
	pw.mu.Unlock()

	// Echo the dropped in-flight bead so TS removes its sprite (routed by source).
	if cancelInFlight {
		pw.Trace.PulseCancelled(cancelNode, cancelPort, cancelVal)
	}

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
