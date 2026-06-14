package Wiring

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// deliveredBead pairs the arrived value with the train sequence number that
// produced it. Recv/PollRecv use the seq to dedup: only the first bead from
// each train (lowest seq not yet accepted) is returned; followers are dropped.
type deliveredBead struct {
	val int
	seq uint64
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
	arc       float64       // current arc length of this bead's edge
	seg       wireSegment   // current straight-segment endpoints of this bead's edge
	node      string        // source node id — the position/cancel routing key
	port      string        // source output port — the position/cancel routing key
	streams   bool          // whether this bead carries position-stream context
	gen       uint64        // per-bead id; the bead's walker self-cancels on mismatch
	seq       uint64        // per-train sequence; receiver dedups by it
}

// PacedWire is a multi-bead FIFO transport. A wire may carry more than one bead
// at once (MODEL.md "Sending"): each Send/TryPlace appends a bead and returns
// immediately — the source never waits on the destination, no acknowledgment,
// no back-pressure, no drop. Each bead's clock-timed walker delivers it into the
// `delivered` FIFO at its own deadline; Recv pops `delivered` in send order and
// CONSUMES on read (no separate Done step).
//
// Clock-driven delivery: the wire times its own delivery on the one monotonic
// clock (MODEL.md). When a bead is placed, the wire records the placement elapsed
// reading and starts a walker that calls clock.WaitUntil(placement + arc/pulseSpeed).
// When that pause-aware deadline is reached, the walker moves the bead from
// `inflight` to `delivered`. There is no TS "delivered" signal and no central
// scheduler — every wire reads the same clock independently. Pause freezes the
// arithmetic (WaitUntil does not advance while halted); Reset/Delete cancel all
// pending walkers.
type PacedWire struct {
	mu   sync.Mutex
	cond *sync.Cond
	// inflight holds beads traversing the wire, in send order. delivered holds
	// arrived-but-unread values, in arrival order (FIFO). All mutation under mu.
	inflight  []inflightBead
	delivered []deliveredBead
	// trainSeq is bumped on every new fire (StartTrain or bare placeBead), minting
	// a fresh train identity. All beads within one fire carry the same seq; Recv
	// drops any bead whose seq <= lastAcceptedSeq (train-follower or stale).
	// Reset to 0 on teardown so a restarted edge fires on its first new bead.
	trainSeq uint64
	// lastAcceptedSeq is the highest train seq Recv/PollRecv has accepted (0 = none).
	// Receiver dedup: drop if b.seq <= lastAcceptedSeq; accept and bump otherwise.
	// No clock measurement — immune to timing coupling between nodes.
	lastAcceptedSeq uint64
	// nextGen mints a unique id for each placed bead (walker self-cancel key) and
	// is also bumped on teardown to invalidate ALL outstanding walkers at once.
	nextGen uint64
	// teardownGen: a walker whose bead gen is < teardownGen is invalidated wholesale
	// (Reset/Delete). Beads placed after a teardown get gen >= teardownGen.
	teardownGen uint64
	faded       bool // when true, Send/TryPlace place nothing
	deleted     bool // when true, the edge was deleted; source places no beads
	// clock is the one monotonic clock this wire reads to time its own delivery.
	clock Clock
	// deliverCancel cancels the context all current walkers wait on, so Reset/Delete
	// wake them promptly instead of leaving them parked on the clock. A fresh context
	// is installed each time a walker is (re)launched; bumping teardownGen + canceling
	// stops every outstanding walker.
	deliverCancel context.CancelFunc
	// walkerCtx is the context all current walkers wait on; canceled by teardown.
	walkerCtx  context.Context
	pulseSpeed float64
	// MaxIncomingSimLatencyMs is the per-port aggregate max(SimLatencyMs) over
	// every edge feeding this destination port. Read only by In.SimLatencyMs().
	MaxIncomingSimLatencyMs float64
	// incomingLatency tracks each feeding edge's own SimLatencyMs (edgeId → latency).
	incomingLatency map[string]float64
	Target          string   // destination node id — authoritative slot identity
	TargetHandle    string   // destination input-port name — authoritative slot identity
	Trace           *T.Trace // injected by loader; used for breadcrumb diagnostics only

	// Paced emission train (MODEL.md "Sending"): a node fire starts/refreshes a
	// clock-paced train on this wire — the fired value placed every beadSpacingMs
	// for trainDurationMs. Exactly one pacer goroutine runs while a train is
	// active; a re-fire refreshes value+window in place (no second overlapping
	// pacer). All guarded by pw.mu.
	trainActive   bool
	trainValue    int
	trainBP       beadPlacement // placement (geometry/identity) the train places with
	trainStart    time.Duration // clock active-elapsed reading the current window began
	trainNext     time.Duration // clock active-elapsed reading of the next scheduled placement
	trainRunning  bool          // a pacer goroutine is live (placing or parked on the clock)
	// persistent: when true, runTrain never expires — at each trainDurationMs window
	// rollover it mints a fresh train seq and re-sends the held trainValue.
	// Sender-side only; does not affect receiver dedup logic.
	persistent bool
}

