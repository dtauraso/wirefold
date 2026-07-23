// ports.go — typed port wrappers that bake tracing into send/recv.
//
// Nodes hold In / Out / Broadcast fields instead of raw channels.
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
	// stream is this In's owning node's shared interior-stream getter
	// (Wiring.newInteriorStreamGetter, injected by wireInPort) — lazily resolves to the
	// SAME *interiorStream instance every closure/port on this node shares. Recv flushes
	// its own row-resolved RowEvent onto it (owner_events.go). nil for a bare chan-mode
	// In built outside reflectBuild (e.g. gatecommon test helpers) — PollRecv's nil
	// check below skips the flush in that case.
	stream func() *interiorStream
	// portRow is this In's own buffer PORT-ROW index (isInput=true), resolved once at
	// construction (wireInPort) from pb.md's row table — see wireInPort's doc comment.
	// -1 when unresolved (no md, or an unwired dead-end port).
	portRow int32
}

// PollRecv is the non-blocking receive used by windowed nodes. In paced mode it
// calls pw.PollRecv (returns immediately with ok=false when no value is present,
// without parking) and, on success, CONSUMES the value on read (pops the front
// delivered bead) while emitting the same trace events as TryRecv. There is no
// separate Done step — the read itself consumes. In chan mode it does a
// non-blocking select, identical to TryRecv's default branch.
//
// Each successful receive ALSO flushes a KindRecv RowEvent onto this node's own
// interior-stream frame (i.stream — Buffer/pack.go's decentralizedEventKinds
// excludes KindRecv from the central VIEW-bucket): this node's own Update goroutine
// (the SAME goroutine calling PollRecv) is the sole owner of when it receives, so it
// resolves its own NodeRow/PortRow at the call site (owner_events.go) rather than
// routing through a shared accumulator.
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
		i.flushRecvEvent(n)
		return n, true
	}
	if i.ch == nil {
		return 0, false
	}
	select {
	case v := <-i.ch:
		i.trace.Recv(i.node, i.port, v)
		i.flushRecvEvent(v)
		return v, true
	default:
		return 0, false
	}
}

