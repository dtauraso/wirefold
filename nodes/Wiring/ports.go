// ports.go — typed port wrappers that bake tracing into send/recv.
//
// Nodes hold In / Out / OutMulti fields instead of raw channels.
// TryRecv / TrySend emit the corresponding trace event on success,
// so a node cannot forget to trace, nor can it mis-type a port name
// string — the port name lives in the wrapper and is set by
// reflectBuild from the struct field name.
//
// Two backing modes:
//   - chan mode (NewIn / NewOut): used by node unit tests. Non-blocking
//     select on the raw channel — original TryRecv/TrySend semantics.
//   - PacedWire mode (NewInPaced / NewOutPaced): used by the loader.
//     TrySend blocks until the paced wire delivers the value (always
//     returns true); TryRecv blocks until a value arrives. Ctx cancel
//     causes both to return the zero-value / false.

package Wiring

import (
	"context"
	"fmt"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// In is a typed input port.
type In struct {
	// chan mode
	ch <-chan int
	// paced mode
	pw  *PacedWire
	ctx context.Context
	// shared
	node  string
	port  string
	trace *T.Trace
}

// PollRecv is the non-blocking receive used by windowed nodes. In paced mode it
// calls pw.PollRecv (returns immediately with ok=false when no value is present,
// without parking) and, on success, CONSUMES the value on read (pops the front
// delivered bead) while emitting the same trace events as TryRecv. There is no
// separate Done step — the read itself consumes. In chan mode it does a
// non-blocking select, identical to TryRecv's default branch.
func (i *In) PollRecv() (int, bool) {
	if i == nil {
		return 0, false
	}
	if i.pw != nil {
		n, ok := i.pw.Recv()
		if !ok {
			return 0, false
		}
		i.trace.Recv(i.node, i.port, n)
		return n, true
	}
	if i.ch == nil {
		return 0, false
	}
	select {
	case v := <-i.ch:
		i.trace.Recv(i.node, i.port, v)
		return v, true
	default:
		return 0, false
	}
}

// Wired reports whether this In port is bound to a real edge (paced-wire
// mode). Returns false for a nil In or a dead-end chan port (unwired).
// Nodes gate optional feedback receives on Wired() so unwired ports are never
// read.
func (i *In) Wired() bool {
	if i == nil {
		return false
	}
	return i.pw != nil
}

// Breadcrumb emits a trace breadcrumb on the input port's wire identity (target
// node + handle). Used by windowed nodes for the window_clear breadcrumb.
func (i *In) Breadcrumb(event, detail string) {
	if i == nil || i.trace == nil {
		return
	}
	node, port := i.node, i.port
	if i.pw != nil {
		node, port = i.pw.Target, i.pw.TargetHandle
	}
	i.trace.Breadcrumb(event, node, port, detail)
}

// SendRule names the per-edge send policy applied by the source node after a
// successful TrySend. The wire stays dumb transport; the node consults the rule.
type SendRule string

const (
	// RuleConsumeGated: default send rule. Kept for compatibility with persisted
	// topology JSON; the consume gate was removed (PacedWire.Done/WaitConsumed
	// are no-ops). The only meaningful distinction is Gated() which gates
	// optional feedback ports.
	RuleConsumeGated SendRule = "consumeGated"
	// RuleFireAndForget: the node sends and does not wait for consumption.
	RuleFireAndForget SendRule = "fireAndForget"
)

// ParseSendRule converts a raw string into a SendRule.
// An empty string returns RuleConsumeGated (preserving the default-when-absent
// behavior). Any string that is not a recognised constant returns an error.
func ParseSendRule(s string) (SendRule, error) {
	switch s {
	case "":
		return RuleConsumeGated, nil
	case string(RuleConsumeGated):
		return RuleConsumeGated, nil
	case string(RuleFireAndForget):
		return RuleFireAndForget, nil
	default:
		return RuleConsumeGated, fmt.Errorf("invalid sendRule %q: must be one of %q, %q",
			s, RuleConsumeGated, RuleFireAndForget)
	}
}

// outGeom is an immutable snapshot of an Out's per-edge geometry, published by the
// owning edgeMover goroutine (recomputeGeometry) via atomic.Pointer and LOADED by
// cross-goroutine readers on the source node goroutine (placement / PlaceDriven /
// the EmitGeometry closure). Writes happen only on the owning
// goroutine, reads via atomic load — no lock, no coordinator. This mirrors the
// nodeMover.snap / centerSnap publish/observe pattern (MODEL.md: per-goroutine
// ownership, cross-goroutine reads via atomic snapshots).
//
//   - ArcLength / SimLatencyMs: this edge's own travel-time, computed from this
//     edge's port-to-port geometry (chord length of this specific segment). SendWire
//     logs these so each bead animates at its own speed even when multiple edges fan
//     into one destination port.
//   - Start/End: this edge's straight-segment endpoints (source OUT-port world pos,
//     dest IN-port world pos) in the SAME 3-D frame the renderer draws. They travel
//     WITH each placed bead (beadPlacement) so the wire's position stream evaluates
//     P(t)=Start+t*(End-Start) on this edge — fan-in safe because the shared dest
//     wire never stores per-edge geometry.
type outGeom struct {
	ArcLength    float64
	SimLatencyMs float64
	Start, End   vec3
}

// Out is a typed output port.
type Out struct {
	// chan mode
	ch chan<- int
	// paced mode
	pw  *PacedWire
	ctx context.Context
	// shared
	node  string
	port  string
	trace *T.Trace
	// geom is this edge's per-edge geometry, published as an immutable snapshot via
	// atomic.Pointer. Seeded at construction (NewOutPaced) and republished by the
	// owning edgeMover on every drag tick; read only via Geom() (atomic load). Never
	// accessed as bare fields across goroutines.
	geom atomic.Pointer[outGeom]
	// EdgeLabel is the TS edge id for this output port's wire. Set by the loader
	// so the node's EmitGeometry closure can stream the authoritative curve via
	// tr.Geometry(EdgeLabel, Start..End) on startup.
	EdgeLabel string
	// Rule is the per-edge send policy applied by the source node after a
	// successful TrySend. Empty string defaults to consumeGated (see Gated).
	Rule SendRule
}

// Geom loads the current per-edge geometry snapshot. Returns the zero outGeom when
// none has been published (chan-mode test Outs never publish). Safe from any
// goroutine — reads the atomically-published snapshot, never the writer's live state.
func (o *Out) Geom() outGeom {
	if o == nil {
		return outGeom{}
	}
	if g := o.geom.Load(); g != nil {
		return *g
	}
	return outGeom{}
}

// publishGeom atomically publishes a fresh per-edge geometry snapshot. Called only
// on the owning goroutine (edgeMover.recomputeGeometry) and at construction.
func (o *Out) publishGeom(g outGeom) {
	o.geom.Store(&g)
}

// placement builds the per-bead beadPlacement this Out hands to the wire: the
// per-edge in-flight time plus the position-stream context (segment endpoints
// + source identity). Centralized so TrySend and TryEmit stay in lockstep.
func (o *Out) placement() beadPlacement {
	return o.placementFrom(o.Geom())
}

// placementFrom builds a beadPlacement from an already-loaded geometry snapshot, so
// a caller can use ONE consistent snapshot for both the placement and the SendWire
// trace (rather than two independent atomic loads that could straddle a republish).
func (o *Out) placementFrom(g outGeom) beadPlacement {
	return beadPlacement{
		InFlightMs: g.SimLatencyMs,
		Start:      g.Start,
		End:        g.End,
		Node:       o.node,
		Port:       o.port,
	}
}

// Paced reports whether this Out drives a paced wire. It is the paced-vs-chan MODE
// predicate: paced mode sleeps on the caller's own clock copy and StepOnces the wire;
// chan mode (unit tests) has no wire to advance and falls back to a wall-clock sleep.
//
// This used to say out loud what `out.Clock() != nil` said sideways — Out.Clock() is
// gone now (per-goroutine-clock.md API demolition item 1: port accessors go away, a
// goroutine gets its clock passed to it directly instead of reaching through a port),
// so Paced() is the only mode selector left. The condition is just "does this Out have
// a PacedWire": NewPacedWire (paced_wire.go) is the only construction site in the repo,
// so pw != nil is unambiguous.
func (o *Out) Paced() bool {
	return o != nil && o.pw != nil
}

// Gated reports whether the source node should wait for consumption after a
// successful send. Nil-safe; the zero value (empty Rule) is gated.
func (o *Out) Gated() bool {
	if o == nil {
		return true
	}
	return o.Rule != RuleFireAndForget
}

// placeDrivenNoWalker sends one bead placement onto the paced wire's in-channel
// (PacedWire.Send — non-blocking, never waits on the wire or the destination) and
// emits the SendWire trace at placement time. The wire's own goroutine stamps the
// placement tick from its own clock when it drains the send; the source no longer
// pins one (MODEL.md: "The wire goroutine reads its OWN clock copy and its own
// tick"). Caller must have already checked o.pw != nil. Returns the wire's
// SendOutcome verbatim so the caller (PlaceDrivenAt) can distinguish a transient
// buffer-full from a genuinely terminal condition instead of collapsing both to
// one bool.
func (o *Out) placeDrivenNoWalker(v int) SendOutcome {
	g := o.Geom()
	outcome := o.pw.Send(v, o.placementFrom(g))
	if outcome != SendPlaced {
		return outcome
	}
	o.trace.SendWire(o.node, o.port, v, g.ArcLength, g.SimLatencyMs, o.pw.Target, o.pw.TargetHandle)
	return SendPlaced
}

// Wired reports whether this Out port is bound to a real edge (paced-wire
// mode). Returns false for a nil Out or a dead-end chan port (unwired).
// Nodes gate optional feedback sends on Wired() so unwired ports are never
// written.
func (o *Out) Wired() bool {
	if o == nil {
		return false
	}
	return o.pw != nil
}

// DriveOutcome distinguishes the outcomes a drive placement can have.
// Collapsing them onto a single bool (the pre-fix shape) made "chan mode, sent
// fine, nothing more to drive" indistinguishable from "placement failed /
// wire torn down" — callers that stopped their loop on !Live() then stopped
// on every chan-mode send too. Keeping them explicit makes that conflation
// unrepresentable.
//
// DriveBufferFull is its own constant, split out from DriveFailed, for the
// same reason: a paced wire's inCh being momentarily full (PacedWire.
// SendBufferFull) is TRANSIENT — the wire's own goroutine drains inCh every
// cycle — and must never be treated as "stop, the wire is gone". Only a nil
// Out (no destination at all, a structural condition) is DriveFailed. Naming
// them apart means a caller who writes `.Failed()` gets ONLY the terminal
// case by construction; it cannot accidentally also catch buffer-full the way
// the pre-fix single bool did.
type DriveOutcome uint8

const (
	// DrivePlaced: a real bead was placed on a paced wire; delivery is driven
	// by subsequent StepOnce/StepOnceAt calls.
	DrivePlaced DriveOutcome = iota
	// DriveSentChan: chan mode (tests) — the value was sent (or dropped by a
	// full non-blocking select) on the raw channel. Nothing to drive further.
	DriveSentChan
	// DriveBufferFull: the paced wire's inCh was momentarily full
	// (PacedWire.SendBufferFull). TRANSIENT, NOT TERMINAL — a caller driving a
	// continuous-placement loop must NOT stop on this; skip this cycle's
	// placement and retry next cycle. A breadcrumb was already emitted by
	// PacedWire.Send.
	DriveBufferFull
	// DriveFailed: nil Out — there is no destination at all. Structural and
	// terminal; the caller should stop.
	DriveFailed
)

// DriveItem is an exported handle to one placed bead. Delivery is timed by the
// wire's own goroutine, not by the caller — this type reports which of the
// DriveOutcomes occurred.
type DriveItem struct {
	outcome DriveOutcome
}

// Live reports whether this DriveItem carries a bead actually placed on a
// paced wire (i.e. PlaceDriven succeeded in paced-wire mode) — outcome ==
// DrivePlaced. False for a nil Out, chan mode, a momentary buffer-full, or a
// failed placement. Callers that need ONLY "did this become a real, time-able
// in-flight bead" (e.g. holdnewsendold's processing-window length) check
// this; callers that need "should I stop, the wire is gone" must check
// Failed() instead — Live() alone cannot distinguish chan-mode success,
// buffer-full, or true failure.
func (di DriveItem) Live() bool {
	return di.outcome == DrivePlaced
}

// Failed reports whether the placement failed for a STRUCTURAL, TERMINAL
// reason: a nil Out (no destination at all). It deliberately does NOT report
// true for DriveBufferFull — a momentarily-full paced-wire buffer is
// transient and self-clears as the wire's own goroutine drains it; treating
// it as terminal would silently and permanently kill a source's drive
// goroutine on ordinary transient load. Callers driving a continuous-
// placement loop should stop on Failed(), not on !Live() — a chan-mode
// successful send or a buffer-full retry are also !Live() but must not stop
// the loop. See BufferFull() for the transient case.
func (di DriveItem) Failed() bool {
	return di.outcome == DriveFailed
}

// BufferFull reports whether this placement did not go through because the
// paced wire's inCh was momentarily full. This is the DISTINCT, NON-TERMINAL
// counterpart to Failed(): a caller must handle this case (typically: skip
// this cycle, keep looping) rather than let it fall through to a generic
// failure branch, which is exactly the bug this type split fixes.
func (di DriveItem) BufferFull() bool {
	return di.outcome == DriveBufferFull
}

// PlaceDrivenAt places one bead on this Out WITHOUT spawning a walker, emits
// the SendWire trace, and returns a DriveItem reporting the outcome. Delivery is
// timed by the wire's own goroutine (PacedWire, driven by edgeMover.run), not by
// the caller. In chan mode (tests) it sends immediately on the raw channel and
// returns DriveSentChan, so unit tests keep their synchronous chan semantics. A
// nil Out returns DriveFailed. A momentarily-full paced wire returns
// DriveBufferFull, never DriveFailed — see the DriveOutcome doc comment.
func (o *Out) PlaceDrivenAt(v int) DriveItem {
	if o == nil {
		return DriveItem{outcome: DriveFailed}
	}
	if o.pw != nil {
		switch o.placeDrivenNoWalker(v) {
		case SendPlaced:
			return DriveItem{outcome: DrivePlaced}
		default: // SendBufferFull
			return DriveItem{outcome: DriveBufferFull}
		}
	}
	// chan mode (tests): no drive needed, send now and return DriveSentChan.
	if o.ch != nil {
		select {
		case o.ch <- v:
			o.trace.Send(o.node, o.port, v)
		default:
		}
		return DriveItem{outcome: DriveSentChan}
	}
	return DriveItem{outcome: DriveSentChan}
}

// OutMulti is a fanout port: a slice of Outs sharing one logical name.
type OutMulti []*Out

// PlaceDrivenAllAt places value v (no walker) on EVERY Out in the set, emitting
// the SendWire trace for each and appending a DriveItem per Out to dst. Delivery
// is timed by each wire's own goroutine, so the whole fan-out animates
// concurrently. Chan-mode Outs send immediately and contribute inert items.
func (outs OutMulti) PlaceDrivenAllAt(v int, dst []DriveItem) []DriveItem {
	for _, o := range outs {
		if o == nil {
			continue
		}
		dst = append(dst, o.PlaceDrivenAt(v))
	}
	return dst
}

// NewInPaced / NewOutPaced are used by the loader. Uses PacedWire mode. Neither the
// port nor the wire behind it holds a clock (per-goroutine-clock.md API demolition
// item 1: port accessors are gone) — a node's own Clock field is what its goroutine
// Copies from at startup.
func NewInPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace) *In {
	return &In{pw: pw, ctx: ctx, node: node, port: port, trace: tr}
}

