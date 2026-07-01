package Wiring

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// deliveredBead is a value that has arrived at the wire's destination and is waiting
// to be read by Recv or PollRecv. Recv/PollRecv consume on read (no separate Done).
type deliveredBead struct {
	val int
}

// PulseSpeedWuPerMs aliases CurveParamPulseSpeedWuPerMs for call sites that
// pass it to NewPacedWire.  The canonical value lives in curve_params.go so
// the codegen tool can export it to TS.
const PulseSpeedWuPerMs = CurveParamPulseSpeedWuPerMs

// ErrCanceled is returned by Send or Recv when the context is canceled.
var ErrCanceled = errors.New("paced wire: context canceled")

// positionEmitIntervalMs is the per-frame position-stream cadence (~60 Hz).
// MODEL.md: "The ~16 ms position emit is a render cadence, not a clock." A
// per-bead delivery goroutine wakes on the one clock every interval to emit
// its bead's evaluated position; this is the report rate, not the bead speed.
const positionEmitIntervalMs = 16

// beadPlacement bundles everything one placement needs. The in-flight time times
// delivery; the segment endpoints + source identity drive the per-frame position
// stream. Geometry travels WITH the bead, never stored on the shared wire, so
// fan-in is safe: each in-flight bead evaluates the exact segment it is drawn on.
// The zero value (empty segment + identity) means "no position stream" — unit
// tests that only exercise delivery pass just InFlightMs.
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

// inflightBead is one bead traversing the wire. Each bead carries its own
// geometry so a mid-flight geometry edit (node-move) re-derives the remaining
// travel from the NEW arc while preserving the bead's FRACTIONAL progress t.
// Distance is NOT stored: distanceCovered = pulseSpeed × (clock.Now() −
// placement) is a pure function of the one clock (MODEL.md "Geometry and time").
// All fields are guarded by pw.mu.
type inflightBead struct {
	val       int
	placement time.Duration // clock active-elapsed reading when placed
	startedAt time.Duration // clock reading when the driver goroutine anchors its first tick;
	// set to placement time so the driver's first tick is always placement+interval
	// regardless of when the goroutine actually starts executing.
	arc     float64     // current arc length of this bead's edge
	seg     wireSegment // current straight-segment endpoints of this bead's edge
	node    string      // source node id — the position/cancel routing key
	port    string      // source output port — the position/cancel routing key
	streams bool        // whether this bead carries position-stream context
	gen     uint64      // per-bead id; the driven loop self-cancels on gen mismatch (teardown)
}

// PacedWire is a multi-bead FIFO transport. Beads are placed via placeBeadNoWalker
// and delivered by the owning node's goroutine driving DriveBeadsToDelivery — no
// per-bead goroutine. The source never waits on the destination; each bead is placed
// immediately and driven to the `delivered` FIFO on the caller's goroutine at its own
// deadline. Recv pops `delivered` in send order and CONSUMES on read (no separate
// Done step).
//
// Clock-driven delivery: the wire times its own delivery on the one monotonic
// clock (MODEL.md). When a bead is placed, the wire records the placement elapsed
// reading; the driven loop calls clock.WaitUntil(placement + arc/pulseSpeed). When
// that pause-aware deadline is reached, the driven loop moves the bead from `inflight`
// to `delivered`. There is no TS "delivered" signal and no central scheduler — every
// wire reads the same clock independently. Pause freezes the arithmetic (WaitUntil
// does not advance while halted); Reset/Delete bump teardownGen so the driven loop
// drops the bead.
type PacedWire struct {
	mu   sync.Mutex
	cond *sync.Cond
	// inflight holds beads traversing the wire, in send order. delivered holds
	// arrived-but-unread values, in arrival order (FIFO). All mutation under mu.
	inflight  []inflightBead
	delivered []deliveredBead
	// nextGen mints a unique id for each placed bead (walker self-cancel key) and
	// is also bumped on teardown to invalidate ALL outstanding walkers at once.
	nextGen uint64
	// teardownGen: a walker whose bead gen is < teardownGen is invalidated wholesale
	// (Reset/Delete). Beads placed after a teardown get gen >= teardownGen.
	teardownGen uint64
	faded       bool // when true, placeBeadNoWalker places nothing
	deleted     bool // when true, the edge was deleted; source places no beads
	// clock is the one monotonic clock this wire reads to time its own delivery.
	clock      Clock
	pulseSpeed float64
	// MaxIncomingSimLatencyMs is the per-port aggregate max(SimLatencyMs) over
	// every edge feeding this destination port. Read only by In.SimLatencyMs().
	MaxIncomingSimLatencyMs float64
	// incomingLatency tracks each feeding edge's own SimLatencyMs (edgeId → latency).
	incomingLatency map[string]float64
	Target          string   // destination node id — authoritative slot identity
	TargetHandle    string   // destination input-port name — authoritative slot identity
	Trace           *T.Trace // injected by loader; used for breadcrumb diagnostics only
}

