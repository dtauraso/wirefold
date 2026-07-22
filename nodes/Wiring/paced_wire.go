package Wiring

import (
	"context"
	"fmt"

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
// Every field here is touched by EXACTLY ONE goroutine: this wire's own (PacedWire.run
// and its DriveOneCycle/reviseEdge helpers — see the PacedWire doc comment below).
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

// PacedWire is an ACTIVE GOROUTINE (MODEL.md "The network"), not a passive struct: a
// channel in from its source node, a channel out to its destination node, and its OWN
// goroutine (run, launched once per unique wire by MoveDispatch.Start) that times bead
// traversal on its own clock.
//
// It owns its own goroutine BECAUSE a destination input port is one wire that several
// source edges can fan into. An earlier design had the wire driven by an incident edge's
// goroutine (edgeMover.run) to keep goroutine count unchanged — correct for a 1:1
// edge:wire, but for fan-in it meant every incident edge's goroutine drove and revised the
// ONE shared wire, N goroutines racing one pw.inflight (and clobbering each other's
// per-edge geometry). Giving the wire its own goroutine restores single-owner: edges only
// POST geometry revisions over reviseCh; only run() ever touches inflight.
//
//   - inCh is the wire's IN-CHANNEL: the source node's own goroutine calls Send,
//     a non-blocking buffered-channel send, and moves on (MODEL.md "Sending" — no
//     back-pressure, ever).
//   - outCh is the wire's OUT-CHANNEL: the destination node's own goroutine calls
//     RecvTick/Recv, a non-blocking buffered-channel receive.
//   - reviseCh is the wire's REVISE-CHANNEL: an incident edge's own goroutine posts a
//     reviseReq (its new per-edge arc/segment) when an endpoint moves; run() applies it.
//   - inflight/nextGen/teardownGen/pulseSpeed are owned EXCLUSIVELY by the wire's
//     own goroutine (run and its DriveOneCycle/reviseEdge helpers) — no lock guards
//     them; there is exactly one writer and one reader (the same goroutine). Do not
//     reintroduce a mutex here "for safety": a lock on top of single-goroutine ownership
//     is dead weight, and if a second goroutine ever needs to touch this state again,
//     that is a sign the ownership model broke, not a reason to add one back.
type PacedWire struct {
	inCh  chan placeRequest
	outCh chan deliveredBead

	// Owned exclusively by this wire's own goroutine (PacedWire.run and its
	// DriveOneCycle/reviseEdge helpers). No mu.
	inflight []inflightBead
	// nextGen mints a unique id for each placed bead and is also bumped on
	// teardown to invalidate ALL outstanding beads at once.
	nextGen uint64
	// teardownGen: a bead whose gen is < teardownGen is invalidated wholesale.
	// No current caller bumps it above 0 (kept because drainPlacements/stepAll
	// still gate on it for future teardown wiring).
	teardownGen uint64
	pulseSpeed  float64

	// clockSrc/clk: this wire's OWN clock. run() copies clockSrc once at goroutine
	// start into clk and reads only clk thereafter — the same per-goroutine-clock
	// pattern nodeMover/edgeMover use. nil clockSrc in bare test construction (no
	// loader): those tests drive the wire manually via DriveOneCycle from their own
	// goroutine and never launch run(), so clk stays the newNodeMover-style default.
	clockSrc Clock
	clk      Clock
	// speedCh delivers a global playback-speed change to THIS wire's own clk copy,
	// polled each run() cycle. The wire is a clock-owning goroutine, so it gets its
	// own speed sink from the loader's build-wide accumulator (buildMoveDispatch);
	// nil in bare test construction (never selected).
	speedCh chan float64
	// reviseCh is how the incident edge's own goroutine (edgeMover.recomputeGeometry)
	// hands this wire a geometry revision WITHOUT touching pw.inflight itself. The edge
	// computes its new arc/segment and posts a reviseReq; run() drains it and applies
	// ReviseInFlightGeometry on THIS wire's own goroutine, so inflight has exactly one
	// owner (the wire's goroutine, not the edge's). Keeping the revise off the edge's
	// goroutine is what lets the wire own its goroutine cleanly; it was also load-bearing
	// under the removed fan-in model, where a shared wire had several edge goroutines.
	reviseCh chan reviseReq

	Target       string   // destination node id — authoritative slot identity
	TargetHandle string   // destination input-port name — authoritative slot identity
	Trace        *T.Trace // injected by loader; used for breadcrumb diagnostics only
}

// reviseReq is an edge's geometry revision, handed to its dest wire's own goroutine over
// reviseCh: the new arc length and straight segment. It carries NO source identity: fan-in
// is removed from the model (validateNoFanIn), so a wire has exactly one incident edge and
// every in-flight bead on it is that edge's — the revision applies to all of them.
type reviseReq struct {
	arc float64
	seg wireSegment
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
	// pulseSpeed is a uniform POSITIVE constant by model (MODEL.md: world-units-per-tick,
	// same for every wire). A non-positive value is a construction-time misconfiguration,
	// not a runtime condition to degrade around: ticksToCross and ReviseInFlightGeometry
	// both silently no-op the travel math when pulseSpeed<=0 (deadline collapses to the
	// placement tick), which would teleport every bead to instant delivery instead of
	// pacing it — a silent, confusing failure far from its cause. Fail loud at the single
	// construction site instead. This never fires under the one production caller
	// (PulseSpeedWuPerTick) — it enforces the invariant the rest of the file assumes.
	if pulseSpeed <= 0 {
		panic(fmt.Sprintf("NewPacedWire: pulseSpeed must be positive (world-units-per-tick), got %v", pulseSpeed))
	}
	return &PacedWire{
		pulseSpeed: pulseSpeed,
		inCh:       make(chan placeRequest, wireChanBufferSize),
		outCh:      make(chan deliveredBead, wireChanBufferSize),
		// reviseCh is generously buffered like inCh: geometry revisions arrive only on a
		// node move (far rarer than bead traffic), and run() drains the whole backlog each
		// cycle. clk defaults to a live RealClock so a bare wire whose run() is never
		// launched (tests driving DriveOneCycle themselves) still has a non-nil clock.
		reviseCh: make(chan reviseReq, wireChanBufferSize),
		clk:      NewRealClock(),
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
// In production this is called ONLY by PacedWire.run (this wire's own goroutine), once
// per cycle, on that goroutine's OWN clock tick (MODEL.md "The network"). It is exported
// so tests that build a bare PacedWire directly (no full loader topology, hence no
// Start-launched run goroutine) can spawn their own driving goroutine that mimics
// production's per-cycle drive, exactly as StepOnceAt's callers used to.
func (pw *PacedWire) DriveOneCycle(ctx context.Context, tick int64) {
	if ctx.Err() != nil {
		return
	}
	pw.drainPlacements(tick)
	pw.stepAll(tick)
}

// run is this wire's OWN goroutine (MODEL.md "The network": each wire is an active
// goroutine). It is launched once per unique dest wire by MoveDispatch.Start. Each cycle
// it: copies its clock once at start; polls its speed sink and applies any playback-speed
// change to its own clk; drains every pending reviseReq (posted by incident edges) and
// applies each with THIS wire's own single tick reading; drives one bead cycle
// (DriveOneCycle); then paces to the next cycle on its own clock (SleepCycle).
//
// This is the ownership fix for fan-in: a destination input port is ONE wire, but several
// source edges can feed it. Previously each incident edge's own goroutine (edgeMover.run)
// drove and revised this shared wire, so N fan-in edges meant N goroutines mutating one
// pw.inflight — a data race and a cross-edge geometry clobber. Now exactly ONE goroutine
// (this run) owns inflight; edges only POST revisions over reviseCh.
func (pw *PacedWire) run(ctx context.Context) {
	// Clock copy taken ONCE at goroutine start (run IS the goroutine). A nil clockSrc
	// (bare test construction) keeps the RealClock default newPacedWire seeded — but such
	// wires are driven by their test via DriveOneCycle and never launch run(), so this
	// path is production-only in practice.
	if pw.clockSrc != nil {
		pw.clk = pw.clockSrc.Copy()
	}
	for {
		if ctx.Err() != nil {
			return
		}
		// One clock reading per cycle (MODEL.md): use it for both the revisions and the
		// drive so a bead's rebase fraction and its advance are measured against the same
		// tick. A speed change drained this cycle applies to the NEXT tick reading.
		tick := pw.clk.Tick()
	drain:
		for {
			select {
			case <-ctx.Done():
				return
			case sp := <-pw.speedCh:
				if rc, ok := pw.clk.(*RealClock); ok {
					rc.SetSpeed(sp)
				}
			case r := <-pw.reviseCh:
				pw.ReviseInFlightGeometry(tick, r.arc, r.seg)
			default:
				break drain
			}
		}
		pw.DriveOneCycle(ctx, tick)
		if err := pw.clk.SleepCycle(ctx); err != nil {
			return
		}
	}
}

// sendRevise posts one edge's geometry revision onto this wire's reviseCh — the
// non-blocking, fire-and-forget handoff an incident edge's own goroutine
// (edgeMover.recomputeGeometry) uses instead of reaching into pw.inflight. Non-blocking so
// the edge never back-pressures on the wire (MODEL.md "Sending"); the buffer is sized like
// inCh and revisions are rare (one per node move), so a full buffer would itself signal a
// bug. A momentarily-dropped revision self-heals: the bead keeps its prior geometry until
// the next move posts a fresh revision. Emits one breadcrumb on the (should-never-happen)
// full case, matching Send.
func (pw *PacedWire) sendRevise(r reviseReq) {
	select {
	case pw.reviseCh <- r:
	default:
		if pw.Trace != nil {
			pw.Trace.Breadcrumb("wire-revise-buffer-full", pw.Target, pw.TargetHandle, "")
		}
	}
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
			// One-cycle delivery floor, BY DESIGN (not a bug to "fix" for zero-arc beads): a
			// bead is never advanced in the same cycle it was placed. For a degenerate
			// zero-arc segment (coincident source/dest ports) ticksToCross is 0, so the model
			// duration is "instant" — but delivering it in its placement cycle would let a
			// node place→deliver→refire within one cycle, a same-cycle feedback busy-loop on a
			// zero-length edge. The floor guarantees every bead is observable for at least one
			// tick and breaks that loop; a zero-arc bead simply delivers on the NEXT cycle.
			if nowTick <= b.placementTick {
				i++
				continue
			}
			emit, pos, final := pw.advanceBead(b, nowTick)
			if emit {
				pw.Trace.Position(pos.node, pos.port, pos.val, pos.x, pos.y, pos.z, pos.t, pos.gen)
			}
			if !final {
				i++
				continue
			}
			b.finalPending = true
		} else if b.streams && pw.Trace != nil {
			// Already finalPending from a PRIOR cycle: the bead has finished traversing and
			// is only waiting for outCh space to hand off. advanceBead is skipped for it, so
			// its last packed position is frozen at the segment end AS OF the cycle it
			// finalized. A concurrent node-drag moves that endpoint (ReviseInFlightGeometry
			// rewrites b.seg every cycle), so without a re-emit the bead visibly detaches
			// from its destination port while it waits. Re-pack it at the CURRENT segment end
			// (t=1) each waiting cycle so it stays glued to the moving port.
			p := b.seg.End
			pw.Trace.Position(b.node, b.port, b.val, p.X, p.Y, p.Z, 1, b.gen)
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

// ReviseInFlightGeometry re-derives EVERY in-flight bead's remaining travel after a
// geometry edit (node-move) changed the edge (MODEL.md "Geometry and time"). It
// preserves each bead's FRACTIONAL progress t (its proportion along the wire), NOT
// the absolute distance covered: each bead stays at the same fraction t and the
// remaining time is recomputed from the NEW arc so UNIFORM PULSE SPEED holds —
// remaining = (1−t)·newArc/pulseSpeed. driveOneCycle re-reads each bead's live
// arc/seg every cycle, so the new geometry takes effect without any relaunch.
// No-op when no bead is in flight.
//
// It rebases EVERY in-flight bead — correct because a wire has exactly one incident edge
// (fan-in is removed), so all its beads are that edge's. Production applies it from run()
// draining a reviseReq off reviseCh; bare-wire unit tests call it directly. Called only by
// the wire's own goroutine (or the test goroutine driving a bare wire); tick is that
// goroutine's own clock reading.
func (pw *PacedWire) ReviseInFlightGeometry(tick int64, newArc float64, newSeg wireSegment) {
	if len(pw.inflight) == 0 {
		return
	}
	nowTick := float64(tick)
	for i := range pw.inflight {
		pw.rebaseBead(&pw.inflight[i], nowTick, newArc, newSeg)
	}
}

// rebaseBead re-derives one in-flight bead's remaining travel for a new arc/segment,
// PRESERVING its fractional progress t (not distance): remaining = (1−t)·newArc/pulseSpeed.
// See ReviseInFlightGeometry's doc. Called only by this wire's own goroutine.
func (pw *PacedWire) rebaseBead(b *inflightBead, nowTick, newArc float64, newSeg wireSegment) {
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
		pw.Trace.Arrive(ai.node, ai.port, ai.value, ai.gen)
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