// NewPacedOutNoGeom builds a paced Out with a zero wire segment. Node packages
// outside Wiring cannot name the unexported wireSegment, so they cannot call
// NewOutPaced directly — this is the supported entry point for tests that need to
// exercise the paced OUTPUT drive (PlaceDrivenAt → StepOnceAt) under a
// RealClock. Only bead timing is exercised; the zero segment means position
// traces carry no geometry. Production paced Outs are built by the loader/builders
// with real segments, not through this.
func NewPacedOutNoGeom(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace, rule SendRule, arcLength, simLatencyMs float64, edgeLabel string) *Out {
	return NewOutPaced(pw, ctx, node, port, tr, rule, arcLength, simLatencyMs, wireSegment{}, edgeLabel)
}

// NewOutChanForTest builds a chan-mode Out for tests outside the Wiring
// package. Chan mode's backing channel (ch) is unexported so other packages'
// tests (e.g. gatecommon's DriveHeld regression) cannot construct one
// directly; this is the supported entry point, mirroring NewPacedOutNoGeom's
// role for paced-mode tests.
func NewOutChanForTest(ch chan<- int, node, port string, tr *T.Trace) *Out {
	return &Out{ch: ch, node: node, port: port, trace: tr}
}

func NewOutPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace, rule SendRule, arcLength, simLatencyMs float64, seg wireSegment, edgeLabel string) *Out {
	if rule == "" {
		rule = RuleConsumeGated
	}
	o := &Out{pw: pw, ctx: ctx, node: node, port: port, trace: tr, Rule: rule, EdgeLabel: edgeLabel}
	// Seed the atomic snapshot so the first placement reads valid geometry before any move.
	o.publishGeom(outGeom{ArcLength: arcLength, SimLatencyMs: simLatencyMs, Start: seg.Start, End: seg.End})
	return o
}
