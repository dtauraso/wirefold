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
	// clock is the fallback Clock for an UNWIRED In (chan mode), where there is no
	// PacedWire to read one from. The loader sets it to the same shared clock every
	// wired node runs on, so an unfed node paces normally and stays inert by polling
	// a port that never delivers. Never read in paced mode (pw.clock wins). See
	// In.Clock.
	clock Clock
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
		v, ok := i.pw.PollRecv()
		if !ok {
			return 0, false
		}
		n, _ := v.(int)
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

// Clock returns the shared human-speed Clock this port paces on. A node's Update loop
// reads it to pace off the same clock the wire times delivery on, without owning a clock
// reference of its own.
//
// NEVER RETURNS NIL — that is the point, not a convenience. Every caller does
// `clk := X.In.Clock()` then `clk.SleepCycle(ctx)` with no guard, so a nil here is a nil-
// interface method call that panics; with no recover() over a node goroutine, one unfed
// port took down every other node and the buffer stream with it. An unwired In is a
// LOADABLE state on purpose (validate.go has no required-inbound-edge check: an unfed
// node loads and stays inert by precondition-gating), so the nil was reachable from an
// ordinary topology. It is now unrepresentable rather than guarded at five call sites:
// an unwired In holds a real clock the same way deadEndIn gives it a real channel.
//
// Contrast Out.Clock, which DOES return nil and must keep doing so: there nil is a
// deliberate mode selector (gatecommon.RunGate reads ToPassed.Clock() to choose paced vs
// chan mode). Nil means "no wire" for an Out; for an In it only ever meant "about to
// panic".
func (i *In) Clock() Clock {
	if i == nil {
		return inertClock{}
	}
	if i.pw != nil && i.pw.clock != nil {
		return i.pw.clock
	}
	if i.clock != nil {
		return i.clock
	}
	return inertClock{}
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

// Paced reports whether this Out drives a paced wire on a shared clock. It is the
// paced-vs-chan MODE predicate: paced mode sleeps on the shared clock and StepOnces the
// wire; chan mode (unit tests) has no wire to advance and falls back to a wall-clock
// sleep.
//
// This says out loud what `out.Clock() != nil` used to say sideways. Mode was encoded in
// a nil, so the ONLY thing stopping someone from "fixing" Clock() to never return nil —
// and silently collapsing both modes into one — was a comment asking them not to. Asking
// is not a mechanism (CLAUDE.md: enforce in code before adding prose). With the mode in a
// named predicate, Clock() is free to be non-nil like In.Clock, and nil carries no
// meaning on either port.
//
// The condition is exactly what the nil encoded — pw AND a clock on it — not merely
// Wired(): a paced wire whose clock was never set took the chan-mode branch before, and
// still does.
func (o *Out) Paced() bool {
	return o != nil && o.pw != nil && o.pw.clock != nil
}

// Clock returns the shared human-speed Clock this port paces on. NEVER RETURNS NIL —
// mirrors In.Clock; see its doc for why a nil Clock is a panic waiting to happen.
// Callers choosing paced vs chan behavior must branch on Paced(), not on this being nil.
// In chan mode this is the inert placeholder, which Paced() callers never reach.
func (o *Out) Clock() Clock {
	if !o.Paced() {
		return inertClock{}
	}
	return o.pw.clock
}

// Gated reports whether the source node should wait for consumption after a
// successful send. Nil-safe; the zero value (empty Rule) is gated.
func (o *Out) Gated() bool {
	if o == nil {
		return true
	}
	return o.Rule != RuleFireAndForget
}

// placeDrivenNoWalker places one bead on the paced wire WITHOUT spawning a
// walker goroutine, emitting the SendWire trace at placement time. The placed
// bead is subsequently driven to delivery by per-cycle StepOnce. Caller must
// have already checked o.pw != nil.
func (o *Out) placeDrivenNoWalker(v int) (gen uint64, ok bool) {
	g := o.Geom()
	gen, ok = o.pw.placeBeadNoWalker(v, o.placementFrom(g))
	if !ok {
		return 0, false
	}
	o.trace.SendWire(o.node, o.port, v, g.ArcLength, g.SimLatencyMs, o.pw.Target, o.pw.TargetHandle)
	return gen, true
}

// placeDrivenNoWalkerAt is placeDrivenNoWalker with the placement tick PINNED
// by the caller (see PacedWire.placeBeadNoWalkerAt) instead of a live clock
// read. Caller must have already checked o.pw != nil.
func (o *Out) placeDrivenNoWalkerAt(v int, tick int64) (gen uint64, ok bool) {
	g := o.Geom()
	gen, ok = o.pw.placeBeadNoWalkerAt(v, o.placementFrom(g), tick)
	if !ok {
		return 0, false
	}
	o.trace.SendWire(o.node, o.port, v, g.ArcLength, g.SimLatencyMs, o.pw.Target, o.pw.TargetHandle)
	return gen, true
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

// StepOnce advances this Out's underlying wire by one non-blocking tick-step
// (see PacedWire.StepOnce): any in-flight bead due at the current tick moves
// one position-step, and FIFO-head delivery is attempted if ready. Returns
// immediately; never parks. No-op in chan mode (o.pw == nil) or for a nil Out.
// Exported so a node's own continuous-drive goroutine (gatecommon.DriveHeld)
// can pace itself one tick at a time instead of blocking a full traversal.
func (o *Out) StepOnce(ctx context.Context) {
	if o == nil || o.pw == nil {
		return
	}
	o.pw.StepOnce(ctx)
}

// StepOnceAt is StepOnce with the current tick PINNED by the caller (see
// PacedWire.StepOnceAt). Use when stepping several Outs in the same cycle
// so they all observe the same tick. No-op in chan mode or for a nil Out.
func (o *Out) StepOnceAt(ctx context.Context, tick int64) {
	if o == nil || o.pw == nil {
		return
	}
	o.pw.StepOnceAt(ctx, tick)
}

// DriveItem is an exported handle to one placed bead. Delivery is driven by
// per-cycle StepOnce on the underlying wire, not by the caller directly — this
// type only reports whether the placement succeeded (Live).
type DriveItem struct {
	live bool
}

// Live reports whether this DriveItem carries a bead actually placed on a
// paced wire (i.e. PlaceDriven succeeded in paced-wire mode). False for a nil
// Out, chan mode, or a failed placement (torn-down wire) — callers that
// need to detect placement failure check this.
func (di DriveItem) Live() bool {
	return di.live
}

// PlaceDriven places one bead on this Out WITHOUT spawning a walker, emits the
// SendWire trace, and returns a DriveItem reporting whether the placement
// succeeded. Delivery is driven by the caller's per-cycle StepOnce/StepOnceAt
// on this Out (or the underlying wire) each subsequent cycle. In chan mode
// (tests) it sends immediately on the raw channel and returns an inert item,
// so unit tests keep their synchronous chan semantics. A nil Out, or a failed
// placement (deleted), returns an inert item.
func (o *Out) PlaceDriven(v int) DriveItem {
	if o == nil {
		return DriveItem{}
	}
	if o.pw != nil {
		if _, ok := o.placeDrivenNoWalker(v); !ok {
			return DriveItem{}
		}
		return DriveItem{live: true}
	}
	// chan mode (tests): no drive needed, send now and return inert.
	if o.ch != nil {
		select {
		case o.ch <- v:
			o.trace.Send(o.node, o.port, v)
		default:
		}
	}
	return DriveItem{}
}

// PlaceDrivenAt is PlaceDriven with the placement tick PINNED by the caller
// (see PacedWire.placeBeadNoWalkerAt) so multiple fan-out wires placed in the
// same cycle all stamp the same placementTick instead of each independently
// re-reading the live shared clock (which can advance mid-cycle and skew
// equal-latency siblings apart by a tick). Chan mode is unaffected (no
// placementTick concept there).
func (o *Out) PlaceDrivenAt(v int, tick int64) DriveItem {
	if o == nil {
		return DriveItem{}
	}
	if o.pw != nil {
		if _, ok := o.placeDrivenNoWalkerAt(v, tick); !ok {
			return DriveItem{}
		}
		return DriveItem{live: true}
	}
	// chan mode (tests): no drive needed, send now and return inert.
	if o.ch != nil {
		select {
		case o.ch <- v:
			o.trace.Send(o.node, o.port, v)
		default:
		}
		return DriveItem{}
	}
	return DriveItem{}
}

// OutMulti is a fanout port: a slice of Outs sharing one logical name.
type OutMulti []*Out

// PlaceDrivenAllAt places value v (no walker) on EVERY Out in the set, emitting
// the SendWire trace for each and appending a DriveItem per Out to dst, with
// the placement tick PINNED by the caller (see PacedWire.placeBeadNoWalkerAt /
// Out.PlaceDrivenAt) so every element of this fan-out set stamps the SAME
// placementTick instead of each independently re-reading the live shared clock
// across sequential placements. Delivery is driven by the caller's per-cycle
// StepOnce on each wire, so the whole fan-out animates concurrently. Chan-mode
// Outs send immediately and contribute inert items.
func (outs OutMulti) PlaceDrivenAllAt(v int, tick int64, dst []DriveItem) []DriveItem {
	for _, o := range outs {
		if o == nil {
			continue
		}
		dst = append(dst, o.PlaceDrivenAt(v, tick))
	}
	return dst
}

// NewInPaced / NewOutPaced are used by the loader. Uses PacedWire mode.
func NewInPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace) *In {
	return &In{pw: pw, ctx: ctx, node: node, port: port, trace: tr}
}

// NewPacedOutNoGeom builds a paced Out with a zero wire segment. Node packages
// outside Wiring cannot name the unexported wireSegment, so they cannot call
// NewOutPaced directly — this is the supported entry point for tests that need to
// exercise the paced OUTPUT drive (PlaceDriven → StepOnce) under a
// RealClock. Only bead timing is exercised; the zero segment means position
// traces carry no geometry. Production paced Outs are built by the loader/builders
// with real segments, not through this.
func NewPacedOutNoGeom(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace, rule SendRule, arcLength, simLatencyMs float64, edgeLabel string) *Out {
	return NewOutPaced(pw, ctx, node, port, tr, rule, arcLength, simLatencyMs, wireSegment{}, edgeLabel)
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
