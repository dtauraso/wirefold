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
	// apply is called synchronously by Handle with this node's freshly computed
	// new world center and updated iR. It performs the single write of this
	// node's position: it hands the update to this node's OWN nodeMover via the
	// existing sendMove/inbox channel (node_move.go) — the one place that
	// already exclusively mutates that mover's held geom/snap — and schedules
	// the iR persist. This node's Update() goroutine is the SOLE DECIDER of the
	// new position (it computes newCenter/newIR here in Handle); routing the
	// actual mutation through the mover's own channel keeps the existing
	// single-writer invariant on nodeMover.geom intact (no second goroutine
	// touches it directly), so there is no dual-writer race even though the
	// decision now originates on a different goroutine per node. nil in tests
	// built without a loader.
	apply func(center vec3, iR int)
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