// flushRecvEvent records this receive as a row-resolved RowEvent on this In's owning
// node's shared interior-stream frame. No-op when stream is unset (bare chan-mode In
// built outside reflectBuild) or the node has no dedicated interior fd.
func (i *In) flushRecvEvent(value int) {
	if i.stream == nil {
		return
	}
	s := i.stream()
	if s == nil {
		return
	}
	s.writeEvents([]RowEvent{{
		Kind: T.KindRecv, NodeRow: s.nodeRow, PortRow: i.portRow,
		TargetRow: -1, TargetPortRow: -1, EdgeRow: -1, Value: int32(value),
	}})
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
// owning edgeMover goroutine (recomputeGeometry) and delivered to each reader
// goroutine over its OWN buffered-1, latest-wins channel (geomEcho / geomSend
// below) — never a shared field read by more than one goroutine. This mirrors
// per-goroutine-clock.md's speedCh Delivery pattern (SendSpeedNonBlocking /
// ApplySpeedNonBlocking): the producer sends, each consumer owns its own copy.
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
	// geomEcho / geomSend are buffered-1, latest-wins channels fed the SAME
	// outGeom snapshot by publishGeom on every LIVE update (edgeMover.recomputeGeometry
	// on a drag tick). The load-time file geometry is NOT sent through them — sendCur /
	// echoCur are initialized to it directly in NewOutPaced. TWO
	// channels because up to two INDEPENDENT goroutines observe this Out's
	// geometry over its lifetime, and each must own a private copy rather than
	// share one field:
	//   - geomEcho is drained exactly ONCE, by the EmitGeometry closure
	//     (builders.go, via echoGeom) on the node's own startup goroutine, to
	//     echo the seed geometry into the trace at construction time.
	//   - geomSend is drained every cycle, via Geom() below, by whichever ONE
	//     goroutine actually places beads on this Out — the node's own Update
	//     goroutine for most kinds, or the dedicated DriveHeld goroutine for
	//     Pulse/HoldFlip (see gatecommon/drive.go). Exactly one goroutine ever
	//     calls PlaceDrivenAt/placement on a given Out, so exactly one
	//     goroutine ever drains geomSend.
	// sendCur is the owned local cache for geomSend, mutated only by that one
	// goroutine inside Geom(); echoCur plays the same role for geomEcho inside
	// echoGeom(). Nil channels (chan-mode test Outs, which never publish) are
	// safe: a non-blocking receive on a nil channel simply never fires.
	geomEcho chan outGeom
	geomSend chan outGeom
	sendCur  outGeom
	echoCur  outGeom
	// EdgeLabel is the TS edge id for this output port's wire. Set by the loader
	// so the node's EmitGeometry closure can stream the authoritative curve via
	// tr.Geometry(EdgeLabel, Start..End) on startup.
	EdgeLabel string
	// Rule is the per-edge send policy applied by the source node after a
	// successful TrySend. Empty string defaults to consumeGated (see Gated).
	Rule SendRule
	// stream is this Out's owning node's shared interior-stream getter
	// (Wiring.newInteriorStreamGetter, injected by wireOutPort/wireOutMultiPort) — see
	// In.stream's doc comment. nil for a bare chan-mode Out built outside reflectBuild
	// (NewOutChanForTest, node unit tests).
	stream func() *interiorStream
	// portRow is this Out's own buffer PORT-ROW index (isInput=false); targetRow/
	// targetPortRow are the destination node/port's buffer rows (b.pw.Target/
	// TargetHandle — static after wiring). All resolved once at construction
	// (wireOutPort/wireOutMultiPort's doc comment). -1 when unresolved.
	portRow, targetRow, targetPortRow int32
}

// Geom returns the current per-edge geometry snapshot as seen by THIS Out's one
// sending goroutine: it non-blockingly drains any newer value off geomSend into
// the goroutine-owned sendCur cache, then returns sendCur. Must only be called
// from the single goroutine that places beads on this Out (see the geomSend
// doc comment above) — calling it from two goroutines would race sendCur.
// Returns the zero outGeom when nothing has ever been published (chan-mode
// test Outs never publish, and o.geomSend is nil).
func (o *Out) Geom() outGeom {
	if o == nil {
		return outGeom{}
	}
	drainGeomNonBlocking(o.geomSend, &o.sendCur)
	return o.sendCur
}

// echoGeom is Geom()'s counterpart for the OTHER reader: the EmitGeometry
// closure (builders.go), which drains its own geomEcho channel into echoCur
// exactly once, at node startup, to echo the seed geometry into the trace.
// Must only ever be called from that one startup call site.
func (o *Out) echoGeom() outGeom {
	if o == nil {
		return outGeom{}
	}
	drainGeomNonBlocking(o.geomEcho, &o.echoCur)
	return o.echoCur
}

// publishGeom fans a fresh per-edge geometry snapshot out to both reader
// channels, latest-wins (dropping any undrained stale value first). Called
// only on the owning goroutine (edgeMover.recomputeGeometry) for LIVE updates;
// the load-time file geometry is set directly on sendCur/echoCur in
// NewOutPaced, not published here. Both channels are nil on a chan-mode Out
// (test-only, never published to); sendGeomNonBlocking on a nil channel is a
// silent no-op, matching Geom()/echoGeom()'s "never published" zero-value
// return.
func (o *Out) publishGeom(g outGeom) {
	sendGeomNonBlocking(o.geomEcho, g)
	sendGeomNonBlocking(o.geomSend, g)
}

// drainGeomNonBlocking folds the latest pending value off ch (if any) into
// *cur, without blocking. A nil ch (chan-mode Out, or an unpublished port)
// simply never selects the receive case, leaving *cur at its zero value.
func drainGeomNonBlocking(ch chan outGeom, cur *outGeom) {
	select {
	case g := <-ch:
		*cur = g
	default:
	}
}