// NewPacedWire creates an empty PacedWire. arcLength is the straight-line
// distance between source and target (world units); pulseSpeed is in world-units
// per millisecond (use PulseSpeedWuPerMs).
func NewPacedWire(arcLength float64, pulseSpeed float64) *PacedWire {
	pw := &PacedWire{
		MaxIncomingSimLatencyMs: arcLength / pulseSpeed,
		pulseSpeed:              pulseSpeed,
		clock:                   NewRealClock(),
	}
	pw.cond = sync.NewCond(&pw.mu)
	return pw
}

// SetIncomingLatency records one feeding edge's own travel-time (SimLatencyMs)
// and recomputes MaxIncomingSimLatencyMs as the max over every feeding edge.
func (pw *PacedWire) SetIncomingLatency(edgeID string, lat float64) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if pw.incomingLatency == nil {
		pw.incomingLatency = map[string]float64{}
	}
	pw.incomingLatency[edgeID] = lat
	var maxLat float64
	for _, l := range pw.incomingLatency {
		if l > maxLat {
			maxLat = l
		}
	}
	pw.MaxIncomingSimLatencyMs = maxLat
}

// SetClock injects the monotonic clock this wire reads to time delivery.
func (pw *PacedWire) SetClock(c Clock) {
	pw.mu.Lock()
	pw.clock = c
	pw.mu.Unlock()
}

// SetFaded sets the faded flag. When faded is true, Send/TryPlace place no bead.
func (pw *PacedWire) SetFaded(v bool) {
	pw.mu.Lock()
	pw.faded = v
	pw.mu.Unlock()
}


// PlaceAndDrive places a bead and drives it to delivery on a new goroutine.
// Returns false if the wire is faded/deleted (nothing placed). This is the
// public entry point used by cross-package tests and by placeAndDrive in the
// Wiring-internal test helper.
func (pw *PacedWire) PlaceAndDrive(ctx context.Context, value any, bp beadPlacement) bool {
	gen, ok := pw.placeBeadNoWalker(value, bp)
	if !ok {
		return false
	}
	go pw.DriveBeadToDelivery(ctx, gen)
	return true
}

// PlaceAndDriveDeliverOnly places a delivery-only bead (no position stream) and
// drives it on a background goroutine. Equivalent to the deleted SendDeliverOnly
// for cross-package tests that only exercise delivery timing.
func (pw *PacedWire) PlaceAndDriveDeliverOnly(ctx context.Context, value any, inFlightMs float64) bool {
	return pw.PlaceAndDrive(ctx, value, beadPlacement{InFlightMs: inFlightMs})
}

// placeBeadNoWalker appends a bead WITHOUT launching a walker goroutine,
// returning the bead's gen so the caller can drive delivery synchronously.
// Returns (0, false) when faded/deleted (nothing placed).
func (pw *PacedWire) placeBeadNoWalker(value any, bp beadPlacement) (gen uint64, ok bool) {
	pw.mu.Lock()
	if pw.faded || pw.deleted {
		pw.mu.Unlock()
		return 0, false
	}
	beadVal, _ := value.(int)
	pw.nextGen++
	if pw.nextGen < pw.teardownGen {
		pw.nextGen = pw.teardownGen
	}
	now := pw.clock.Now()
	b := inflightBead{
		val:       beadVal,
		placement: now,
		startedAt: now, // anchor first tick to placement time, not goroutine-start time
		arc:       bp.InFlightMs * pw.pulseSpeed,
		seg:       wireSegment{Start: bp.Start, End: bp.End},
		node:      bp.Node,
		port:      bp.Port,
		streams:   bp.streams(),
		gen:       pw.nextGen,
	}
	pw.inflight = append(pw.inflight, b)
	pw.cond.Broadcast()
	gen = b.gen
	pw.mu.Unlock()
	return gen, true
}

