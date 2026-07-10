package Wiring

// LayoutMsg is the payload carried on the hidden layout graph — a parallel
// edge set that mirrors the domain graph one-for-one (source -> target),
// carrying layout/drag messages instead of beads. SLICE 1 payload is just a
// visited-set so propagation can terminate on a cycle; iR/radius fields land
// in a later slice.
type LayoutMsg struct {
	Visited map[string]bool
}

// clone returns a shallow copy of msg with its own Visited map, so forwarding
// to multiple outgoing edges never lets one branch's mutation leak into
// another's.
func (msg LayoutMsg) clone() LayoutMsg {
	v := make(map[string]bool, len(msg.Visited))
	for k, val := range msg.Visited {
		v[k] = val
	}
	return LayoutMsg{Visited: v}
}

// LayoutPort is the per-node hidden-layout-graph plumbing: one inbound channel
// and a set of outbound channels mirroring this node's domain out-edges.
// Injected onto every node struct by the loader, the same way EmitGeometry is
// injected (see builders.go injectClosures / reflectBuild). Node kinds poll it
// non-blockingly inside their existing Update loop.
type LayoutPort struct {
	id  string
	in  chan LayoutMsg
	out []chan LayoutMsg
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

// Handle is SLICE-1 propagation behavior: if this node is already marked
// visited in msg, the wave terminates here (breaks cycles). Otherwise it
// marks this node visited and forwards a copy to every outgoing layout edge,
// non-blocking.
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
	for _, out := range p.out {
		fwd := msg.clone()
		select {
		case out <- fwd:
		default:
		}
	}
}