// NewPacedWire creates an empty PacedWire. arcLength is the straight-line
// distance between source and target (world units); pulseSpeed is in world-units
// per millisecond (use PulseSpeedWuPerMs).
func NewPacedWire(arcLength float64, pulseSpeed float64) *PacedWire {
	pw := &PacedWire{
		MaxIncomingSimLatencyMs: arcLength / pulseSpeed,
		pulseSpeed:              pulseSpeed,
		clock:                   NewRealClock(),
		// trainSeq/lastAcceptedSeq start at 0; the first fire bumps trainSeq to 1,
		// which exceeds lastAcceptedSeq (0), so the first bead is always accepted.
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

// Send places a bead on the wire and returns immediately. Multi-bead model: the
// wire may already carry other beads — Send never parks and never drops. It
// appends the bead, schedules its clock-timed delivery at placement+bp.InFlightMs,
// and returns. If the wire is faded or deleted, Send returns nil immediately
// without placing a bead. The ctx is retained only for symmetry; Send no longer
// blocks, so it never returns ErrCanceled.
func (pw *PacedWire) Send(ctx context.Context, value any, bp beadPlacement) error {
	pw.placeBead(value, bp)
	return nil
}

// SendDeliverOnly places a bead timed for delivery at placement+inFlightMs with
// NO position stream. It exists for cross-package tests (node firing-rule tests,
// the headless cascade) that exercise delivery timing only.
func (pw *PacedWire) SendDeliverOnly(ctx context.Context, value any, inFlightMs float64) error {
	return pw.Send(ctx, value, beadPlacement{InFlightMs: inFlightMs})
}

// TryPlace is the non-blocking placement used by the fire-and-forget send rule.
// Multi-bead model: it never blocks and never drops on a busy wire — it always
// places the bead and returns true (unless the wire is faded/deleted, where it
// returns false and places nothing). Kept as a bool for signature compatibility.
func (pw *PacedWire) TryPlace(value any, bp beadPlacement) bool {
	return pw.placeBead(value, bp)
}

// placeBead is the single placement path. Appends an inflightBead under the lock,
// launches its clock-timed walker, and returns true (false only when faded/deleted,
// where nothing is placed). Never blocks, never drops on a busy wire.
// bumpSeq: when true (bare Send/TryPlace outside a train), mint a new trainSeq so
// this standalone fire gets a unique identity at the receiver. When false (called
// from runTrain's subsequent placements), reuse the current trainSeq so all beads
// of one train share the same identity and the receiver deduplicates them to one fire.
func (pw *PacedWire) placeBead(value any, bp beadPlacement) bool {
	return pw.placeBeadSeq(value, bp, true)
}

// placeBeadSeq is the internal placement path. bumpSeq=true mints a fresh trainSeq
// (every bare Send/TryPlace and the first bead of every StartTrain); bumpSeq=false
// reuses the current trainSeq (runTrain followers within the same fire).
func (pw *PacedWire) placeBeadSeq(value any, bp beadPlacement, bumpSeq bool) bool {
	pw.mu.Lock()
	if pw.faded || pw.deleted {
		pw.mu.Unlock()
		return false
	}
	beadVal, _ := value.(int)
	if bumpSeq {
		pw.trainSeq++
	}
	pw.nextGen++
	if pw.nextGen < pw.teardownGen {
		pw.nextGen = pw.teardownGen
	}
	b := inflightBead{
		val:       beadVal,
		placement: pw.clock.Now(),
		arc:       bp.InFlightMs * pw.pulseSpeed,
		seg:       wireSegment{Start: bp.Start, End: bp.End},
		node:      bp.Node,
		port:      bp.Port,
		streams:   bp.streams(),
		gen:       pw.nextGen,
		seq:       pw.trainSeq,
	}
	pw.inflight = append(pw.inflight, b)
	pw.cond.Broadcast()
	pw.launchWalkerLocked(b.gen)
	pw.mu.Unlock()
	return true
}

// StartTrain begins (or refreshes) a clock-paced emission train on this wire.
// On a fire a node calls this instead of placing a single bead: the first bead
// is placed IMMEDIATELY (no initial delay), then the same value is placed every
// beadSpacingMs for trainDurationMs — ~trainDurationMs/beadSpacingMs + 1 beads
// riding the multi-bead wire. A re-fire (even same value) REFRESHES the train:
// it sets the new value+placement and resets the trainDurationMs window from the
// current clock reading, so the in-flight pacer extends/replaces the window
// rather than stacking a second pacer. The pacer is timed on the ONE clock
// (pw.clock — the same active-elapsed reading the bead walkers use) via
// WaitUntil, so the global pause gate freezes the train exactly as it freezes
// delivery. Faded/deleted wires place nothing.
// Returns true if the train was started (the first bead placed), false if the
// wire is faded/deleted (nothing placed).
func (pw *PacedWire) StartTrain(value any, bp beadPlacement) bool {
	pw.mu.Lock()
	if pw.faded || pw.deleted {
		pw.mu.Unlock()
		return false
	}
	beadVal, _ := value.(int)
	now := pw.clock.Now()
	// Mint a new train identity at fire time. All beads in this train (the first one
	// placed immediately below + every runTrain follower) share this seq. The receiver
	// deduplicates the train to exactly one fire by dropping beads with seq <=
	// lastAcceptedSeq.
	pw.trainSeq++
	pw.trainActive = true
	pw.trainValue = beadVal
	pw.trainBP = bp
	pw.trainStart = now
	pw.trainNext = now // first bead places immediately
	launch := !pw.trainRunning
	if launch {
		pw.trainRunning = true
	}
	pw.mu.Unlock()

	// Place the first bead synchronously so a fire always lands a bead at once,
	// even if the pacer goroutine has not been scheduled yet. bumpSeq=false because
	// we already bumped trainSeq above under the lock.
	pw.placeBeadSeq(beadVal, bp, false)

	if launch {
		pw.runTrain()
	}
	return true
}

// runTrain is the single pacer goroutine for this wire. It re-reads the LIVE
// train state each iteration (so a refresh's new value/window/placement are
// picked up), parks on the one clock until the next scheduled placement, places
// the bead, and stops once the window (trainStart + trainDurationMs) has passed.
// The first placement is handled by StartTrain; this loop owns every subsequent
// one. It exits with trainRunning=false so the next fire relaunches it.
func (pw *PacedWire) runTrain() {
	clk := pw.clock
	go func() {
		for {
			pw.mu.Lock()
			if !pw.trainActive || pw.deleted {
				pw.trainRunning = false
				pw.mu.Unlock()
				return
			}
			windowEnd := pw.trainStart + trainDurationMs*time.Millisecond
			next := pw.trainNext + beadSpacingMs*time.Millisecond
			pw.trainNext = next
			ctx := pw.walkerCtx
			pw.mu.Unlock()

			if next > windowEnd {
				// Window exhausted: no more placements for this train.
				pw.mu.Lock()
				// A refresh may have moved the window forward while we computed;
				// only stop if the window is still exhausted relative to the live
				// start. Re-check against the LIVE window end.
				live := pw.trainStart + trainDurationMs*time.Millisecond
				if next > live {
					if pw.persistent && !pw.faded && !pw.deleted {
						now := pw.clock.Now()
						pw.trainSeq++
						pw.trainStart = now
						pw.trainNext = now
						val := pw.trainValue
						bp := pw.trainBP
						pw.mu.Unlock()
						pw.placeBeadSeq(val, bp, false)
						continue
					}
					pw.trainActive = false
					pw.trainRunning = false
					pw.mu.Unlock()
					return
				}
				pw.mu.Unlock()
			}

			if ctx == nil {
				ctx = context.Background()
			}
			if err := clk.WaitUntil(ctx, next); err != nil {
				// Context canceled by teardown — stop the pacer.
				pw.mu.Lock()
				pw.trainRunning = false
				pw.mu.Unlock()
				return
			}

			pw.mu.Lock()
			if !pw.trainActive || pw.faded || pw.deleted {
				pw.trainActive = false
				pw.trainRunning = false
				pw.mu.Unlock()
				return
			}
			// Re-check the window against the LIVE start (a refresh may have moved
			// it); only place while inside [trainStart, trainStart+trainDurationMs].
			if pw.clock.Now() > pw.trainStart+trainDurationMs*time.Millisecond {
				if pw.persistent && !pw.faded && !pw.deleted {
					now := pw.clock.Now()
					pw.trainSeq++
					pw.trainStart = now
					pw.trainNext = now
					val := pw.trainValue
					bp := pw.trainBP
					pw.mu.Unlock()
					pw.placeBeadSeq(val, bp, false)
					continue
				}
				pw.trainActive = false
				pw.trainRunning = false
				pw.mu.Unlock()
				return
			}
			val := pw.trainValue
			bp := pw.trainBP
			pw.mu.Unlock()
			// bumpSeq=false: follower beads reuse the train's seq so the receiver
			// treats all beads of one fire as the same train identity.
			pw.placeBeadSeq(val, bp, false)
		}
	}()
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

// launchWalkerLocked spawns the clock-timed walker for the bead identified by gen.
// Must be called with pw.mu held. The walker re-reads its bead's LIVE arc/seg each
// tick (so a mid-flight ReviseInFlightGeometry is picked up), emits position traces
// at ~16 ms cadence, and at the deadline moves the bead from inflight to delivered.
// It self-cancels if its gen disappears (delivered/removed) or the wire is torn down
// (teardownGen advanced past it).
func (pw *PacedWire) launchWalkerLocked(gen uint64) {
	clk := pw.clock
	tr := pw.Trace
	pulseSpeed := pw.pulseSpeed

	// Ensure there is a live cancel context all walkers share; install one if absent.
	if pw.deliverCancel == nil {
		dctx, cancel := context.WithCancel(context.Background())
		pw.deliverCancel = cancel
		pw.walkerCtx = dctx
	}
	dctx := pw.walkerCtx

	idx := pw.findInflightLocked(gen)
	if idx < 0 {
		return
	}
	placement := pw.inflight[idx].placement
	startNow := clk.Now()

	go func() {
		interval := time.Duration(positionEmitIntervalMs * float64(time.Millisecond))
		// Anchor ticks to placement (stable grid) but never replay the past: the
		// first tick is at or after startNow so a ReviseInFlightGeometry rebase does
		// not race the bead back to t≈0.
		next := placement + interval
		if next <= startNow {
			steps := (startNow-placement)/interval + 1
			next = placement + steps*interval
		}
		for {
			pw.mu.Lock()
			if gen < pw.teardownGen {
				pw.mu.Unlock()
				return
			}
			i := pw.findInflightLocked(gen)
			if i < 0 {
				// Bead delivered or removed by another path.
				pw.mu.Unlock()
				return
			}
			b := pw.inflight[i]
			arc := b.arc
			seg := b.seg
			placement = b.placement
			stream := b.streams && tr != nil && arc > 0
			pw.mu.Unlock()

			deadline := placement
			if pulseSpeed > 0 {
				deadline += time.Duration(arc / pulseSpeed * float64(time.Millisecond))
			}

			target := next
			final := false
			if target >= deadline {
				target = deadline
				final = true
			}
			if err := clk.WaitUntil(dctx, target); err != nil {
				// Context canceled by teardown — stop, do not emit, do not deliver.
				return
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
				pos := lerp(seg.Start, seg.End, t)
				tr.Position(b.node, b.port, b.val, pos.X, pos.Y, pos.Z, t, b.gen)
			}

			if final {
				pw.mu.Lock()
				// FIFO: a bead delivers only once it is at the head of inflight, so
				// Recv sees values in SEND order even when a later bead's deadline
				// (shorter arc) would otherwise overtake an earlier one. Wait until
				// this gen is index 0; teardown (gen < teardownGen) or removal aborts.
				for {
					if gen < pw.teardownGen {
						pw.mu.Unlock()
						return
					}
					i := pw.findInflightLocked(gen)
					if i < 0 {
						pw.mu.Unlock()
						return
					}
					if i == 0 {
						break
					}
					pw.cond.Wait()
				}
				db := pw.inflight[0]
				pw.inflight = pw.inflight[1:]
				pw.delivered = append(pw.delivered, deliveredBead{val: db.val, seq: db.seq})
				pw.cond.Broadcast()
				ai := arriveInfo{emit: db.streams, node: db.node, port: db.port, value: db.val, gen: db.gen}
				pw.mu.Unlock()
				pw.emitArrive(ai)
				return
			}
			next += interval
		}
	}()
}

// Recv blocks until a delivered value is available, then pops and returns it
// (FIFO, in send order). Recv CONSUMES on read — there is no separate Done step.
// Returns ErrCanceled if ctx is done before a value arrives.
func (pw *PacedWire) Recv(ctx context.Context) (any, error) {
	done := pw.watchCtx(ctx)
	defer close(done)

	pw.mu.Lock()
	for {
		for len(pw.delivered) == 0 && ctx.Err() == nil {
			pw.cond.Wait()
		}
		if len(pw.delivered) == 0 {
			pw.mu.Unlock()
			return nil, ErrCanceled
		}
		b := pw.delivered[0]
		pw.delivered = pw.delivered[1:]
		// Identity dedup: collapse a train to one fire. All beads in a train share
		// the same seq (minted once per StartTrain / bare Send). If b.seq <=
		// lastAcceptedSeq, this bead is a train-follower or stale — drop it (already
		// popped) and loop. No time measurement: immune to cross-node timing coupling.
		if b.seq <= pw.lastAcceptedSeq {
			continue
		}
		pw.lastAcceptedSeq = b.seq
		pw.mu.Unlock()
		return b.val, nil
	}
}

// PollRecv is the non-blocking variant of Recv. It pops and returns the front
// delivered value if one is present, else (nil, false). Like Recv, PollRecv
// CONSUMES on read.
func (pw *PacedWire) PollRecv() (any, bool) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	// Same identity dedup as Recv: drop train-follower beads (same seq as an already
	// accepted bead). Loop because several followers may be queued up.
	for len(pw.delivered) > 0 {
		b := pw.delivered[0]
		pw.delivered = pw.delivered[1:]
		if b.seq <= pw.lastAcceptedSeq {
			continue
		}
		pw.lastAcceptedSeq = b.seq
		return b.val, true
	}
	return nil, false
}

// ReviseInFlightGeometry re-derives EVERY in-flight bead's remaining travel after a
// geometry edit (node-move) changed the edge (MODEL.md "Geometry and time"). It
// preserves each bead's FRACTIONAL progress t (its proportion along the wire), NOT
// the absolute distance covered: each bead stays at the same fraction t and the
// remaining time is recomputed from the NEW arc so UNIFORM PULSE SPEED holds —
// remaining = (1−t)·newArc/pulseSpeed. Relaunches each bead's walker so the new
// arc/seg take effect. No-op when no bead is in flight or the wire is deleted.
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
		pw.launchWalkerLocked(b.gen)
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

// emitArrive sends the traversal-complete trace for a delivered bead. Called by
// the walker AFTER releasing pw.mu (trace channel send off the lock).
func (pw *PacedWire) emitArrive(ai arriveInfo) {
	if ai.emit {
		pw.Trace.Arrive(ai.node, ai.port, ai.value, ai.gen)
	}
}

// Done is a PHASE-1 SHIM: removed in phase 2 (gate strip). Recv/PollRecv now
// consume on read, so there is no separate acknowledgment step. No-op.
func (pw *PacedWire) Done() {}

// WaitConsumed is a PHASE-1 SHIM: removed in phase 2 (gate strip). The consume
// gate is gone (a wire never waits on the destination), so this returns nil
// immediately.
func (pw *PacedWire) WaitConsumed(ctx context.Context) error { return nil }

// teardownLocked cancels ALL in-flight bead walkers, clears both queues, and
// returns the per-bead source identities for any in-flight beads so the caller can
// emit one PulseCancelled per dropped bead after unlocking. Must be called with
// pw.mu held.
func (pw *PacedWire) teardownLocked() []arriveInfo {
	var cancelled []arriveInfo
	for i := range pw.inflight {
		b := pw.inflight[i]
		cancelled = append(cancelled, arriveInfo{emit: true, node: b.node, port: b.port, value: b.val, gen: b.gen})
	}
	pw.inflight = nil
	pw.delivered = nil
	// Re-arm identity dedup: reset seq counters so a restarted edge accepts its
	// first new bead. trainSeq is reset to 0; the next fire bumps it to 1, which
	// exceeds lastAcceptedSeq (0), so the first bead is always accepted.
	pw.trainSeq = 0
	pw.lastAcceptedSeq = 0
	// Stop the paced emission train: a Reset/Delete clears the wire, so the pacer
	// must not keep placing. The pacer goroutine observes trainActive=false (or the
	// canceled walkerCtx) and exits.
	pw.trainActive = false
	// Invalidate every outstanding walker at once and wake any parked WaitUntil.
	pw.teardownGen = pw.nextGen + 1
	if pw.deliverCancel != nil {
		pw.deliverCancel()
		pw.deliverCancel = nil
		pw.walkerCtx = nil
	}
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
		pw.Trace.PulseCancelled(ai.node, ai.port, ai.value, ai.gen)
	}
}

// Restore clears the deleted flag set by Delete so the wire carries pulses again.
func (pw *PacedWire) Restore() {
	pw.mu.Lock()
	pw.deleted = false
	pw.teardownLocked()
	pw.mu.Unlock()
}

// InFlight reports whether any bead is currently traversing this wire.
func (pw *PacedWire) InFlight() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return len(pw.inflight) > 0
}

// Occupied reports whether the wire is non-empty: a bead is in flight or a
// delivered value is waiting to be read.
func (pw *PacedWire) Occupied() bool {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return len(pw.inflight) > 0 || len(pw.delivered) > 0
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