// driveItem carries one bead's context for DriveBeadsToDelivery.
type driveItem struct {
	pw  *PacedWire
	gen uint64
}

// DriveBeadsToDelivery drives multiple beads on different PacedWires in lockstep
// on the calling goroutine — no additional goroutines. Each ~16 ms frame it
// advances every not-yet-final bead, emits position traces, and marks delivered
// items done. Blocks until all items are delivered or ctx is canceled.
func DriveBeadsToDelivery(ctx context.Context, items []driveItem) {
	if len(items) == 0 {
		return
	}

	// Per-item state: done marks items that have finished delivery.
	done := make([]bool, len(items))
	remaining := len(items)

	interval := time.Duration(positionEmitIntervalMs * float64(time.Millisecond))

	// Compute the first tick target for each item anchored to its placement.
	nexts := make([]time.Duration, len(items))
	for i, it := range items {
		if done[i] {
			continue
		}
		it.pw.mu.Lock()
		idx := it.pw.findInflightLocked(it.gen)
		var placement, startedAt time.Duration
		if idx >= 0 {
			placement = it.pw.inflight[idx].placement
			startedAt = it.pw.inflight[idx].startedAt
		}
		it.pw.mu.Unlock()

		if idx < 0 {
			done[i] = true
			remaining--
			continue
		}
		// Anchor the first tick to startedAt (= clock reading at placement time).
		// This ensures intermediate position ticks are emitted even when the driver
		// goroutine starts late (after the test advances the clock past the deadline).
		next := placement + interval
		if next <= startedAt {
			steps := (startedAt-placement)/interval + 1
			next = placement + steps*interval
		}
		nexts[i] = next
	}

	// Use the clock from the first live item (all items share the same clock).
	var clk Clock
	for i, it := range items {
		if !done[i] {
			clk = it.pw.clock
			break
		}
	}
	if clk == nil || remaining == 0 {
		return
	}

	for remaining > 0 {
		if ctx.Err() != nil {
			return
		}

		// Find the minimum next tick across live items, capping each item's next
		// at its bead's deadline so we never park past the delivery point.
		var minNext time.Duration
		first := true
		for i, it := range items {
			if done[i] {
				continue
			}
			// Cap nexts[i] at the bead's delivery deadline so WaitUntil never
			// parks past the point where final=true would be set.
			it.pw.mu.Lock()
			idx := it.pw.findInflightLocked(it.gen)
			if idx >= 0 {
				b := it.pw.inflight[idx]
				if it.pw.pulseSpeed > 0 {
					dl := b.placement + time.Duration(b.arc/it.pw.pulseSpeed*float64(time.Millisecond))
					if nexts[i] > dl {
						nexts[i] = dl
					}
				}
			}
			it.pw.mu.Unlock()
			if first || nexts[i] < minNext {
				minNext = nexts[i]
				first = false
			}
		}

		if err := clk.WaitUntil(ctx, minNext); err != nil {
			return
		}

		// Advance all items whose next tick is at or before minNext.
		for i, it := range items {
			if done[i] || nexts[i] > minNext {
				continue
			}

			it.pw.mu.Lock()
			if it.gen < it.pw.teardownGen {
				it.pw.mu.Unlock()
				done[i] = true
				remaining--
				continue
			}
			idx := it.pw.findInflightLocked(it.gen)
			if idx < 0 {
				it.pw.mu.Unlock()
				done[i] = true
				remaining--
				continue
			}
			b := it.pw.inflight[idx]
			arc := b.arc
			placement := b.placement
			it.pw.mu.Unlock()

			pulseSpeed := it.pw.pulseSpeed
			deadline := placement
			if pulseSpeed > 0 {
				deadline += time.Duration(arc / pulseSpeed * float64(time.Millisecond))
			}

			target := nexts[i]
			final := false
			if target >= deadline {
				target = deadline
				final = true
			}

			it.pw.mu.Lock()
			emit, posArgs, isFinal := it.pw.advanceBeadLocked(it.gen, target)
			// advanceBeadLocked released the lock.
			if emit {
				it.pw.Trace.Position(posArgs.node, posArgs.port, posArgs.val, posArgs.x, posArgs.y, posArgs.z, posArgs.t, posArgs.gen)
			}
			if final || isFinal {
				it.pw.mu.Lock()
				if ai, ok := it.pw.deliverHeadLocked(ctx, it.gen); ok {
					it.pw.emitArrive(ai)
				}
				done[i] = true
				remaining--
				continue
			}
			nexts[i] += interval
		}
	}
}

