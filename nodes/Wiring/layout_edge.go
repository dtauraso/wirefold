package Wiring

import "context"

// LayoutMsg is the payload carried on the hidden layout graph — a parallel
// edge set that mirrors the domain graph one-for-one (source -> target),
// carrying layout/drag messages instead of beads. The radius cascade carries
// ONLY the new radius value (IR) — TRANSPORT is decoupled from GEOMETRY: each
// receiving node computes its own world position about ITS OWN REFERENCE
// (never about the sender), so no FromCenter is carried and no Visited table
// is needed — termination is a fixpoint on IR (see Handle).
type LayoutMsg struct {
	IR int
	// PropagatingKind names the node kind ALLOWED TO PROPAGATE (forward) this
	// cascade — who may SEND the message onward. Set once by the seed to the
	// dragged node's kind and carried unchanged through forwarding; a receiving
	// node forwards only if its own kind equals this.
	PropagatingKind string
	// UpdateKinds is the SET of node kinds allowed to UPDATE (reposition) on
	// receiving this cascade — who may UPDATE, a separate axis from who may send.
	// Set by the propagating timer node when the message reaches it; for a timer
	// cascade it is {timer, pulse}. A receiver applies only if its kind is in this
	// set (constant-time lookup), so receiving alone no longer means "move me".
	UpdateKinds map[string]bool
	// Direct marks a DIRECT position set (SLICE 3: the drag origin's own new center,
	// delivered by node_move.go RootMove/fanCenters via LayoutPort.InjectDirect) rather
	// than a radius-cascade hop. A direct message is applied verbatim to THIS node
	// (DirectCenter/DirectReach) — no polar-offset math, no visited-marking, no
	// forwarding — and is handled on this node's own Update() goroutine, same as a
	// cascade hop, so it is the single writer of this node's position on drag too.
	Direct       bool
	DirectCenter vec3
	DirectReach  float64
}

// cascadeTimerKind is the FIXED node kind allowed to propagate (forward) a
// radius cascade — the timer kind, not derived from whichever node happens to
// be dragged.
const cascadeTimerKind = "HoldNewSendOld"

// cascadeUpdateKinds returns a fresh set of node kinds allowed to UPDATE
// (reposition) on a radius cascade: the timer kind itself, plus Pulse and
// Hold. Fixed, not derived from the dragged node's kind. Returns a fresh map
// each call so callers may hold onto it without aliasing concerns.
func cascadeUpdateKinds() map[string]bool {
	return map[string]bool{cascadeTimerKind: true, "Pulse": true, "Hold": true}
}

// LayoutPort is the per-node hidden-layout-graph plumbing: one inbound channel
// and a set of outbound channels mirroring this node's domain out-edges.
// Injected onto every node struct by the loader, the same way EmitGeometry is
// injected (see builders.go injectClosures / reflectBuild). Node kinds poll it
// non-blockingly inside their existing Update loop — so Handle always runs on
// THIS node's own Update() goroutine, which is what makes it the sole decider
// of this node's cascaded position (see the ownership note on apply below).
type LayoutPort struct {
	id  string
	in  chan LayoutMsg
	out []chan LayoutMsg

	// iTheta/iPhi are this node's fixed angular offset (about its reference),
	// loaded at build time (quantized_layout.go quantizedOffset) and never
	// mutated by the cascade — only iR changes on a radius propagation.
	iTheta, iPhi int
	// iR is this node's current radial step about its reference/forwarding
	// parent. It starts at the node's loaded value and is overwritten by every
	// cascade hop this node's OWN Update() goroutine handles. Only that one
	// goroutine ever touches it (Handle runs exclusively on this node's own
	// Update loop), so no lock is needed.
	iR int
	// kind is this node's kind name (e.g. "HoldNewSendOld"), loaded at build time.
	// A node forwards a cascade only when its kind equals the message's
	// PropagatingKind (Handle's propagation gate).
	kind string
	// apply is called synchronously by Handle (on THIS node's own Update()
	// goroutine — see the type doc above) with ONLY the new radius (iR). apply
	// itself computes this node's freshly-positioned world center about ITS OWN
	// REFERENCE (node_move.go MoveDispatch.applyLayoutCenter), never about any
	// sender-provided center — this is the transport/geometry decoupling: the
	// message carries a number, geometry is entirely local to the node. It also
	// schedules the iR persist. A plain in-process function call on the CALLER's
	// goroutine — no channel hop through nodeMover's own goroutine, because
	// nodeMover no longer runs one for centers. Since apply is only ever invoked
	// from Handle/InjectDirect's consumer, both of which run on this node's own
	// Update() goroutine, this node's own goroutine is the SOLE writer of its
	// position (single-writer by construction, not by mutex). nil in tests built
	// without a loader.
	apply func(iR int)
	// applyDirect is called synchronously by Handle for a Direct message (the
	// drag origin's own new center, delivered via InjectDirect) with the exact
	// world center + reach RootMove computed on the stdin goroutine. Like apply,
	// it performs the position write in-process on THIS node's own Update()
	// goroutine — the drag-origin counterpart to the radius-cascade apply above,
	// completing the single-writer invariant for both position sources (SLICE 3).
	// nil in tests built without a loader.
	applyDirect func(center vec3, reach float64)
}

