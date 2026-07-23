package Wiring

import (
	"context"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// wireChanBufferSize bounds PacedWire's in-channel (source -> wire) and out-channel
// (wire -> destination). Generously sized so the SOURCE's send (Send) and the WIRE's
// own delivery send (inside driveOneCycle) can never need to block (MODEL.md
// "Sending": "no back-pressure, ever" -- the send must always succeed immediately).
// A node fires at most a handful of times between two wire-drive cycles (~16ms
// apart), so this ceiling is never approached under any realistic load; if it ever
// were, that is a bug to fix at the source (firing far faster than any wire could
// ever drain), not a reason to make either send blocking.
const wireChanBufferSize = 4096

// deliveredBead is a value the wire's own goroutine has finished timing and handed
// off toward the destination over outCh. deliverTick is the tick THIS WIRE's own
// clock reading was at when the handoff happened -- the authoritative "what tick did
// this actually land on" answer (RecvTick/PollRecvTick's tick return).
type deliveredBead struct {
	val         int
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

// placeRequest is what Send hands across the wire's in-channel: a bead value plus
// the placement geometry it should be timed/drawn against. The WIRE's own
// goroutine (driveOneCycle/drainPlacements) stamps placementTick from ITS OWN
// clock reading when it drains this off inCh — the source node's tick plays no
// part (MODEL.md: "The wire goroutine reads its OWN clock copy and its own tick").
type placeRequest struct {
	val int
	bp  beadPlacement
}

// inflightBead is one bead traversing the wire. Each bead carries its own
// geometry so a mid-flight geometry edit (node-move) re-derives the remaining
// travel from the NEW arc while preserving the bead's FRACTIONAL progress t.
// Distance is NOT stored: fractional progress t = (clock.Tick() − placementTick)
// / ticksToCross is a pure function of the wire's own single clock reading
// (MODEL.md "Geometry and time").
//
// Every field here is touched by EXACTLY ONE goroutine: this wire's own (folded
// into edgeMover.run via driveOneCycle — see the PacedWire doc comment below).
// There is no lock; ownership replaces locking.
type inflightBead struct {
	val           int
	placementTick float64     // this wire's own tick reading when placed (fractional after a geometry rebase)
	arc           float64     // current arc length of this bead's edge (world units)
	seg           wireSegment // current straight-segment endpoints of this bead's edge
	node          string      // source node id — the position/cancel routing key
	port          string      // source output port — the position/cancel routing key
	streams       bool        // whether this bead carries position-stream context
	gen           uint64      // per-bead id; retained for teardownGen invalidation
	// finalPending is true once a drive cycle has advanced this bead to its
	// delivery deadline (target==deadline) but the handoff to outCh has not yet
	// succeeded (e.g. it was not yet at the FIFO head, or outCh had no room that
	// cycle). Subsequent cycles retry only the (cheap) delivery handoff for such
	// a bead, without re-running the position-advance math.
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

// PacedWire is an ACTIVE GOROUTINE (MODEL.md "The network"), not a passive
// struct: a channel in from its source node, a channel out to its destination
// node. It is NOT a separate goroutine of its own — it is driven by the same
// per-edge goroutine that already existed to revise in-flight geometry on a
// node-move (edgeMover.run, node_mover.go), now given ownership of the beads it
// used to reach across a lock to revise. Goroutine count is therefore
// unchanged; what changed is who owns the state.
//
//   - inCh is the wire's IN-CHANNEL: the source node's own goroutine calls Send,
//     a non-blocking buffered-channel send, and moves on (MODEL.md "Sending" — no
//     back-pressure, ever).
//   - outCh is the wire's OUT-CHANNEL: the destination node's own goroutine calls
//     RecvTick/Recv, a non-blocking buffered-channel receive.
//   - inflight/nextGen/teardownGen/pulseSpeed are owned EXCLUSIVELY by the wire's
//     own goroutine (driveOneCycle, called every cycle from edgeMover.run) — no
//     lock guards them; there is exactly one writer and one reader (the same
//     goroutine). Do not reintroduce a mutex here "for safety": a lock on top of
//     single-goroutine ownership is dead weight, and if a second goroutine ever
//     needs to touch this state again, that is a sign the ownership model broke,
//     not a reason to add one back.
type PacedWire struct {
	inCh  chan placeRequest
	outCh chan deliveredBead

	// Owned exclusively by this wire's own goroutine (driveOneCycle and its
	// helpers, and ReviseInFlightGeometry — both called only from edgeMover.run,
	// which IS this wire's goroutine). No mu.
	inflight []inflightBead
	// nextGen mints a unique id for each placed bead and is also bumped on
	// teardown to invalidate ALL outstanding beads at once.
	nextGen uint64
	// teardownGen: a bead whose gen is < teardownGen is invalidated wholesale.
	// No current caller bumps it above 0 (kept because drainPlacements/stepAll
	// still gate on it for future teardown wiring).
	teardownGen uint64
	pulseSpeed  float64

	Target       string   // destination node id — authoritative slot identity
	TargetHandle string   // destination input-port name — authoritative slot identity
	Trace        *T.Trace // injected by loader; used for breadcrumb diagnostics only

	// snap is an atomically-published, READ-ONLY snapshot of every in-flight
	// bead's current segment (FIFO order), republished by driveOneCycle every
	// cycle. It exists so a non-owning goroutine — test-only inspection
	// (InFlightSegments) — can observe this wire's in-flight geometry without
	// racing the owning goroutine's live inflight slice. Production code never
	// reads it: per MODEL.md §Sending, nothing queries wire occupancy.
	snap atomic.Pointer[[]wireSegment]

	// pending buffers this wire's OWN Position/Arrive events since the last drain
	// (memory/feedback_no_single_writer_bridge.md): appended only by stepAll (this
	// wire's own goroutine, via edgeMover.run's DriveOneCycle call) and drained only by
	// edgeMover.writeStreamFrame — the SAME goroutine on both ends (edgeMover.run calls
	// DriveOneCycle then writeStreamFrame back to back, every cycle), so no lock.
	pending []pendingWireEvent
}

// pendingWireEvent is one raw Position/Arrive tuple recorded by stepAll, awaiting
// row-resolution + packing by edgeMover.writeStreamFrame (drainPendingEvents).
type pendingWireEvent struct {
	kind       string
	value      int
	x, y, z, t float64
	gen        uint64
}

// drainPendingEvents returns every pendingWireEvent recorded since the last call and
// clears the buffer. Safe to call only from this wire's own goroutine.
func (pw *PacedWire) drainPendingEvents() []pendingWireEvent {
	if len(pw.pending) == 0 {
		return nil
	}
	out := pw.pending
	pw.pending = nil
	return out
}

// NewPacedWire creates an empty PacedWire with its in/out channels ready. arcLength
// is the straight-line distance between source and target (world units); pulseSpeed
// is in world-units per TICK (use PulseSpeedWuPerTick).
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
	return &PacedWire{
		pulseSpeed: pulseSpeed,
		inCh:       make(chan placeRequest, wireChanBufferSize),
		outCh:      make(chan deliveredBead, wireChanBufferSize),
	}
}

// msToArcWu names the ms→world-units conversion used to reconstruct a bead's
// travelled arc from a reported InFlightMs latency (Send below).
// It is PulseSpeedWuPerMs under a name that documents what the multiplication
// means at that call site, without changing the value.
const msToArcWu = PulseSpeedWuPerMs

// SendOutcome distinguishes WHY Send did not place a bead, so a caller cannot
// accidentally treat "buffer momentarily full" (transient — never exit on this)
// the same as a genuinely terminal condition. There is currently only one
// non-terminal failure mode (inCh full); SendOutcome exists as a TYPE so that
// if a real terminal wire-teardown path is ever reintroduced, it lands as a
// third, distinct constant rather than silently widening SendBufferFull's
// meaning.
type SendOutcome uint8

const (
	// SendPlaced: the bead was enqueued onto inCh.
	SendPlaced SendOutcome = iota
	// SendBufferFull: inCh's buffer (wireChanBufferSize) was full at send time.
	// TRANSIENT, NOT TERMINAL — the wire's own goroutine drains inCh every
	// cycle, so room reappears almost immediately. A caller must NOT exit its
	// drive loop on this outcome; it should skip this cycle's placement and
	// retry on the next one. See the wireChanBufferSize doc comment: this
	// should never occur under realistic load, but if it does, the source
	// keeps running rather than silently losing its drive goroutine forever.
	SendBufferFull
)

// Send enqueues one bead placement onto this wire's IN-CHANNEL from the SOURCE
// node's own goroutine (Out.placeDrivenNoWalkerAt). Non-blocking by construction:
// the buffered channel means this call always succeeds immediately under any
// realistic load, so the source never waits on the wire or the destination
// (MODEL.md "Sending" — no back-pressure, ever). Returns SendBufferFull only if
// the (generously sized) buffer is somehow already full — a condition that would
// itself indicate a bug elsewhere (a source firing far faster than any wire could
// ever drain), never ordinary traffic. Emits ONE breadcrumb per occurrence (this
// should never fire; it is a control-event signal, not a per-tick firehose) —
// see CLAUDE.md's debug-breadcrumb section.
func (pw *PacedWire) Send(v int, bp beadPlacement) SendOutcome {
	select {
	case pw.inCh <- placeRequest{val: v, bp: bp}:
		return SendPlaced
	default:
		if pw.Trace != nil {
			pw.Trace.Breadcrumb("wire-send-buffer-full", pw.Target, pw.TargetHandle, "")
		}
		return SendBufferFull
	}
}

// RecvTick is the non-blocking receive used by windowed nodes (In.PollRecv) on the
// DESTINATION node's own goroutine. Returns immediately: ok=false when the wire's
// own goroutine has not yet handed a delivered bead onto outCh. Also returns the
// tick the delivered bead actually landed on (deliveredBead.deliverTick, stamped
// by the wire's own goroutine at handoff time) — callers proving same-tick fan-out
// delivery must use this instead of re-reading a live clock after the fact.
func (pw *PacedWire) RecvTick() (int, int64, bool) {
	select {
	case db := <-pw.outCh:
		return db.val, db.deliverTick, true
	default:
		return 0, 0, false
	}
}

// Recv is RecvTick without the tick. Consumes on read (no separate Done step).
func (pw *PacedWire) Recv() (int, bool) {
	v, _, ok := pw.RecvTick()
	return v, ok
}

// DriveOneCycle is this wire's own single per-cycle unit of work: drain newly
// Send-ed beads off inCh (stamping their placementTick from THIS call's tick —
// the wire's own clock reading), advance every in-flight bead due at tick by one
// position-step (emitting Position traces), and hand off any bead that has
// reached its delivery deadline onto outCh (non-blocking — a destination that
// hasn't drained yet simply leaves the bead retried next cycle, still at the
// FIFO head).
//
// In production this is called ONLY by edgeMover.run (node_mover.go), once per
// cycle, on that goroutine's OWN clock tick — the wire's goroutine IS the edge's
// goroutine (MODEL.md "The network"). It is exported so tests that build a bare
// PacedWire directly (no full loader topology, hence no edgeMover to drive it)
// can spawn their own driving goroutine that mimics production's per-cycle drive,
// exactly as StepOnceAt's callers used to.
func (pw *PacedWire) DriveOneCycle(ctx context.Context, tick int64) {
	if ctx.Err() != nil {
		return
	}
	pw.drainPlacements(tick)
	pw.stepAll(tick)
	pw.publishSnap()
}

// publishSnap republishes the atomic in-flight-segment snapshot (see the snap
// field doc). Called only by this wire's own goroutine, once per DriveOneCycle.
func (pw *PacedWire) publishSnap() {
	segs := make([]wireSegment, len(pw.inflight))
	for i, b := range pw.inflight {
		segs[i] = b.seg
	}
	pw.snap.Store(&segs)
}

// InFlightSegments returns a snapshot of every in-flight bead's current segment,
// in FIFO order. Safe to call from ANY goroutine (reads the atomically-published
// snapshot, never this wire's live inflight slice). This is a read-only debug/test
// inspection point only — production never queries wire occupancy (MODEL.md
// §Sending).
func (pw *PacedWire) InFlightSegments() []wireSegment {
	if s := pw.snap.Load(); s != nil {
		return *s
	}
	return nil
}

// drainPlacements pops every placement currently queued on inCh (non-blocking)
// and appends each as a fresh in-flight bead, stamping placementTick from tick —
// THIS wire's own clock reading, taken once by the caller (driveOneCycle) per
// cycle. Called only by this wire's own goroutine.
func (pw *PacedWire) drainPlacements(tick int64) {
	for {
		select {
		case req := <-pw.inCh:
			pw.nextGen++
			if pw.nextGen < pw.teardownGen {
				pw.nextGen = pw.teardownGen
			}
			pw.inflight = append(pw.inflight, inflightBead{
				val:           req.val,
				placementTick: float64(tick),
				// arc (world units) is reconstructed from the reported ms latency via
				// the FIXED ms→wu conversion (msToArcWu), so it is independent of the
				// clock's tick speed.
				arc:     req.bp.InFlightMs * msToArcWu,
				seg:     wireSegment{Start: req.bp.Start, End: req.bp.End},
				node:    req.bp.Node,
				port:    req.bp.Port,
				streams: req.bp.streams(),
				gen:     pw.nextGen,
			})
		default:
			return
		}
	}
}

// stepAll advances every in-flight bead due at tick by one position-step and
// attempts FIFO-head delivery for any that have reached their deadline. It
// processes beads head-first so an earlier bead's delivery in this same call can
// unblock a later bead's delivery within the same cycle — the same shape the old
// per-call gens-snapshot loop had, without a lock since only this wire's own
// goroutine ever calls it.
func (pw *PacedWire) stepAll(tick int64) {
	nowTick := float64(tick)
	for i := 0; i < len(pw.inflight); {
		b := &pw.inflight[i]
		if b.gen < pw.teardownGen {
			pw.inflight = append(pw.inflight[:i], pw.inflight[i+1:]...)
			continue
		}
		if !b.finalPending {
			if nowTick <= b.placementTick {
				i++
				continue
			}
			emit, pos, final := pw.advanceBead(b, nowTick)
			if emit {
				pw.pending = append(pw.pending, pendingWireEvent{
					kind: T.KindPosition, value: pos.val,
					x: pos.x, y: pos.y, z: pos.z, t: pos.t, gen: pos.gen,
				})
			}
			if !final {
				i++
				continue
			}
			b.finalPending = true
		}
		// Only the FIFO head (i==0) can deliver; a non-head bead simply waits.
		if i != 0 {
			i++
			continue
		}
		select {
		case pw.outCh <- deliveredBead{val: b.val, deliverTick: tick}:
			pw.emitArrive(arriveInfo{emit: b.streams, node: b.node, port: b.port, value: b.val, gen: b.gen})
			pw.inflight = pw.inflight[1:]
			// Do not advance i: the slice shifted, so index 0 is now the next bead.
		default:
			// Destination hasn't drained outCh yet; retry the handoff next cycle.
			i++
		}
	}
}

// LiveBeadRow is one in-flight bead's CURRENT world position + value + id, computed with
// the same lerp math advanceBead uses but with NO side effects — no trace emit, no state
// mutation. Used only by the dedicated per-edge stream (edgeMover.writeStreamFrame,
// node_mover.go) to snapshot this wire's current beads without duplicating tr.Position's
// separate accumulation into Buffer.SnapshotState.
type LiveBeadRow struct {
	Val     int
	X, Y, Z float64
	Gen     uint64
}

// LiveBeadRows returns every in-flight, position-streaming bead's CURRENT world position
// at tick (this wire's own goroutine's clock reading), in FIFO order. Safe to call ONLY
// from this wire's own goroutine (reads pw.inflight directly, no lock — same single-
// goroutine-ownership contract stepAll/ReviseInFlightGeometry rely on). A bead with no
// position stream (bp.streams()==false) is omitted, matching advanceBead's own emit gate.
func (pw *PacedWire) LiveBeadRows(tick int64) []LiveBeadRow {
	nowTick := float64(tick)
	rows := make([]LiveBeadRow, 0, len(pw.inflight))
	for i := range pw.inflight {
		b := &pw.inflight[i]
		if !b.streams {
			continue
		}
		crossTicks := pw.ticksToCross(b.arc)
		target := nowTick
		if crossTicks > 0 && nowTick >= b.placementTick+crossTicks {
			target = b.placementTick + crossTicks
		}
		t := 0.0
		if crossTicks > 0 {
			t = (target - b.placementTick) / crossTicks
		}
		if t < 0 {
			t = 0
		}
		if t > 1 {
			t = 1
		}
		p := lerp(b.seg.Start, b.seg.End, t)
		rows = append(rows, LiveBeadRow{Val: b.val, X: p.X, Y: p.Y, Z: p.Z, Gen: b.gen})
	}
	return rows
}

// ReviseInFlightGeometry re-derives EVERY in-flight bead's remaining travel after a
// geometry edit (node-move) changed the edge (MODEL.md "Geometry and time"). It
// preserves each bead's FRACTIONAL progress t (its proportion along the wire), NOT
// the absolute distance covered: each bead stays at the same fraction t and the
// remaining time is recomputed from the NEW arc so UNIFORM PULSE SPEED holds —
// remaining = (1−t)·newArc/pulseSpeed. driveOneCycle re-reads each bead's live
// arc/seg every cycle, so the new geometry takes effect without any relaunch.
// No-op when no bead is in flight.
//
// Called only by this wire's own goroutine (edgeMover.recomputeGeometry, itself
// running on the SAME goroutine as DriveOneCycle — see the PacedWire doc comment):
// tick is that goroutine's own clock reading, taken once per call. There is no
// second clock copy involved anymore, so the two-copy skew the old
// caller-pinned-tick contract had to tolerate cannot arise here.
func (pw *PacedWire) ReviseInFlightGeometry(tick int64, newArc float64, newSeg wireSegment) {
	if len(pw.inflight) == 0 {
		return
	}
	nowTick := float64(tick)
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

// arriveInfo carries the source identity a delivery must echo on the arrive trace.
// emit is false for a bead that carried no position stream.
type arriveInfo struct {
	emit       bool
	node, port string
	value      int
	gen        uint64 // the delivered bead's per-wire id (renderer bead key)
}

// posEmitArgs holds the arguments for a tr.Position call, returned by
// advanceBead so the caller can emit it.
type posEmitArgs struct {
	node, port string
	val        int
	x, y, z, t float64
	gen        uint64
}

// emitArrive sends the traversal-complete trace for a delivered bead. Called by
// this wire's own goroutine (stepAll) right after the outCh handoff succeeds.
func (pw *PacedWire) emitArrive(ai arriveInfo) {
	if ai.emit {
		pw.pending = append(pw.pending, pendingWireEvent{kind: T.KindArrive, value: ai.value, gen: ai.gen})
	}
}

// advanceBead performs one cycle's work for the in-flight bead b at clock
// reading now (the scheduled tick time). Called only by this wire's own
// goroutine (stepAll).
//
// Returns:
//   - emit=true if a Position trace should be sent (tr.Position) for this call;
//     pos contains the arguments.
//   - final=true if the bead has reached or passed its deadline at now, meaning
//     the caller should attempt the FIFO-head delivery handoff.
func (pw *PacedWire) advanceBead(b *inflightBead, nowTick float64) (emit bool, pos posEmitArgs, final bool) {
	tr := pw.Trace

	arc := b.arc
	seg := b.seg
	placementTick := b.placementTick
	stream := b.streams && tr != nil && arc > 0
	crossTicks := pw.ticksToCross(arc)

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