// deliverHeadLocked completes a bead's final delivery: it waits (parking on
// pw.cond) until the bead identified by gen is at the FIFO head, then moves it
// from inflight to delivered and returns its source identity for the arrive
// trace. The caller MUST hold pw.mu on entry; this method always releases it
// before returning (matching the inline delivery window it replaced). Returns
// ok=false — with the lock released — when the bead was torn down or already
// dropped (no arrive emit), ok=true when it was delivered.
func (pw *PacedWire) deliverHeadLocked(ctx context.Context, gen uint64) (ai arriveInfo, ok bool) {
	// stop is the canceller installed only when this bead must actually PARK behind an
	// earlier FIFO head (j != 0). Without it a waiter would park on pw.cond forever if
	// the head bead's driver exits on ctx cancellation without delivering — the
	// cond.Wait has no ctx wakeup of its own. broadcastOnCancel wakes it on ctx.Done
	// (same mechanism Recv uses); the loop re-checks ctx.Err() and returns ok=false
	// (no delivery). The common single-bead fast path (j == 0) never parks, so it
	// spawns nothing and behavior is unchanged.
	var stop chan struct{}
	defer func() {
		if stop != nil {
			close(stop)
		}
	}()
	for {
		if ctx.Err() != nil {
			pw.mu.Unlock()
			return arriveInfo{}, false
		}
		if gen < pw.teardownGen {
			pw.mu.Unlock()
			return arriveInfo{}, false
		}
		j := pw.findInflightLocked(gen)
		if j < 0 {
			pw.mu.Unlock()
			return arriveInfo{}, false
		}
		if j == 0 {
			break
		}
		if stop == nil {
			stop = broadcastOnCancel(ctx, &pw.mu, pw.cond)
		}
		pw.cond.Wait()
	}
	db := pw.inflight[0]
	pw.inflight = pw.inflight[1:]
	pw.delivered = append(pw.delivered, deliveredBead{val: db.val})
	pw.cond.Broadcast()
	ai = arriveInfo{emit: db.streams, node: db.node, port: db.port, value: db.val, gen: db.gen}
	pw.mu.Unlock()
	return ai, true
}

// DriveBeadToDelivery runs the same per-frame loop the walker would run for
// the bead identified by gen, but SYNCHRONOUSLY on the caller's goroutine.
// ctx is the caller's context (canceled by node teardown). Blocks until the
// bead is delivered or ctx is canceled / the wire is torn down.
func (pw *PacedWire) DriveBeadToDelivery(ctx context.Context, gen uint64) {
	DriveBeadsToDelivery(ctx, []driveItem{{pw: pw, gen: gen}})
}

// findInflightLocked returns the index of the bead with this gen, or -1.
func (pw *PacedWire) findInflightLocked(gen uint64) int {
	for i := range pw.inflight {
		if pw.inflight[i].gen == gen {
			return i
		}
	}
	return -1
}


// Recv blocks until a delivered value is available, then pops and returns it
// (FIFO, in send order). Recv CONSUMES on read — there is no separate Done step.
// Returns ErrCanceled if ctx is done before a value arrives.
func (pw *PacedWire) Recv(ctx context.Context) (any, error) {
	done := broadcastOnCancel(ctx, &pw.mu, pw.cond)
	defer close(done)

	pw.mu.Lock()
	for len(pw.delivered) == 0 && ctx.Err() == nil {
		pw.cond.Wait()
	}
	if len(pw.delivered) == 0 {
		pw.mu.Unlock()
		return nil, ErrCanceled
	}
	b := pw.delivered[0]
	pw.delivered = pw.delivered[1:]
	pw.mu.Unlock()
	return b.val, nil
}

