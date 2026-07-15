package Wiring

import (
	"context"
	"fmt"
	"sync"

	T "github.com/dtauraso/wirefold/Trace"
)

// deliveredBead is a value that has arrived at the wire's destination and is waiting
// to be read by PollRecv, which consumes on read (no separate Done).
type deliveredBead struct {
	val int
	// deliverTick is the PINNED tick the caller was actually stepping when this
	// bead was moved from inflight to delivered (tryDeliverHeadLocked receives
	// it as nowTick — StepOnceAt's caller-pinned tick for a fan-out cycle, or
	// driveAdvanceItem's per-step target for the blocking path). It is the
	// authoritative "what tick did this actually land on" answer. It is
	// deliberately NOT a live read of pw.clock.Tick(): the shared clock can be
	// advanced again by another goroutine between this wire's step and a
	// sibling wire's step in the same fan-out cycle, so re-reading "now" from
	// the clock at delivery time can record a LATER tick than the one the
	// caller actually pinned and delivered against.
	deliverTick int64
}

// PulseSpeedWuPerMs aliases CurveParamPulseSpeedWuPerMs. It is retained as the
// fixed world-units-per-MILLISECOND conversion for the SimLatencyMs REPORTING
// path (the ms value emitted on the send trace); it is NOT the clock's unit.
// The canonical value lives in curve_params.go so the codegen tool can export it
// to TS.
const PulseSpeedWuPerMs = CurveParamPulseSpeedWuPerMs

// PulseSpeedWuPerTick is the uniform pulse speed reinterpreted in world-units per
// TICK (MODEL.md: pulseSpeed is world-units-per-tick). It is the ms speed scaled
// by the tick period: 0.04 wu/ms × 16 ms/tick = 0.64 wu/tick. This is what the
// human-speed clock uses to derive ticksToCross = arcLength / PulseSpeedWuPerTick,
// which equals the retired arc/pulseSpeedMs/16 sample count — so a bead visits the
// same number of positions in the same wall time.
const PulseSpeedWuPerTick = PulseSpeedWuPerMs * MsPerTick

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
// Distance is NOT stored: fractional progress t = (clock.Tick() − placementTick)
// / ticksToCross is a pure function of the one clock (MODEL.md "Geometry and time").
// All fields are guarded by pw.mu.
type inflightBead struct {
	val           int
	placementTick float64     // clock tick reading when placed (fractional after a geometry rebase)
	arc           float64     // current arc length of this bead's edge (world units)
	seg           wireSegment // current straight-segment endpoints of this bead's edge
	node          string      // source node id — the position/cancel routing key
	port          string      // source output port — the position/cancel routing key
	streams       bool        // whether this bead carries position-stream context
	gen           uint64      // per-bead id; the driven loop self-cancels on gen mismatch (teardown)
	// finalPending is true once StepOnce has advanced this bead to its delivery
	// deadline (target==deadline) but it was not yet at the FIFO head, so the
	// move to `delivered` is still outstanding. StepOnce retries only the
	// (cheap) delivery handoff for such a bead on subsequent calls.
	finalPending bool
}

// ticksToCross returns the tick count for a bead of the given arc length to cross
// at the uniform pulse speed: arcLength / PulseSpeedWuPerTick (MODEL.md). Fractional;
// the driver delivers on the first integer tick at or past placementTick + this.
func (pw *PacedWire) ticksToCross(arc float64) float64 {
	if pw.pulseSpeed <= 0 {
		return 0
	}
	return arc / pw.pulseSpeed
}

// PacedWire is a multi-bead FIFO transport. Beads are placed via placeBeadNoWalker
// and delivered by the owning node's goroutine driving per-cycle StepOnce — no
// per-bead goroutine. The source never waits on the destination; each bead is placed
// immediately and driven to the `delivered` FIFO on the caller's goroutine at its own
// deadline. Recv pops `delivered` in send order and CONSUMES on read (no separate
// Done step).
//
// Clock-driven delivery: the wire times its own delivery on the one human-speed
// clock (MODEL.md), sleep-only — the driven loop paces itself with
// clock.SleepCycle rather than blocking on a target tick. When a bead is placed,
// the wire records the placement tick; the driven loop sleeps cycle by cycle
// until placementTick + ticksToCross is reached, then moves the bead from
// `inflight` to `delivered`. There is no TS "delivered" signal and no central
// scheduler — every wire reads the same clock independently. Pause freezes the
// tick (the clock does not advance while halted); Reset/Delete bump teardownGen
// so the driven loop drops the bead.
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
	// every edge feeding this destination port. Kept in step by SetIncomingLatency.
	MaxIncomingSimLatencyMs float64
	// incomingLatency tracks each feeding edge's own SimLatencyMs (edgeId → latency).
	incomingLatency map[string]float64
	Target          string   // destination node id — authoritative slot identity
	TargetHandle    string   // destination input-port name — authoritative slot identity
	Trace           *T.Trace // injected by loader; used for breadcrumb diagnostics only
}