// sendGeomNonBlocking delivers g to ch, latest-wins: if the buffer already
// holds an undrained stale value (the reader hasn't woken to drain it since
// the last publish), that stale value is dropped and replaced — mirrors
// SendSpeedNonBlocking (clock.go) for the same reason (absolute state, not an
// event stream). A nil ch (chan-mode Out) makes every case here select
// `default`, so this is a silent no-op.
func sendGeomNonBlocking(ch chan outGeom, g outGeom) {
	select {
	case ch <- g:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- g:
	default:
	}
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
	o.flushSendEvent(v, g.ArcLength, g.SimLatencyMs)
	return SendPlaced
}

// flushSendEvent records this send as a row-resolved RowEvent on this Out's owning
// node's shared interior-stream frame (Buffer/pack.go's decentralizedEventKinds
// excludes KindSend from the central VIEW-bucket): this node's own Update goroutine
// (the SAME goroutine driving the send) is the sole owner, so it resolves its own
// NodeRow/PortRow/TargetRow/TargetPortRow at the call site (owner_events.go). No-op
// when stream is unset (bare chan-mode Out) or the node has no dedicated interior fd.
func (o *Out) flushSendEvent(value int, arcLength, simLatencyMs float64) {
	if o.stream == nil {
		return
	}
	s := o.stream()
	if s == nil {
		return
	}
	s.writeEvents([]RowEvent{{
		Kind: T.KindSend, NodeRow: s.nodeRow, PortRow: o.portRow,
		TargetRow: o.targetRow, TargetPortRow: o.targetPortRow, EdgeRow: -1,
		Value: int32(value), ArcLength: arcLength, SimLatencyMs: simLatencyMs,
	}})
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
	// chan mode (tests, or a production dead-end unwired Out): no drive needed, send
	// now and return DriveSentChan. flushSendEvent no-ops when stream is unset (both
	// cases: no reflectBuild-injected getter).
	if o.ch != nil {
		select {
		case o.ch <- v:
			o.trace.Send(o.node, o.port, v)
			o.flushSendEvent(v, 0, 0)
		default:
		}
		return DriveItem{outcome: DriveSentChan}
	}
	return DriveItem{outcome: DriveSentChan}
}

// Broadcast is a broadcast port: a slice of Outs the node emits the same
// value onto, each its own independent 1:1 wire.
type Broadcast []*Out

// PlaceDrivenAllAt places value v (no walker) on EVERY Out in the set, emitting
// the SendWire trace for each and appending a DriveItem per Out to dst. Delivery
// is timed by each wire's own goroutine, so the whole broadcast animates
// concurrently. Chan-mode Outs send immediately and contribute inert items.
func (outs Broadcast) PlaceDrivenAllAt(v int, dst []DriveItem) []DriveItem {
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
	// The initial geometry is the LOAD-TIME geometry the loader derived from the
	// topology file (arcLength/simLatencyMs/seg) — not a synthetic seed. Initialize
	// each reader's owned cache to it directly, before either reader goroutine starts
	// (happens-before), so the first echo/placement reads valid file geometry with no
	// channel bootstrap. The channels then carry ONLY live edgeMover updates (drags);
	// until the first one arrives, a non-blocking drain finds them empty and leaves the
	// cache at this file value.
	fileGeom := outGeom{ArcLength: arcLength, SimLatencyMs: simLatencyMs, Start: seg.Start, End: seg.End}
	o := &Out{
		pw: pw, ctx: ctx, node: node, port: port, trace: tr, Rule: rule, EdgeLabel: edgeLabel,
		geomEcho: make(chan outGeom, 1),
		geomSend: make(chan outGeom, 1),
		sendCur:  fileGeom,
		echoCur:  fileGeom,
	}
	return o
}