// PollRecv is the non-blocking variant of Recv. It pops and returns the front
// delivered value if one is present, else (nil, false). Like Recv, PollRecv
// CONSUMES on read.
func (pw *PacedWire) PollRecv() (any, bool) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if len(pw.delivered) > 0 {
		b := pw.delivered[0]
		pw.delivered = pw.delivered[1:]
		return b.val, true
	}
	return nil, false
}

// ReviseInFlightGeometry re-derives EVERY in-flight bead's remaining travel after a
// geometry edit (node-move) changed the edge (MODEL.md "Geometry and time"). It
// preserves each bead's FRACTIONAL progress t (its proportion along the wire), NOT
// the absolute distance covered: each bead stays at the same fraction t and the
// remaining time is recomputed from the NEW arc so UNIFORM PULSE SPEED holds —
// remaining = (1−t)·newArc/pulseSpeed. The driven loop re-reads each bead's live
// arc/seg every frame, so the new geometry takes effect without any relaunch.
// No-op when no bead is in flight or the wire is deleted.
func (pw *PacedWire) ReviseInFlightGeometry(newArc float64, newSeg wireSegment) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if pw.deleted || len(pw.inflight) == 0 {
		return
	}
	now := pw.clock.Now()
	for i := range pw.inflight {
		b := &pw.inflight[i]
		t := 0.0
		if b.arc > 0 && pw.pulseSpeed > 0 {
			covered := pw.pulseSpeed * float64(now-b.placement) / float64(time.Millisecond)
			t = covered / b.arc
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
		}
		b.arc = newArc
		b.seg = newSeg
		// Rebase placement so elapsed-since-placement maps to the same fraction t on
		// the NEW arc: covered' = t·newArc ⇒ placement' = Now() − (t·newArc/pulseSpeed).
		if pw.pulseSpeed > 0 {
			coveredNew := t * newArc
			b.placement = now - time.Duration(coveredNew/pw.pulseSpeed*float64(time.Millisecond))
		}
		// Update startedAt to now so the driver's next tick is anchored to the
		// rebase point (avoids replaying the traversal from the old startedAt).
		b.startedAt = now
	}
}

// arriveInfo carries the source identity a delivery must echo on the arrive trace
// AFTER releasing pw.mu. emit is false for a bead that carried no position stream.
type arriveInfo struct {
	emit       bool
	node, port string
	value      int
	gen        uint64 // the delivered/dropped bead's per-wire id (renderer bead key)
}

// posEmitArgs holds the arguments for a deferred tr.Position call, returned by
// advanceBeadLocked so the caller can emit off the lock.
type posEmitArgs struct {
	node, port  string
	val         int
	x, y, z, t float64
	gen         uint64
}

// emitArrive sends the traversal-complete trace for a delivered bead. Called by
// the walker AFTER releasing pw.mu (trace channel send off the lock).
func (pw *PacedWire) emitArrive(ai arriveInfo) {
	if ai.emit {
		pw.Trace.Arrive(ai.node, ai.port, ai.value, ai.gen)
	}
}

// advanceBeadLocked performs one frame's work for the in-flight bead identified by
// gen at clock reading now (the scheduled tick time). Caller must hold pw.mu on
// entry; this method always releases it before returning.
//
// Returns:
//   - emit=true if a Position trace should be sent (tr.Position) after this call;
//     pos contains the arguments.
//   - final=true if the bead has reached or passed its deadline at now, meaning the
//     caller should proceed with the FIFO-head delivery loop.
//
// If the bead is missing or the wire torn down, all zero/false values are returned
// and pw.mu is still released.
//
// NOTE: the inflight→delivered move and cond.Broadcast are NOT done here; the
// walker's FIFO-head wait loop does that after this returns when final=true. This
// keeps the two locking phases (trace-emit window vs. delivery window) unchanged.
func (pw *PacedWire) advanceBeadLocked(gen uint64, now time.Duration) (emit bool, pos posEmitArgs, final bool) {
	tr := pw.Trace
	pulseSpeed := pw.pulseSpeed

	if gen < pw.teardownGen {
		pw.mu.Unlock()
		return
	}
	i := pw.findInflightLocked(gen)
	if i < 0 {
		pw.mu.Unlock()
		return
	}
	b := pw.inflight[i]
	arc := b.arc
	seg := b.seg
	placement := b.placement
	stream := b.streams && tr != nil && arc > 0
	pw.mu.Unlock()

	deadline := placement
	if pulseSpeed > 0 {
		deadline += time.Duration(arc / pulseSpeed * float64(time.Millisecond))
	}

	target := now
	if now >= deadline {
		target = deadline
		final = true
	}

	if stream {
		covered := pulseSpeed * float64(target-placement) / float64(time.Millisecond)
		t := 0.0
		if arc > 0 {
			t = covered / arc
		}
		if t > 1 {
			t = 1
		}
		p := lerp(seg.Start, seg.End, t)
		emit = true
		pos = posEmitArgs{
			node: b.node, port: b.port, val: b.val,
			x: p.X, y: p.Y, z: p.Z, t: t, gen: b.gen,
		}
	}
	return
}