// NewPacedWire creates an empty PacedWire. arcLength is the straight-line
// distance between source and target (world units); pulseSpeed is in world-units
// per TICK (use PulseSpeedWuPerTick). MaxIncomingSimLatencyMs stays in ms (the
// reporting unit) so it is derived from the fixed ms conversion, independent of
// the clock's tick speed.
//
// PULSE SPEED IS UNIFORM ACROSS ALL WIRES — per-wire speed is rejected doctrine, and the
// TS layer cannot even express it (no speed prop in WireProps). The pulseSpeed PARAMETER
// survives only as a TEST affordance: the lean per-node tests pass PulseSpeedWuPerMs so
// that ticksToCross falls out as latMs. What keeps production uniform is that there is
// exactly ONE non-test call site (loader.go), passing PulseSpeedWuPerTick.
//
// That one-call-site invariant is enforced by tools/check-uniform-pulse-speed.sh. Do not
// add a second production caller: it converts "uniform" from structural to conventional.
// If production ever needs to build a wire elsewhere, drop this parameter instead and let
// the tests express arc as ticks*PulseSpeedWuPerTick.
func NewPacedWire(arcLength float64, pulseSpeed float64) *PacedWire {
	pw := &PacedWire{
		MaxIncomingSimLatencyMs: arcLength / PulseSpeedWuPerMs,
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

// PlaceDeliverOnly places a delivery-only bead (no position stream) WITHOUT
// spawning a walker goroutine. Delivery is driven by the caller's subsequent
// per-cycle StepOnce/StepOnceAt calls. Returns false if the wire is
// faded/deleted (nothing placed). Cross-package tests that only exercise
// delivery timing use this (see gatetesthelper.Send).
func (pw *PacedWire) PlaceDeliverOnly(value any, inFlightMs float64) bool {
	_, ok := pw.placeBeadNoWalker(value, beadPlacement{InFlightMs: inFlightMs})
	return ok
}

// msToArcWu names the ms→world-units conversion used to reconstruct a bead's
// travelled arc from a reported InFlightMs latency (placeBeadNoWalker below).
// It is PulseSpeedWuPerMs under a name that documents what the multiplication
// means at that call site, without changing the value.
const msToArcWu = PulseSpeedWuPerMs

// placeBeadNoWalker appends a bead WITHOUT launching a walker goroutine,
// returning the bead's gen so the caller can drive delivery synchronously.
// Returns (0, false) when faded/deleted (nothing placed).
func (pw *PacedWire) placeBeadNoWalker(value any, bp beadPlacement) (gen uint64, ok bool) {
	return pw.placeBeadNoWalkerAt(value, bp, pw.clock.Tick())
}

// placeBeadNoWalkerAt is placeBeadNoWalker with the current tick PINNED by the
// caller instead of re-reading pw.clock.Tick() live. Use when placing several
// beads across different wires in the same fan-out cycle: the shared clock
// can advance mid-cycle between placements, so each wire must stamp
// placementTick from ONE snapshot taken once per cycle, not one live read
// per wire — otherwise fan-out siblings placed on either side of a tick
// boundary get different placementTicks and deliver a cycle apart despite
// equal latency.
func (pw *PacedWire) placeBeadNoWalkerAt(value any, bp beadPlacement, tick int64) (gen uint64, ok bool) {
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
	nowTick := float64(tick)
	b := inflightBead{
		val:           beadVal,
		placementTick: nowTick,
		// arc (world units) is reconstructed from the reported ms latency via the
		// FIXED ms→wu conversion (msToArcWu), so it is independent of the clock's
		// tick speed.
		arc:     bp.InFlightMs * msToArcWu,
		seg:     wireSegment{Start: bp.Start, End: bp.End},
		node:    bp.Node,
		port:    bp.Port,
		streams: bp.streams(),
		gen:     pw.nextGen,
	}
	pw.inflight = append(pw.inflight, b)
	pw.cond.Broadcast()
	gen = b.gen
	pw.mu.Unlock()
	return gen, true
}

// tryDeliverHeadLocked attempts, ONE TIME (no parking/looping), to deliver the bead
// identified by gen. Caller must hold pw.mu on entry.
//
// Three outcomes:
//   - ready=true, ok=true: the bead was at the FIFO head and was moved to
//     `delivered`; ai carries its source identity for the arrive trace. pw.mu is
//     released.
//   - ready=true, ok=false: the bead is gone (ctx canceled, torn down, or already
//     dropped) — no delivery, nothing to wait for. pw.mu is released.
//   - ready=false: the bead is still live but is NOT yet at the FIFO head (an
//     earlier bead must deliver first). pw.mu is left HELD so a blocking caller
//     can park on pw.cond, or a non-blocking caller can unlock and retry later.
//
// This is the (never-parking) core StepOnce retries every cycle for a bead that
// is not yet at the FIFO head.
func (pw *PacedWire) tryDeliverHeadLocked(ctx context.Context, gen uint64, nowTick int64) (ai arriveInfo, ok bool, ready bool) {
	if ctx.Err() != nil {
		pw.mu.Unlock()
		return arriveInfo{}, false, true
	}
	if gen < pw.teardownGen {
		pw.mu.Unlock()
		return arriveInfo{}, false, true
	}
	j := pw.findInflightLocked(gen)
	if j < 0 {
		pw.mu.Unlock()
		return arriveInfo{}, false, true
	}
	if j != 0 {
		return arriveInfo{}, false, false
	}
	db := pw.inflight[0]
	pw.inflight = pw.inflight[1:]
	pw.delivered = append(pw.delivered, deliveredBead{val: db.val, deliverTick: nowTick})
	pw.cond.Broadcast()
	ai = arriveInfo{emit: db.streams, node: db.node, port: db.port, value: db.val, gen: db.gen}
	pw.mu.Unlock()
	return ai, true, true
}

// StepOnce is the NON-BLOCKING one-tick step primitive: the only delivery path
// (MODEL.md "The model is sleep-only"). It advances every in-flight bead
// on this wire that is due at the CURRENT clock tick by exactly one
// position-step, attempts any FIFO-head delivery that is now ready, and
// RETURNS IMMEDIATELY — it never loops over future ticks and never parks
// (no cond.Wait) on a bead that is not yet due or not yet at the FIFO head;
// such a bead simply stays in-flight for a future StepOnce call.
//
// Calling StepOnce exactly once per tick (paced by the caller's SleepCycle)
// for N ticks delivers each bead on the tick its deadline is reached.
//
// A bead that reaches its delivery deadline while not yet at the FIFO head is
// marked finalPending so subsequent StepOnce calls retry ONLY the (cheap)
// delivery handoff for it, without re-running the position-advance math.
func (pw *PacedWire) StepOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	pw.StepOnceAt(ctx, pw.clock.Tick())
}

// StepOnceAt is StepOnce but with the current tick PINNED by the caller
// instead of read from the shared clock inside this call. Use this when
// stepping more than one wire per logical cycle (fan-out/fan-in) so all
// wires observe the SAME tick even if the shared clock advances between
// individual StepOnce calls — snapshot clk.Tick() once per cycle and pass
// it to every wire's StepOnceAt. Single-wire-per-cycle callers can keep
// using plain StepOnce.
func (pw *PacedWire) StepOnceAt(ctx context.Context, tick int64) {
	if ctx.Err() != nil {
		return
	}

	// Snapshot the FIFO order of currently in-flight beads. Iterate that fixed
	// order — head-first — so an earlier bead's delivery this same call can
	// unblock a later bead's delivery within the same StepOnce.
	pw.mu.Lock()
	gens := make([]uint64, len(pw.inflight))
	for i, b := range pw.inflight {
		gens[i] = b.gen
	}
	pw.mu.Unlock()

	nowTick := float64(tick)

	for _, gen := range gens {
		if ctx.Err() != nil {
			return
		}

		pw.mu.Lock()
		idx := pw.findInflightLocked(gen)
		if idx < 0 {
			pw.mu.Unlock()
			continue
		}
		b := pw.inflight[idx]
		alreadyFinal := b.finalPending
		placementTick := b.placementTick
		pw.mu.Unlock()

		if !alreadyFinal {
			if nowTick <= placementTick {
				// Not due yet: no step this tick, no delivery attempt.
				continue
			}
			pw.mu.Lock()
			emit, posArgs, final := pw.advanceBeadLocked(gen, nowTick) // unlocks internally
			if emit {
				pw.Trace.Position(posArgs.node, posArgs.port, posArgs.val, posArgs.x, posArgs.y, posArgs.z, posArgs.t, posArgs.gen)
			}
			if !final {
				continue
			}
			pw.mu.Lock()
			if fi := pw.findInflightLocked(gen); fi >= 0 {
				pw.inflight[fi].finalPending = true
			}
			pw.mu.Unlock()
		}

		// Bead has reached its delivery deadline; try the non-blocking FIFO-head
		// handoff. If it is not yet head, tryDeliverHeadLocked leaves it in-flight
		// (still finalPending) for a later StepOnce call to retry.
		pw.mu.Lock()
		ai, ok, ready := pw.tryDeliverHeadLocked(ctx, gen, tick)
		if !ready {
			pw.mu.Unlock()
			continue
		}
		if ok {
			pw.emitArrive(ai)
		}
	}
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

// PollRecv is the non-blocking variant of the former Recv. It pops and returns the front
// delivered value if one is present, else (nil, false). Like Recv, PollRecv
// CONSUMES on read.
func (pw *PacedWire) PollRecv() (any, bool) {
	v, _, ok := pw.PollRecvTick()
	return v, ok
}

// PollRecvTick is PollRecv but also returns the tick the delivered bead
// actually landed on (deliveredBead.deliverTick, captured under pw.mu at
// delivery time by tryDeliverHeadLocked). Tests proving same-tick fan-out
// delivery must use this instead of re-reading the shared clock after the
// fact — the clock can be advanced again before the caller is scheduled,
// which makes a caller-side re-read of "now" a different (later) tick than
// the one the bead actually delivered on.
func (pw *PacedWire) PollRecvTick() (any, int64, bool) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if len(pw.delivered) > 0 {
		b := pw.delivered[0]
		pw.delivered = pw.delivered[1:]
		return b.val, b.deliverTick, true
	}
	return nil, 0, false
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
	nowTick := float64(pw.clock.Tick())
	for i := range pw.inflight {
		b := &pw.inflight[i]
		t := 0.0
		if b.arc > 0 && pw.pulseSpeed > 0 {
			// elapsed ticks / old ticksToCross = fraction covered.
			t = (nowTick - b.placementTick) / (b.arc / pw.pulseSpeed)
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
		}
		b.arc = newArc
		b.seg = newSeg
		// Rebase placementTick so elapsed-since-placement maps to the same fraction t
		// on the NEW arc: remainingTicks = (1−t)·newArc/pulseSpeed, so the covered part
		// is t·newArc/pulseSpeed ticks ⇒ placementTick' = nowTick − t·(newArc/pulseSpeed).
		if pw.pulseSpeed > 0 {
			b.placementTick = nowTick - t*(newArc/pw.pulseSpeed)
		}
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
	node, port string
	val        int
	x, y, z, t float64
	gen        uint64
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
func (pw *PacedWire) advanceBeadLocked(gen uint64, nowTick float64) (emit bool, pos posEmitArgs, final bool) {
	tr := pw.Trace

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
	placementTick := b.placementTick
	stream := b.streams && tr != nil && arc > 0
	crossTicks := pw.ticksToCross(arc)
	pw.mu.Unlock()

	deadline := placementTick + crossTicks

	target := nowTick
	if nowTick >= deadline {
		target = deadline
		final = true
	}

	if stream {
		// fractional progress t = elapsed ticks / ticksToCross (== distance
		// covered / arc, since both scale by the uniform pulse speed).
		t := 0.0
		if crossTicks > 0 {
			t = (target - placementTick) / crossTicks
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
// matching tryDeliverHeadLocked's emit: db.streams). Must be called with pw.mu held.
func (pw *PacedWire) teardownLocked() []arriveInfo {
	var cancelled []arriveInfo
	for i := range pw.inflight {
		b := pw.inflight[i]
		cancelled = append(cancelled, arriveInfo{emit: b.streams, node: b.node, port: b.port, value: b.val, gen: b.gen})
	}
	pw.inflight = nil
	pw.delivered = nil
	// Invalidate every outstanding driven loop at once and wake any parked WaitTick.
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
