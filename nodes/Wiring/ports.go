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

// TryRecv in chan mode: non-blocking select. In paced mode: blocks until
// a value is placed or ctx is cancelled.
func (i *In) TryRecv() (int, bool) {
	if i == nil {
		return 0, false
	}
	if i.pw != nil {
		v, err := i.pw.Recv(i.ctx)
		if err != nil {
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

// SimLatencyMs reports the input wire's traversal latency in milliseconds
// (arcLength / pulseSpeed), or 0 in chan mode (no wire geometry). Windowed nodes
// use this to derive their coincidence window W from current geometry.
func (i *In) SimLatencyMs() float64 {
	if i == nil || i.pw == nil {
		return 0
	}
	return i.pw.MaxIncomingSimLatencyMs
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
// EmitOneDriven / the EmitGeometry closure). Writes happen only on the owning
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

// Gated reports whether the source node should wait for consumption after a
// successful send. Nil-safe; the zero value (empty Rule) is gated.
func (o *Out) Gated() bool {
	if o == nil {
		return true
	}
	return o.Rule != RuleFireAndForget
}

// EmitOneDriven places one bead WITHOUT spawning a walker goroutine, emits the
// same SendWire trace as EmitOne, then drives the bead to delivery synchronously
// on the caller's goroutine. Blocks until delivered or ctx is canceled.
// Use this when the node drives its own outbound edge (no walker goroutine).
func (o *Out) EmitOneDriven(ctx context.Context, v int) bool {
	if o == nil {
		return false
	}
	if o.pw != nil {
		g := o.Geom()
		gen, ok := o.pw.placeBeadNoWalker(v, o.placementFrom(g))
		if !ok {
			return false
		}
		o.trace.SendWire(o.node, o.port, v, g.ArcLength, g.SimLatencyMs, o.pw.Target, o.pw.TargetHandle)
		o.pw.DriveBeadToDelivery(ctx, gen)
		return true
	}
	// chan mode (tests): fall back to EmitOne behavior (no drive needed).
	if o.ch == nil {
		return false
	}
	select {
	case o.ch <- v:
		o.trace.Send(o.node, o.port, v)
		return true
	default:
		return false
	}
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

// InFlight reports whether a bead is currently traversing the underlying wire.
// Returns false in chan mode (no wire geometry / no in-flight concept).
// Nil-safe; returns false for a nil Out.
func (o *Out) InFlight() bool {
	if o == nil || o.pw == nil {
		return false
	}
	return o.pw.InFlight()
}

// DriveItem is an exported handle to one placed-but-not-yet-driven bead. A node
// that drives several outbound edges on its OWN goroutine accumulates a set of
// these (each carrying a SendWire trace already emitted at placement time) and
// then drives them ALL concurrently in one DriveAll call, so the beads animate in
// parallel rather than one blocking emit at a time. The zero value (placement
// failed / chan mode) is inert and DriveAll skips it.
type DriveItem struct {
	item driveItem
	live bool
}

// PlaceDriven places one bead on this Out WITHOUT spawning a walker, emits the
// SendWire trace, and returns a DriveItem the caller drives later via DriveAll.
// In chan mode (tests) it sends immediately on the raw channel and returns an inert item,
// so unit tests keep their synchronous chan semantics. A nil Out, or a failed
// placement (faded/deleted), returns an inert item.
func (o *Out) PlaceDriven(v int) DriveItem {
	if o == nil {
		return DriveItem{}
	}
	if o.pw != nil {
		g := o.Geom()
		gen, ok := o.pw.placeBeadNoWalker(v, o.placementFrom(g))
		if !ok {
			return DriveItem{}
		}
		o.trace.SendWire(o.node, o.port, v, g.ArcLength, g.SimLatencyMs, o.pw.Target, o.pw.TargetHandle)
		return DriveItem{item: driveItem{pw: o.pw, gen: gen}, live: true}
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

// DriveAll drives every live DriveItem to delivery in lockstep on the calling
// goroutine — no extra goroutines, beads animate concurrently — and blocks until
// all are delivered or ctx is canceled. Inert items (chan mode / failed placement)
// are skipped. With an empty/all-inert set it returns immediately.
func DriveAll(ctx context.Context, items []DriveItem) {
	var live []driveItem
	for _, di := range items {
		if di.live {
			live = append(live, di.item)
		}
	}
	DriveBeadsToDelivery(ctx, live)
}

// OutMulti is a fanout port: a slice of Outs sharing one logical name.
type OutMulti []*Out

// PlaceDrivenAll places value v (no walker) on EVERY Out in the set, emitting the
// SendWire trace for each, and appends a DriveItem per Out to dst. The caller
// combines these with other ports' items and drives them together via DriveAll so
// the whole fan-out animates concurrently. Chan-mode Outs send immediately and
// contribute inert items.
func (outs OutMulti) PlaceDrivenAll(v int, dst []DriveItem) []DriveItem {
	for _, o := range outs {
		if o == nil {
			continue
		}
		dst = append(dst, o.PlaceDriven(v))
	}
	return dst
}

// EmitManyDriven places one bead on EVERY Out in the set WITHOUT spawning walker
// goroutines, emits the SendWire trace for each, then drives ALL beads to delivery
// in lockstep on the calling goroutine and waits until every bead is delivered or
// ctx is canceled. In chan mode (tests), falls back to EmitOne on each Out.
// If the set is empty or all placements fail, returns immediately.
func (outs OutMulti) EmitManyDriven(ctx context.Context, v int) {
	DriveAll(ctx, outs.PlaceDrivenAll(v, nil))
}

// NewIn / NewOut are exported for tests that construct nodes directly
// without going through reflectBuild. Uses chan mode.
func NewIn(ch <-chan int, node, port string, tr *T.Trace) *In {
	return &In{ch: ch, node: node, port: port, trace: tr}
}

func NewOut(ch chan<- int, node, port string, tr *T.Trace) *Out {
	return &Out{ch: ch, node: node, port: port, trace: tr, Rule: RuleConsumeGated}
}

// NewInPaced / NewOutPaced are used by the loader. Uses PacedWire mode.
func NewInPaced(pw *PacedWire, ctx context.Context, node, port string, tr *T.Trace) *In {
	return &In{pw: pw, ctx: ctx, node: node, port: port, trace: tr}
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