// teardownLocked cancels ALL in-flight bead walkers, clears both queues, and
// returns the per-bead source identities for any in-flight beads so the caller can
// emit one PulseCancelled per dropped STREAMING bead after unlocking (emit mirrors
// the bead's streams flag — delivery-only beads carry no sprite and emit nothing,
// matching deliverHeadLocked's emit: db.streams). Must be called with pw.mu held.
func (pw *PacedWire) teardownLocked() []arriveInfo {
	var cancelled []arriveInfo
	for i := range pw.inflight {
		b := pw.inflight[i]
		cancelled = append(cancelled, arriveInfo{emit: b.streams, node: b.node, port: b.port, value: b.val, gen: b.gen})
	}
	pw.inflight = nil
	pw.delivered = nil
	// Invalidate every outstanding driven loop at once and wake any parked WaitUntil.
	pw.teardownGen = pw.nextGen + 1
	pw.cond.Broadcast()
	return cancelled
}

// Reset drops all in-flight/delivered beads and cancels their walkers; used when
// an edge is deleted in the editor.
func (pw *PacedWire) Reset() {
	pw.mu.Lock()
	pw.teardownLocked()
	pw.mu.Unlock()
}

// Delete persistently silences the wire: sets deleted=true so the source stops
// placing beads, then tears down all in-flight beads and emits one pulse-cancelled
// trace per dropped bead (keyed by the bead's SOURCE node+port — the renderer
// removes each bead sprite). Cancel identities are captured under the lock; the
// traces are emitted after unlocking (Trace.PulseCancelled sends on a channel and
// must not hold the wire lock).
func (pw *PacedWire) Delete() {
	pw.mu.Lock()
	pw.deleted = true
	inFlightCount := len(pw.inflight)
	pw.Trace.Breadcrumb("wire_delete_drop_pulse",
		pw.Target, pw.TargetHandle,
		fmt.Sprintf("in_flight=%d delivered=%d", inFlightCount, len(pw.delivered)))
	cancelled := pw.teardownLocked()
	pw.mu.Unlock()

	for _, ai := range cancelled {
		if ai.emit {
			pw.Trace.PulseCancelled(ai.node, ai.port, ai.value, ai.gen)
		}
	}
}

// Restore clears the deleted flag set by Delete so the wire carries pulses again.
//
// In the live flow Restore only runs to un-silence a wire that is currently deleted
// (the "create" edit re-adds a previously-deleted edge), and while deleted the source
// places no beads — so Delete's teardown already drained inflight and it stays empty.
// The teardown here is therefore a no-op for beads in practice. But rather than rely on
// that invariant, Restore matches Delete: it emits one pulse-cancelled per dropped bead
// (captured under the lock, emitted after unlock — PulseCancelled sends on a channel and
// must not hold the wire lock) so a Restore that ever did race a live bead cannot orphan
// a sprite. In the normal empty case no traces are emitted, so behavior is unchanged.
func (pw *PacedWire) Restore() {
	pw.mu.Lock()
	pw.deleted = false
	cancelled := pw.teardownLocked()
	pw.mu.Unlock()

	for _, ai := range cancelled {
		if ai.emit {
			pw.Trace.PulseCancelled(ai.node, ai.port, ai.value, ai.gen)
		}
	}
}

// InFlight reports whether any bead is currently traversing this wire.
func (pw *PacedWire) InFlight() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return len(pw.inflight) > 0
}