// layoutPortBufferCap is the buffered capacity of a LayoutPort's inbound
// channel. Layout messages are infrequent (drag-driven), so a small buffer is
// enough to avoid a slow reader stalling a fast sender's non-blocking send.
const layoutPortBufferCap = 64

// NewLayoutPort creates a LayoutPort for the node with the given id.
func NewLayoutPort(id string) *LayoutPort {
	return &LayoutPort{
		id: id,
		in: make(chan LayoutMsg, layoutPortBufferCap),
	}
}

// connectTo mirrors one domain edge (this node -> dst) onto the hidden layout
// graph: dst's inbound channel is appended to this port's outbound set, so a
// message forwarded by this port is delivered to dst.
func (p *LayoutPort) connectTo(dst *LayoutPort) {
	if p == nil || dst == nil {
		return
	}
	p.out = append(p.out, dst.in)
}

// TryRecv is a non-blocking receive off this port's inbound channel.
func (p *LayoutPort) TryRecv() (LayoutMsg, bool) {
	if p == nil {
		return LayoutMsg{}, false
	}
	select {
	case msg := <-p.in:
		return msg, true
	default:
		return LayoutMsg{}, false
	}
}

// run is this node's dedicated LAYOUT goroutine: it blocks on the inbound
// channel and applies each layout/drag message via Handle until ctx is done.
// This is the ALWAYS-ON half of the two-goroutine node
// (docs/planning/visual-editor/split-layout-bead-goroutines.md): position
// handling lives here, NOT in the node's bead Update() loop, so play/pause —
// which governs only the bead goroutine — never affects dragging. Because
// Handle -> apply/applyDirect -> nodeMover.applyCenter runs on THIS goroutine
// and nothing else writes the node's position, this goroutine is the SOLE
// writer of the node's center (single-writer by construction). Launched once
// per node by MoveDispatch.Start. A blocking receive (not a poll) means it
// parks at zero cost until a drag arrives.
func (p *LayoutPort) run(ctx context.Context) {
	if p == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-p.in:
			p.Handle(msg)
		}
	}
}

// Inject places msg onto this port's OWN inbound channel, non-blocking. This
// is the entry point for a drag origin (or a test) to start a propagation at
// this node.
func (p *LayoutPort) Inject(msg LayoutMsg) {
	if p == nil {
		return
	}
	select {
	case p.in <- msg:
	default:
	}
}

// InjectDirect places a DIRECT position-set message on this port's OWN inbound
// channel, non-blocking: the drag origin's freshly computed world center + reach
// (node_move.go RootMove/fanCenters), to be applied on THIS node's own Update()
// goroutine when it next drains its layout port (Handle below). This is the
// drag-origin counterpart to a radius-cascade hop landing via Inject/Handle —
// together they are the two position sources that route to the node goroutine
// (SLICE 3, layout-on-domain-network.md).
func (p *LayoutPort) InjectDirect(center vec3, reach float64) {
	if p == nil {
		return
	}
	select {
	case p.in <- LayoutMsg{Direct: true, DirectCenter: center, DirectReach: reach}:
	default:
	}
}

// Handle is the radius-cascade behavior. The message carries only a new
// radius value (IR); it never carries a sender-provided world center — TERM:
// this is the transport/geometry decoupling this model rests on. Termination
// is a FIXPOINT on IR (replacing the old visited table): if this node is
// already at the propagated radius, the wave has already passed through here
// and stops. Otherwise, if this node's kind is in UpdateKinds, it adopts the
// new radius and repositions ITSELF about ITS OWN REFERENCE (p.apply, which
// looks up this node's own reference center — never the sender's), and, only
// if this node's kind matches PropagatingKind (the fixed cascade timer kind),
// forwards the (unchanged) radius to every outgoing layout edge.
func (p *LayoutPort) Handle(msg LayoutMsg) {
	if p == nil {
		return
	}
	if msg.Direct {
		// Drag-origin direct set: apply verbatim, no cascade math, no forwarding —
		// this message never propagates past the dragged node itself.
		if p.applyDirect != nil {
			p.applyDirect(msg.DirectCenter, msg.DirectReach)
		}
		return
	}
	// Fixpoint termination: already at this radius, nothing to do.
	if p.iR == msg.IR {
		return
	}
	// Application (reposition) is gated by UpdateKinds: a receiver updates its own
	// position ONLY if its kind is in the message's UpdateKinds set. Merely
	// receiving the message does not mean "move me".
	if !msg.UpdateKinds[p.kind] {
		return
	}
	p.iR = msg.IR
	if p.apply != nil {
		p.apply(msg.IR)
	}

	// Propagation is gated by PropagatingKind (separate from UpdateKinds): only a
	// node whose OWN kind matches the message's PropagatingKind (the fixed cascade
	// timer kind) may forward this cascade further — who may SEND, not who updates.
	if p.kind != msg.PropagatingKind {
		return
	}
	for _, out := range p.out {
		fwd := LayoutMsg{IR: msg.IR, PropagatingKind: msg.PropagatingKind, UpdateKinds: msg.UpdateKinds}
		select {
		case out <- fwd:
		default:
		}
	}
}
