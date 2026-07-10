package Wiring

// LayoutMsg is the payload carried on the hidden layout graph — a parallel
// edge set that mirrors the domain graph one-for-one (source -> target),
// carrying layout/drag messages instead of beads. SLICE 2 payload adds the
// radius (IR) cascade: the propagated new iR and the forwarding parent's NEW
// world center (FromCenter), which together let the receiving node compute
// its own new world center as a plain local polar offset about the parent.
type LayoutMsg struct {
	Visited    map[string]bool
	IR         int
	FromCenter vec3
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

// clone returns a shallow copy of msg with its own Visited map, so forwarding
// to multiple outgoing edges never lets one branch's mutation leak into
// another's.
func (msg LayoutMsg) clone() LayoutMsg {
	v := make(map[string]bool, len(msg.Visited))
	for k, val := range msg.Visited {
		v[k] = val
	}
	return LayoutMsg{Visited: v, IR: msg.IR, FromCenter: msg.FromCenter}
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
	// isTimeNode marks a HoldNewSendOld node: only a time node forwards a
	// cascade past itself (quantized_layout.go / layout-on-domain-network.md).
	isTimeNode bool
	// apply is called synchronously by Handle (on THIS node's own Update()
	// goroutine — see the type doc above) with this node's freshly computed new
	// world center and updated iR, and schedules the iR persist. SLICE 3: apply
	// now performs the position write itself (node_move.go MoveDispatch.
	// applyLayoutCenter -> nodeMover.applyCenter), a plain in-process function
	// call on the CALLER's goroutine — no channel hop through nodeMover's own
	// goroutine, because nodeMover no longer runs one for centers. Since apply is
	// only ever invoked from Handle/InjectDirect's consumer, both of which run on
	// this node's own Update() goroutine, this node's own goroutine is the SOLE
	// writer of its position (single-writer by construction, not by mutex). nil
	// in tests built without a loader.
	apply func(center vec3, iR int)
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

// Inject places msg onto this port's OWN inbound channel, non-blocking. This
// is the entry point for a drag origin (or a test) to start a propagation at
// this node.
func (p *LayoutPort) Inject(msg LayoutMsg) {
	if p == nil {
		return
	}
	if msg.Visited == nil {
		msg = LayoutMsg{Visited: map[string]bool{}}
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

// Handle is the SLICE-2 radius-cascade behavior: if this node is already
// marked visited in msg, the wave terminates here (breaks cycles). Otherwise
// it computes this node's own new world center as a PLAIN local polar offset
// about the forwarding parent's new center (msg.FromCenter) — the same
// formula as snapToReference, NOT the rotated forward-kinematics compose path
// in quantized_layout.go — adopts the propagated iR, applies the position
// (geom write + snap publish + emit + persist, via p.apply), and then, ONLY
// if this node is a time node (HoldNewSendOld), forwards a copy carrying its
// OWN new center and the same iR to every outgoing layout edge. A non-time
// node re-places itself but does not forward — the cascade wave terminates on
// that branch.
func (p *LayoutPort) Handle(msg LayoutMsg) {
	if p == nil {
		return
	}
	if msg.Direct {
		// Drag-origin direct set (SLICE 3): apply verbatim, no cascade math, no
		// visited-marking, no forwarding — this message never propagates past the
		// dragged node itself.
		if p.applyDirect != nil {
			p.applyDirect(msg.DirectCenter, msg.DirectReach)
		}
		return
	}
	if msg.Visited == nil {
		msg.Visited = map[string]bool{}
	}
	if msg.Visited[p.id] {
		return
	}
	msg.Visited[p.id] = true

	newIR := msg.IR
	newCenter := msg.FromCenter.add(polar2cart(polar{
		R:     float64(newIR) * stepR,
		Theta: float64(p.iTheta) * stepTheta,
		Phi:   float64(p.iPhi) * stepPhi,
	}))
	p.iR = newIR
	if p.apply != nil {
		p.apply(newCenter, newIR)
	}

	if !p.isTimeNode {
		return
	}
	for _, out := range p.out {
		fwd := msg.clone()
		fwd.IR = newIR
		fwd.FromCenter = newCenter
		select {
		case out <- fwd:
		default:
		}
	}
}

// SeedForward pushes msg directly onto every outgoing layout edge of THIS
// node, marking this node visited first (cycle guard), without applying any
// position update to this node itself. Used to seed a radius cascade from a
// drag's REFERENCE node: the reference's own center does not change, only its
// children are re-placed (node_move.go RootMove / SeedLayoutCascade).
func (p *LayoutPort) SeedForward(msg LayoutMsg) {
	if p == nil {
		return
	}
	if msg.Visited == nil {
		msg.Visited = map[string]bool{}
	}
	msg.Visited[p.id] = true
	for _, out := range p.out {
		fwd := msg.clone()
		select {
		case out <- fwd:
		default:
		}
	}
}
