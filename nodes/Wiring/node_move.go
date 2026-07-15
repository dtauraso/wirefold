// node_move.go — decentralized node-move handling.
//
// A node-move is NOT handled by a central coordinator. Instead the loader builds
// a pure dispatch registry (key → inbox channel) whose keys are BOTH node ids AND
// edge ids (the edge label, which encodes the two connected nodes). The stdin
// reader's whole job for a move is a mail-sort: for each (key,value) entry in the
// message, push value onto channels[key]. No recompute, no topology logic lives in
// the reader.
//
// Two kinds of mover own the work, each in its own goroutine (MODEL.md: each node
// and each wire is a goroutine; geometry emission is per-goroutine —
// memory/feedback_per_goroutine_bridge.md):
//
//   - nodeMover: owns ONE node's geometry. On a move for itself it updates its own
//     held position and re-emits its own node-geometry (emitNodeGeometry).
//   - edgeMover: owns ONE edge. It holds BOTH endpoint nodeGeoms (set at load). On
//     a move of either endpoint it updates the matching endpoint, recomputes its
//     OWN segment + arc (segmentBetweenPorts/arcLengthBetweenPorts), writes them
//     onto the source Out (next placement reads them), revises any in-flight bead's
//     remaining travel on the dest wire (ReviseInFlightGeometry, fraction-preserving),
//     emits its OWN edge geometry (tr.Geometry), and updates its contribution to the
//     dest port's MaxIncomingSimLatencyMs aggregate (PacedWire.SetIncomingLatency,
//     which recomputes the max over ALL feeding edges).
//
// This reproduces, per-goroutine, exactly what the old central applyNodeMove did in
// one stdin goroutine: same node-geometry emit, same per-edge segment/arc recompute,
// same in-flight revision, same edge-geometry emit, same latency aggregate.

package Wiring

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// moveMsgKind discriminates moveMsg payloads.
const (
	// The node-move kind ("move", the zero value "") carries no payload and is a
	// no-op in every mover switch, so it has no constant — the switches simply
	// fall through. The remaining kinds each select a distinct payload.
	moveMsgKindFade    = "fade"
	moveMsgKindAnchor  = "anchor"  // per-port anchor update (drag along the ring)
	moveMsgKindCenter  = "center"  // polar-layout re-propagated world center for one node
	moveMsgKindCenters = "centers" // batched centers for an edge: update both endpoints, recompute ONCE
	moveMsgKindResend  = "resend"  // re-emit this mover's held geometry on its own goroutine
	// moveMsgKindEqualize and moveMsgKindTrigger implement the STEP 1 decentralized
	// node-5 cascade: node-to-node messages routed via
	// sendMove instead of a central recursive rootMove. See ruleFollowers/ruleSource
	// below for the (currently hardcoded, 5-chain-scoped) per-node rule tables.
	moveMsgKindEqualize = "equalize" // follower move: receiver moves ITSELF to length TargetC from FromCenter, then stops (no rule, no forward)
	moveMsgKindTrigger  = "trigger"  // rule-node trigger: receiver's source edge changed; receiver re-derives L, equalizes its own followers, and (delta-gated) forwards to any further rule-neighbor
	// moveMsgKindGatePlace is node 6's cascade, decentralized: node
	// 6 sends its two gate neighbors (9, 10) the anchor center (node 6's own fresh center)
	// and the shortest c-distance d (TargetC) it computed to them; the receiver (9 or 10,
	// via gateNeighbors) runs placeAtDistanceFromBoth against ITS two fixed neighbors
	// (the anchor + its OTHER neighbor) on its OWN goroutine, then moveNodeAndSetEdgeCs to
	// land there and set both edge-c records to SnapC. SenderID names the anchor node
	// (always "6").
	moveMsgKindGatePlace = "gatePlace"
	// moveMsgKindDrag is node 6's own-goroutine drag entry:
	// the drag itself is routed to node 6's OWN inbox instead of the stdin reader
	// committing on node 6's behalf. The receiver (node 6) commits its OWN new
	// position via the owner-goroutine commit path (commitNodeMoveLocal, which
	// publishes its snap SYNCHRONOUSLY via applyCenter) and then runs its own
	// self-trigger (handleTrigger) — apply-then-fan as two sequential statements on
	// one goroutine, so the fan reads the freshly-applied center, never a stale one.
	// Scoped to node 6 only: node 6 is a FREE move (not in gateNeighbors), so there is
	// no equal-radii solve to run before committing.
	moveMsgKindDrag = "drag"
)

// ruleSource maps a rule-node id to its designated SOURCE neighbor ("per-node rule").
// HARDCODED to the current 10-node topology: a graph without these ids silently gets no
// cascade, because the rule lives here rather than in the spec the graph came from.
var ruleSource = map[string]string{
	"5": "2",
	"2": "5",
	"1": "2",
}

// ruleFollowers maps a rule-node id to the FOLLOWER neighbors it repositions (via an
// Equalize message) whenever it is triggered. Followers have no rule of their own — they
// move themselves to the new length and stop.
var ruleFollowers = map[string][]string{
	"5": {"7", "8"},
	"2": {"6"},
	"1": {"3"},
}

// gateNeighbors maps a two-neighbor GATE node id to its two FIXED neighbors (a,b), for
// the decentralized gate self-trigger path. A gate node
// is NOT a ruleSource/follower node: on a direct drag it solves its own equal-radii
// landing position, commits itself, and self-triggers its OWN goroutine to run its
// own edge-c equalize (equalizeEdgeCLocal) — no forward, no neighbor move. Scoped to
// nodes 9 and 10 (mirroring node9's widening).
var gateNeighbors = map[string][2]string{
	"1":  {"2", "3"},
	"9":  {"3", "6"},
	"10": {"6", "8"},
}

// centerSnap is an immutable snapshot of a node's position published by the nodeMover
// via an atomic.Pointer so readers on other goroutines (stdin reader, etc.) can observe
// the current center without touching the mover's live geom.
type centerSnap struct {
	c     vec3  // world center — cartesian, published for the emit/input boundaries only
	p     polar // scene polar (r,θ,φ about the scene center) — the polar source of truth for geometry math
	reach float64
}

// moveMsg is one entry routed to a mover's inbox. kind selects the payload:
//   - "" or "move": node-move — currently a no-op (polar-layout positions all nodes via "center" messages).
//   - "fade":       per-wire fade — Faded applied by edgeMover only (nodeMover ignores).
//
// ack (if non-nil) is closed by the mover after it has fully handled the message —
// used only by the synchronous test façade. The live bridge path leaves ack nil.
type moveMsg struct {
	Kind   string
	NodeID string
	Faded  bool
	// Anchor payload (Kind == "anchor"): identify the port whose anchor changed.
	// Port/IsInput name the port on NodeID; AnchorId is the snapped ring-anchor index
	// (Go snaps from the incoming world-space direction; TS never computes the index).
	Port     string
	IsInput  bool
	AnchorId int
	// Center (Kind == "center"): the re-propagated world center for NodeID under the
	// polar layout. Each owning node/edge goroutine writes it onto its held geom
	// and re-emits its own geometry. RootMove updates one node centrally and
	// fans the fresh centers out — the one centralized step (sphere_layout.go).
	Center *vec3
	// Centers (Kind == "centers"): batched per-edge re-propagation. Maps node id → new
	// world center for every moved endpoint of THIS edge in a single frame, so an edge
	// whose BOTH endpoints moved updates both and recomputes/emits its segment ONCE
	// instead of once per endpoint message (the node-2 drag duplicate-emit fix).
	Centers map[string]vec3
	// ReachR (Kind == "center"): the re-propagated sphere REACH radius for NodeID (max
	// distance to a surface child under the new centers). The nodeMover writes it onto its
	// held geom so the re-emitted node-geometry streams the correct sphereR during a drag.
	ReachR float64
	// FromCenter / TargetC (Kind == "equalize"): the follower's neighbor's new world
	// center and the c-length to hold. The receiver moves ITSELF to
	// FromCenter + unit(self_old - FromCenter) * TargetC — receiver-computes.
	FromCenter vec3
	TargetC    float64
	// SenderID (Kind == "trigger"): the id that sent this trigger, so the receiving
	// rule-node does not forward the trigger back to whoever just triggered it.
	// Also used, overloaded, by Kind == "gatePlace" to name the ANCHOR node (always "6")
	// so the receiver (9 or 10) can find its OTHER fixed neighbor via gateNeighbors.
	SenderID string
	// SnapC (Kind == "gatePlace"): the propagated shortest c (whole ticks of localStepR)
	// to write onto both of the receiver's edge-c records via moveNodeAndSetEdgeCs.
	SnapC int
	// Target (Kind == "drag"): the raw drag target world position for NodeID's
	// owner-goroutine commit. Node 6 is a free move,
	// so this is committed as-is (no gate solve).
	Target vec3
	ack    chan struct{}
}

// setPortAnchorId sets the AnchorId on the named port within the given geom,
// clearing any free-direction Anchor so AnchorId takes highest priority (matching
// portDir's resolution order: AnchorId > Anchor > side/slot). Returns true if the
// port was found and mutated. The geom is mutated in place (its slice elements are
// addressable). Used by both movers to apply a snapped ring-anchor update.
func setPortAnchorId(g *nodeGeom, port string, isInput bool, anchorId int) bool {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	for i := range list {
		if list[i].Name == port {
			list[i].AnchorId = &anchorId
			return true
		}
	}
	return false
}

// nodeMover owns one node's geometry. It reads its inbox in its own goroutine and,
// on a move for itself, updates its held position and re-emits its node-geometry.
type nodeMover struct {
	id    string
	geom  nodeGeom
	inbox chan moveMsg
	tr    *T.Trace
	// geomMu guards m.geom against the two remaining goroutines that touch it
	// concurrently since SLICE 3: this node's OWN Update() goroutine (applyCenter,
	// the sole writer of position fields) and nodeMover's own inbox-drain goroutine
	// (handle's anchor/resend/default cases, the sole writer of port-anchor fields
	// and the reader for every re-emit). Position is still single-writer by
	// construction (only applyCenter ever writes it); this mutex exists purely so
	// emitGeometry's full-struct read on one goroutine never races a concurrent
	// field write on the other — it is NOT a second position-writer.
	geomMu sync.Mutex
	// snap is an atomically-published immutable snapshot of this node's current
	// center+reachR. Written only by the mover's own goroutine after every center
	// update; read by any goroutine (stdin reader) to observe the current position
	// without crossing into the mover's live geom.
	snap atomic.Pointer[centerSnap]
	// sendMove routes a moveMsg to another id's inbox by id-lookup against md.dispatch (a
	// read-only directory after construction — no queue, no shared mutable state).
	sendMove func(id string, msg moveMsg)
	edgeIDs  []string
	// centerOf resolves another node's current world center, bound to
	// md.centerOfNode. Used by the decentralized rule-node trigger handling
	// (moveMsgKindTrigger) to read its source/rule-neighbor centers without a
	// central coordinator computing them.
	centerOf func(id string) (vec3, bool)
	// commitLocal is the OWNER-GOROUTINE commit path, bound to
	// md.commitNodeMoveLocal (generalized to every
	// node). It publishes this node's own snap SYNCHRONOUSLY via applyCenter instead
	// of enqueuing an async self-send, so it is safe to call from THIS node's own
	// handle() and immediately follow with handleTrigger (moveMsgKindDrag) or return
	// (moveMsgKindEqualize) in the same call, with no cross-goroutine self-send and
	// no shared mutable state (each node's quantized offset lives on its own mover —
	// see nodeMover.quantOffset). nil in tests that build a bare nodeMover directly.
	commitLocal func(id string, newPos vec3)
	// gateEqualize runs a two-neighbor GATE node's OWN edge-c equalize
	// (equalizeEdgeCLocal) using its CURRENT committed center, bound to
	// md.gateEqualizeNode. Dispatched from handleTrigger's gate branch so the
	// c-equalize step runs on the gate node's OWN goroutine (self-trigger message)
	// instead of synchronously on the drag call stack. nil in tests that build a bare
	// nodeMover directly.
	gateEqualize func(id string)
	// gatePlace runs a gate node's (9 or 10) placeAtDistanceFromBoth + moveNodeAndSetEdgeCs
	// re-solve against an anchor node's fresh center, bound to md.gatePlaceNode. Dispatched
	// from handleTrigger's moveMsgKindGatePlace case so node 6's cascade repositions 9/10 on
	// THEIR OWN goroutines instead of synchronously on node 6's
	// drag call stack. Args: (this node's id, anchor node's id, anchor's fresh center,
	// target distance d, propagated shortest-c integer). nil in tests that build a bare
	// nodeMover directly.
	gatePlace func(id, anchorID string, anchorCenter vec3, d float64, snapC int)
	// partnerCenter resolves, per (port,isInput) on this node, the CURRENT world center of
	// the single partner node connected via one edge (aimed-port model, port_geometry.go
	// portWorldPosAimed / builders.go partnerCenterFn). Wired by newMoveDispatch from
	// b.edgeEndpoints + the OTHER nodeMover's atomic snap — a dynamic, always-current lookup
	// with no shared mutable state. nil only in tests that build a bare nodeMover directly.
	partnerCenter partnerCenterFn
	// quantOffset is THIS node's own quantized polar offset (iTheta,iPhi,iR + step
	// constants) about the scene center — the per-node replacement for the formerly
	// shared md.quantizedOffsets map, which one mover goroutine's read could race
	// another mover goroutine's write on the SAME Go map object even for different
	// keys (fatal "concurrent map read and map write"). Seeded at load
	// (buildMoveDispatch) from the computed/persisted offset, then mutated ONLY by
	// this node's own commit path (commitNodeMoveCommon, called from this node's own
	// goroutine via commitLocal/moveNodeAndSetEdgeCs) — single-writer, no map, no race.
	quantOffset quantizedOffset
	// placeEqualRadii solves this node's landing point for a drag to target, subject
	// to its two FIXED gate neighbors (gateNeighbors) having equal radii to it, bound
	// (for gate nodes 1/9/10 only) to a closure over md.placeEqualRadii + this node's
	// own neighbor ids. nil for every non-gate node. Called from THIS node's own
	// moveMsgKindDrag handler (generalized to every
	// node) so the equal-radii solve, like the commit, runs on the node's own
	// goroutine rather than the stdin goroutine.
	placeEqualRadii func(target vec3) vec3
}

func newNodeMover(id string, geom nodeGeom, tr *T.Trace) *nodeMover {
	nm := &nodeMover{id: id, geom: geom, inbox: make(chan moveMsg, 8), tr: tr}
	// Seed the atomic snapshot from the initial geometry (even when !HasPos, in which case
	// nodeWorldPos falls back to the origin) so readers — including another node's aimed-port
	// partnerCenter lookup — always have a valid center to read before the first center
	// message arrives.
	nm.snap.Store(&centerSnap{c: nodeWorldPos(geom), p: geom.ScenePolar, reach: geom.ReachR})
	return nm
}

// handle applies one move to this node: update held position, re-emit node-geometry.
func (m *nodeMover) handle(msg moveMsg) {
	if msg.NodeID != m.id {
		return
	}
	if msg.Kind == moveMsgKindAnchor {
		// Per-port anchor update: snap to ring-anchor index, mutate this node's held
		// port AnchorId, and re-emit node-geometry so the renderer redraws the port.
		m.geomMu.Lock()
		ok := setPortAnchorId(&m.geom, msg.Port, msg.IsInput, msg.AnchorId)
		m.geomMu.Unlock()
		if !ok {
			return
		}
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// nodeMover is the SOLE writer of its own position (single-writer by
		// construction — this is the only path that mutates it). A Center payload is
		// the flat absolute-scene-polar drag write from fanCenters: apply it via
		// applyCenter, which also re-emits. A nil Center is fanCenters' PARTNER
		// re-emit (an aimed-port neighbor whose OWN center is unchanged, only asked
		// to re-emit so its port direction picks up the moved partner's fresh center
		// via m.partnerCenter at emit time) — no mutation, just re-emit.
		if msg.Center != nil {
			m.applyCenter(*msg.Center, msg.ReachR)
			return
		}
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindResend {
		// Re-emit this node's geometry on our own goroutine — the inbox-routed
		// path used by ResendGeometry when movers are running.
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindEqualize {
		// Follower move (receiver-computes): move
		// SELF to FromCenter + unit(self_old - FromCenter) * TargetC, then commit
		// through the existing single-node commit path and STOP — no rule, no
		// forward.
		if s := m.snap.Load(); s != nil && m.commitLocal != nil {
			dir := s.c.sub(msg.FromCenter)
			if dir.length() != 0 {
				newPos := msg.FromCenter.add(dir.normalize().scale(msg.TargetC))
				m.commitLocal(m.id, newPos)
				m.tr.Breadcrumb("cascade.follower.move", m.id, "", fmt.Sprintf("targetC=%.4f", msg.TargetC))
			}
		}
		return
	}
	if msg.Kind == moveMsgKindTrigger {
		m.handleTrigger(msg)
		return
	}
	if msg.Kind == moveMsgKindDrag {
		// Owner-goroutine drag entry (generalized to
		// EVERY node so no node's quantized offset is ever touched by a foreign
		// mover goroutine): if this node is a two-neighbor GATE node, first solve its
		// equal-radii landing point against its FIXED neighbors (still reading their
		// centers via the goroutine-safe centerOfNode snapshot — no shared mutable
		// state), then commit this node's OWN new position via the local
		// (synchronous-snap-publish) commit path, then run this node's own
		// self-trigger — solve-then-apply-then-fan as sequential statements on this
		// one goroutine, so handleTrigger's snap.Load() below reads the
		// freshly-committed center, never a stale one.
		newPos := msg.Target
		if m.placeEqualRadii != nil {
			newPos = m.placeEqualRadii(msg.Target)
		}
		if m.commitLocal != nil {
			m.commitLocal(m.id, newPos)
		}
		if m.tr != nil {
			m.tr.Breadcrumb("cascade.root", m.id, "", fmt.Sprintf("newPos=(%.4f,%.4f,%.4f)", newPos.X, newPos.Y, newPos.Z))
		}
		m.handleTrigger(moveMsg{Kind: moveMsgKindTrigger, NodeID: m.id, SenderID: ""})
		return
	}
	if msg.Kind == moveMsgKindGatePlace {
		// Node 6's cascade: run this gate node's OWN
		// placeAtDistanceFromBoth + moveNodeAndSetEdgeCs on this node's OWN goroutine,
		// against the anchor's fresh center (msg.FromCenter) and its OTHER fixed
		// neighbor (via gateNeighbors).
		if m.gatePlace != nil {
			m.gatePlace(m.id, msg.SenderID, msg.FromCenter, msg.TargetC, msg.SnapC)
			m.tr.Breadcrumb("cascade.gateplace", m.id, "", fmt.Sprintf("anchor=%q d=%.4f", msg.SenderID, msg.TargetC))
		}
		return
	}
	if m.tr != nil {
		m.emitGeometry()
	}
}

// handleTrigger runs this rule-node's OWN rule ("per-node rule") on receiving a
// moveMsgKindTrigger: its source edge changed, so it re-derives L =
// dist(self, source), sends each of its followers an Equalize to L, and — delta-gated —
// forwards the trigger to any further node whose OWN source is this node (excluding
// whoever just sent this trigger, to avoid bouncing back). This node does NOT move itself;
// only the source moved (or, for the top-level drag, the dragged node called this via
// triggerSelf below, having already committed its own new position).
func (m *nodeMover) handleTrigger(msg moveMsg) {
	if m.id == "6" {
		// Node 6 is neither a ruleSource/ruleFollowers rule-node nor a gateNeighbors
		// gate node: it is the FIXED ANCHOR of its own cascade (// propagateShortestCFrom6's decentralized replacement). It moved freely (no
		// equal-radii solve) to its new committed position; this self-trigger computes
		// the SHORTER of its two c-distances (to 9, to 10, each rounded to a whole tick
		// of localStepR against their CURRENT centers) and sends each gate neighbor a
		// GatePlace message carrying its own fresh center + that shortest distance, so 9
		// and 10 each re-solve placeAtDistanceFromBoth on their OWN goroutines. It also
		// forwards a Trigger (SenderID=="6") to node 2, whose handleTrigger special-cases
		// SenderID=="6" below to re-equalize ITS remaining peer (node 5) against the
		// fresh 2<->6 distance — mirroring the old central case "6"'s cascade into node 2.
		if m.centerOf == nil || m.sendMove == nil {
			return
		}
		s := m.snap.Load()
		if s == nil {
			return
		}
		selfCenter := s.c
		step := localStepR
		c9, ok9 := m.centerOf("9")
		c10, ok10 := m.centerOf("10")
		if !ok9 && !ok10 {
			return
		}
		var cTo9, cTo10 float64
		if ok9 {
			cTo9 = math.Round(c9.sub(selfCenter).length() / step)
		}
		if ok10 {
			cTo10 = math.Round(c10.sub(selfCenter).length() / step)
		}
		var shortest float64
		switch {
		case ok9 && ok10:
			shortest = math.Min(cTo9, cTo10)
		case ok9:
			shortest = cTo9
		default:
			shortest = cTo10
		}
		d := shortest * step
		m.tr.Breadcrumb("cascade.node6.trigger", m.id, "", fmt.Sprintf("d=%.4f", d))
		if ok9 {
			m.sendMove("9", moveMsg{Kind: moveMsgKindGatePlace, NodeID: "9", SenderID: "6", FromCenter: selfCenter, TargetC: d, SnapC: int(shortest)})
		}
		if ok10 {
			m.sendMove("10", moveMsg{Kind: moveMsgKindGatePlace, NodeID: "10", SenderID: "6", FromCenter: selfCenter, TargetC: d, SnapC: int(shortest)})
		}
		m.sendMove("2", moveMsg{Kind: moveMsgKindTrigger, NodeID: "2", SenderID: "6"})
		return
	}
	if m.id == "2" && msg.SenderID == "6" {
		// Node 2 was NOT dragged — node 6's cascade re-triggered it (its 2<->6 distance
		// changed). Mirrors the old central case "2" origin=="6" branch: source on node
		// 6 (not node 2's normal ruleSource, node 5) and reposition ONLY the remaining
		// peer (node 5 — node 1 is permanently excluded from node 2's peer set, node 6
		// is the source so it is untouched), then STOP — no forward to 5 or 1's own
		// cascades (the old code `break`s before that tail).
		if m.centerOf == nil || m.sendMove == nil {
			return
		}
		s := m.snap.Load()
		if s == nil {
			return
		}
		sourceCenter, ok := m.centerOf("6")
		if !ok {
			return
		}
		L := sourceCenter.sub(s.c).length()
		m.sendMove("5", moveMsg{Kind: moveMsgKindEqualize, NodeID: "5", FromCenter: s.c, TargetC: L})
		m.tr.Breadcrumb("cascade.node2.from6", "5", "", fmt.Sprintf("targetC=%.4f", L))
		return
	}
	if _, isGate := gateNeighbors[m.id]; isGate && msg.SenderID == "" {
		// Gate node (self-trigger only): run this node's OWN edge-c equalize on
		// this node's OWN goroutine. No ruleSource/ruleFollowers logic applies, no
		// forward, no neighbor message — the position was already solved and
		// committed by rootMoveViaMessages before this self-trigger was sent. Node
		// 1 is ALSO a ruleSource/ruleFollowers rule-node (unlike 9/10): a FORWARDED
		// trigger into node 1 (SenderID != "", e.g. node 2's cascade) is NOT a
		// direct node-1 drag, so it must fall through to the generic ruleSource
		// logic below (equalize follower 3) instead of gating here.
		if m.gateEqualize != nil {
			m.gateEqualize(m.id)
			m.tr.Breadcrumb("gate.equalize", m.id, "", fmt.Sprintf("senderID=%q", msg.SenderID))
		}
		return
	}
	if m.centerOf == nil || m.sendMove == nil {
		return
	}
	s := m.snap.Load()
	if s == nil {
		return
	}
	selfCenter := s.c
	sourceID, ok := ruleSource[m.id]
	if !ok {
		return
	}
	sourceCenter, ok := m.centerOf(sourceID)
	if !ok {
		return
	}
	L := sourceCenter.sub(selfCenter).length()
	m.tr.Breadcrumb("cascade.trigger", m.id, "", fmt.Sprintf("source=%s L=%.4f senderID=%q", sourceID, L, msg.SenderID))
	for _, f := range ruleFollowers[m.id] {
		m.sendMove(f, moveMsg{Kind: moveMsgKindEqualize, NodeID: f, FromCenter: selfCenter, TargetC: L})
		m.tr.Breadcrumb("cascade.equalize", f, "", fmt.Sprintf("targetC=%.4f", L))
	}
	// Delta-gated forward: notify any Y whose OWN source is this node, unless Y is
	// whoever just triggered us (no bounce). This node's own center only changes
	// between this handler's calls when THIS message is the top-level self-trigger
	// issued right after a direct drag committed this node's new position
	// (SenderID == "", see rootMoveViaMessages) — a message forwarded FROM another rule-
	// node (SenderID != "") never carries a change to this node's own position, so
	// this node's edge to any Y is provably unchanged and there is nothing to
	// forward (the spec's "no message to 1" termination case). Scoped to the
	// node-5 chain (step 1): only node 5 currently self-triggers.
	selfMoved := msg.SenderID == ""
	for y, src := range ruleSource {
		if src != m.id || y == msg.SenderID {
			continue
		}
		if !selfMoved {
			m.tr.Breadcrumb("cascade.stop", m.id, "", fmt.Sprintf("target=%s reason=no-change senderID=%q", y, msg.SenderID))
			continue
		}
		m.sendMove(y, moveMsg{Kind: moveMsgKindTrigger, NodeID: y, SenderID: m.id})
		m.tr.Breadcrumb("cascade.forward", m.id, "", fmt.Sprintf("target=%s", y))
	}
}

// applyCenter is the SOLE WRITE of this node's center/reach. It is called ONLY from
// this nodeMover's own inbox-drain goroutine (handle's moveMsgKindCenter case, driven
// by fanCenters below), which is what makes that one goroutine the exclusive writer of
// m.geom/m.snap. It sets the held polar position, publishes the atomic snapshot readers
// observe cross-goroutine (stdin reader: centerOfNode/heldCenters/heldPolar/fanCenters'
// partner lookup, edgeMover's partnerCenter), and re-emits this node's live geometry.
func (m *nodeMover) applyCenter(center vec3, reach float64) {
	m.geomMu.Lock()
	setNodeWorld(&m.geom, center)
	m.geom.ReachR = reach
	scenePolar := m.geom.ScenePolar
	m.geomMu.Unlock()
	m.snap.Store(&centerSnap{c: center, p: scenePolar, reach: reach})
	if m.tr != nil {
		m.emitGeometry()
	}
}

// emitGeometry re-emits this node's authoritative geometry. A CONNECTED port marker is
// AIMED at its partner's current center (m.partnerCenter, atomic-snapshot-backed); an
// edgeless port falls back to its own polar-torus ring-anchor placement (portWorldPos).
// geomMu-guarded: takes a local copy of m.geom under the lock so the actual emit (which
// can be slow — trace serialization) does not hold the lock against the other writer.
func (m *nodeMover) emitGeometry() {
	m.geomMu.Lock()
	geom := m.geom
	m.geomMu.Unlock()
	emitNodeGeometryLocked(m.tr, m.id, geom, m.partnerCenter)
}

// run is the node's per-goroutine move loop: drain the inbox until ctx is done.
func (m *nodeMover) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.inbox:
			m.handle(msg)
			if msg.ack != nil {
				close(msg.ack)
			}
		}
	}
}

// edgeMover owns one edge. It holds both endpoint geometries and recomputes its own
// segment/arc on an endpoint move (the edge label, which keys its inbox, encodes the
// two connected nodes).
type edgeMover struct {
	edgeID  string
	srcID   string
	dstID   string
	srcH    string
	dstH    string
	srcGeom nodeGeom
	dstGeom nodeGeom
	out     *Out       // source Out for this edge (per-edge segment/arc/latency)
	dest    *PacedWire // dest wire (in-flight revision + latency aggregate)
	inbox   chan moveMsg
	tr      *T.Trace
}

func newEdgeMover(ep EdgeEndpoints, edgeID string, srcGeom, dstGeom nodeGeom, tr *T.Trace) *edgeMover {
	return &edgeMover{
		edgeID:  edgeID,
		srcID:   ep.Source,
		dstID:   ep.Target,
		srcH:    ep.SourceHandle,
		dstH:    ep.TargetHandle,
		srcGeom: srcGeom,
		dstGeom: dstGeom,
		inbox:   make(chan moveMsg, 8),
		tr:      tr,
	}
}

// handle applies one inbox message to this edge. For a "fade" message it sets its
// OWN wire's faded flag (the wire owns its flag — no central fan-out). For a move
// message it updates the matching endpoint geom, recomputes the edge's segment + arc,
// writes them onto the source Out, revises any in-flight bead, emits the new edge
// geometry, and updates the dest port's latency aggregate. A move that touches
// neither endpoint is ignored.
func (m *edgeMover) handle(msg moveMsg) {
	if msg.Kind == moveMsgKindFade {
		if m.dest != nil {
			m.dest.SetFaded(msg.Faded)
		}
		return
	}
	if msg.Kind == moveMsgKindAnchor {
		// A port-anchor change recomputes this edge's segment/arc only if the changed
		// port is one of THIS edge's endpoints (matching node id, port name, direction).
		// Source endpoint is an OUTPUT (isInput==false); target endpoint is an INPUT.
		switch {
		case msg.NodeID == m.srcID && !msg.IsInput && msg.Port == m.srcH:
			if !setPortAnchorId(&m.srcGeom, msg.Port, false, msg.AnchorId) {
				return
			}
		case msg.NodeID == m.dstID && msg.IsInput && msg.Port == m.dstH:
			if !setPortAnchorId(&m.dstGeom, msg.Port, true, msg.AnchorId) {
				return
			}
		default:
			return
		}
		m.recomputeGeometry()
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// Polar re-propagation: adopt the centrally-computed center on whichever
		// endpoint this message names, then recompute the edge.
		if msg.Center == nil {
			return
		}
		switch msg.NodeID {
		case m.srcID:
			setNodeWorld(&m.srcGeom, *msg.Center)
		case m.dstID:
			setNodeWorld(&m.dstGeom, *msg.Center)
		default:
			return
		}
		m.recomputeGeometry()
		return
	}
	if msg.Kind == moveMsgKindCenters {
		// Batched polar re-propagation: apply every moved endpoint this edge owns,
		// then recompute ONCE. An edge whose both endpoints moved in one frame would
		// otherwise recompute (and emit) twice — the duplicate-emit source on a node-2
		// drag, where the dragged node and its sphere center both move.
		moved := false
		if c, ok := msg.Centers[m.srcID]; ok {
			setNodeWorld(&m.srcGeom, c)
			moved = true
		}
		if c, ok := msg.Centers[m.dstID]; ok {
			setNodeWorld(&m.dstGeom, c)
			moved = true
		}
		if moved {
			m.recomputeGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindResend {
		// Re-emit this edge's segment on our own goroutine — the inbox-routed
		// path used by ResendGeometry when movers are running.
		m.emitGeometry()
		return
	}
	// Plain "move" messages have no effect under the polar layout;
	// position updates arrive as "center" messages instead.
	_ = msg
}

// emitGeometry re-emits this edge's segment from its held endpoint geoms without
// touching the source Out, revising in-flight beads, or updating latency aggregates.
// Used by the inbox-routed resend path (ResendGeometry when movers are running) so
// the emit happens on the edge's own goroutine without races on held geom.
func (m *edgeMover) emitGeometry() {
	if m.tr == nil {
		return
	}
	seg := edgeSegment(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	m.tr.Geometry(m.edgeID, m.srcID, m.dstID,
		seg.Start.X, seg.Start.Y, seg.Start.Z,
		seg.End.X, seg.End.Y, seg.End.Z)
}

// recomputeGeometry re-derives this edge's segment/arc/latency from its held endpoint
// geoms+handles and propagates them: write onto the source Out, revise any in-flight
// bead (fraction-preserving), update the dest port window aggregate, and emit the new
// segment so the renderer redraws the wire. Shared by node-move and port-anchor handling.
func (m *edgeMover) recomputeGeometry() {
	seg := edgeSegment(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	arc := edgeArcPolar(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	lat := arc / PulseSpeedWuPerMs

	// Publish the new per-edge segment/arc/latency onto the source Out as an immutable
	// snapshot so the next placement (on the source node goroutine) reads the new
	// segment via an atomic load — no data race with recomputeGeometry's write here.
	if m.out != nil {
		m.out.publishGeom(outGeom{ArcLength: arc, SimLatencyMs: lat, Start: seg.Start, End: seg.End})
	}
	// Re-derive an in-flight bead on this edge from the new arc + segment (no-op if
	// none in flight); the dest wire owns the bead under its own mutex.
	if m.dest != nil {
		m.dest.ReviseInFlightGeometry(arc, seg)
		// Update this edge's contribution to the dest port window aggregate; the
		// wire recomputes the max over ALL feeding edges (fan-in safe).
		m.dest.SetIncomingLatency(m.edgeID, lat)
	}
	// Emit this edge's own segment so the renderer redraws the wire from Go's endpoints.
	if m.tr != nil {
		m.tr.Geometry(m.edgeID, m.srcID, m.dstID,
			seg.Start.X, seg.Start.Y, seg.Start.Z,
			seg.End.X, seg.End.Y, seg.End.Z)
	}
}

// run is the edge's per-goroutine move loop: drain the inbox until ctx is done.
func (m *edgeMover) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.inbox:
			m.handle(msg)
			if msg.ack != nil {
				close(msg.ack)
			}
		}
	}
}

// MoveDispatch is the pure key→inbox dispatch registry built at load. Keys are BOTH
// node ids AND edge ids; the stdin reader mail-sorts each move entry to channels[key].
// It also retains the per-edge source Outs so out-of-package test/verifier callers can
// read an edge's loaded geometry (EdgeOut) without going through a central coordinator.
type MoveDispatch struct {
	dispatch   map[string]chan moveMsg
	nodeMovers map[string]*nodeMover
	edgeMovers map[string]*edgeMover
	// edgeOut: edgeId → source *Out, for read-only access by tests/verifiers.
	edgeOut map[string]*Out
	// started is set by Start; the synchronous façade uses the goroutine path when
	// true and direct handler calls otherwise (unit tests that never Start).
	started bool
	// sceneSphere is the first-class scene reference every node's SCENE polar is measured
	// about (polar-model.md, sphere_layout.go). Loaded from scene.json (or defaulted from
	// the content-fit) at startup; its Center is the one cartesian anchor. Phase 1 stores
	// it; later phases derive node world from it and move it on pan.
	sceneSphere sceneSphere
	// vp is the polar camera viewpoint state (viewpoint_state.go). Owned entirely by
	// MoveDispatch — no separate goroutine; callers serialize externally (stdin reader
	// runs in a single goroutine). MoveDispatch exposes thin delegating methods.
	vp viewpointState
	// tr is the trace sink (retained for trace emission; diagnostic breadcrumbs removed).
	tr *T.Trace
	// ov groups the 9 overlay-toggle visibility booleans and their flip/emit logic
	// (overlay_state.go). Initialized to defaults by newMoveDispatch (all true).
	// MoveDispatch exposes thin delegating methods.
	ov overlayState
	// gest is the gesture state machine (gesture.go): it consumes raw pointer/wheel input
	// and produces camera (viewpoint) + topology (node-move) changes. Owned by
	// MoveDispatch; serialized by the single-goroutine stdin reader. Zero value = idle.
	gest gestureState
	// vpPersist is the debounced camera-viewpoint persister (scene_camera_persist.go), armed
	// by EnableViewpointPersist after the startup seed. nil until armed (old path / tests).
	vpPersist *viewpointPersister
	// posPersist / anchorPersist / fadePersist are the debounced disk persisters for the
	// three FSM-applied edits (node-drag position, ring-move anchor, fade). Armed by
	// EnableEditPersist after the startup seed; nil until armed (tests that never arm).
	posPersist      *nodePosPersister
	anchorPersist   *anchorPersister
	fadePersist     *fadePersister
	overlaysPersist *overlaysPersister
	// quantOffsetPersist is the debounced disk persister for a node's scalar triple
	// (iTheta,iPhi,iR) about the scene center (quant_offset_persist.go) — the sole
	// persisted position source under the flat polar model. Armed by EnableEditPersist;
	// scheduled from RootMove for the dragged node.
	quantOffsetPersist *quantOffsetPersister
	// spherePersist is the debounced disk persister for the scene sphere (sphere_layout.go
	// md.sceneSphere), armed by EnableEditPersist. Only ever FLUSHED (handleSaveMsg): nothing
	// moves the sphere today, so the debounced schedule path has no caller — see MODEL.md's
	// "the sphere is currently immovable" note. nil until armed (tests that never arm).
	spherePersist *sceneSpherePersister
	// selected is the CURRENTLY-SELECTED node id (click-select), owned by Go. "" = nothing
	// selected. Set by the gesture FSM's click outcome (applySelect) and emitted via
	// KindSelect so the buffer snapshot marks the node's Selected column.
	selected string
	// selectedEdge is the CURRENTLY-SELECTED edge label (click-select), owned by Go. "" =
	// no edge selected. Set by the gesture FSM's click outcome (applySelect) and emitted via
	// KindSelect (Edge field) so the buffer snapshot marks the edge's Selected column.
	// Exclusive with `selected`: selecting an edge clears the node selection and vice versa.
	selectedEdge string
	// directlyFadedNodes / directlyFadedEdges are the Go-owned fade SEED sets: the node ids
	// and edge labels the user has DIRECTLY toggled faded (pressing "f" on a selection). Go
	// owns fade because it owns topology + selection. ToggleFadeSelection flips the currently-
	// selected entity's membership and emits the full seeds via KindFade; the buffer snapshot
	// mirrors them and recomputes the fade fixpoint (computeFade) each build. Fade STATE is
	// in-memory only — persistence to disk is a separate (pending) batch.
	directlyFadedNodes map[string]bool
	directlyFadedEdges map[string]bool
	// portRows resolves a numeric buffer PORT-ROW index (carried on a new-system raw hit)
	// back to its (node, port, isInput) identity. Wired to the buffer SnapshotState's
	// port-row table in main.go (new-system only); nil on the old path and in unit tests, in
	// which case the gesture FSM falls back to parsing the legacy port-id string. Go owns the
	// topology and wrote the Port block, so it — not TS — maps a port row to a (node, port).
	portRows PortRowResolver
	// edgeRows resolves a numeric buffer EDGE-ROW index (carried on a new-system raw hit)
	// back to its edge label. Wired to the buffer SnapshotState's edge-row table in main.go
	// (new-system only); nil on the old path and in unit tests, in which case the gesture
	// FSM falls back to the raw hit's Id string. Go owns the topology and wrote the Edge
	// block, so it — not TS — maps an edge row to its label.
	edgeRows EdgeRowResolver
	// nodeRows resolves a numeric buffer NODE-ROW index (carried on a new-system raw hit)
	// back to its node id. Wired to the buffer SnapshotState's node-row table in main.go
	// (new-system only); nil on the old path and in unit tests, in which case the gesture
	// FSM falls back to the raw hit's Id string. Go owns the topology and wrote the Node
	// block, so it — not TS — maps a node row to its id.
	nodeRows NodeRowResolver
	// hoverNode / hoverPort / hoverInput are the CURRENTLY-HOVERED entity (pointer hover),
	// owned by Go. The gesture FSM tracks them from the raycast hit on each pointer-move and
	// emits KindHover ONLY when they change (dedupe) so pointer-move doesn't flood the
	// snapshot. hoverPort != "" means a port is hovered (on hoverNode); otherwise hoverNode
	// (possibly "") is the hovered node. "" / "" = nothing hovered.
	hoverNode  string
	hoverPort  string
	hoverInput bool
	// quantizedLayout gates the quantized absolute-scene-polar snap (quantized_layout.go)
	// — every node is a root, measured/derived about the scene center only.
	quantizedLayout bool
	// layoutHolders resolves a node id to the *LayoutHolder embedded in that node's
	// built struct (reflection-attached by buildNodes the same way LocalPolars
	// itself is attached — see loader.go). This is the ONLY route from the drag
	// path (RootMove) to each node's own LayoutHolder; MoveDispatch does not own
	// or copy LocalPolars itself, it just routes the update to the owning node.
	layoutHolders map[string]*LayoutHolder
	// msgTap is a TEST-ONLY observability seam: when non-nil, sendMove invokes it with
	// every (destID, msg) it routes, BEFORE the send. nil in production — production code
	// never calls SetMsgTap, so msgTap.Load() is always nil there (one atomic load, no
	// lock, no allocation). This exists only so tests can assert the decentralized node-5
	// cascade is actually exchanging moveMsgKindEqualize/
	// moveMsgKindTrigger messages between movers, rather than being satisfiable by the old
	// central rootMove recursion. It is pure observation — it never authors domain state
	// or changes routing. Stored as an atomic.Pointer (not a plain field) because sendMove
	// is the one chokepoint every mover goroutine calls concurrently.
	msgTap atomic.Pointer[func(destID string, msg moveMsg)]
}

// SetMsgTap installs (or clears, with nil) the test-only message-trace hook. Test-only —
// production code never calls this.
func (md *MoveDispatch) SetMsgTap(tap func(destID string, msg moveMsg)) {
	if tap == nil {
		md.msgTap.Store(nil)
		return
	}
	md.msgTap.Store(&tap)
}

// NodeRowResolver maps a numeric buffer NODE-ROW index to its node id. Implemented by
// Buffer.SnapshotState (which wrote the Node block in this same row order). Kept as an
// interface here so the Wiring package needs no dependency on the Buffer package — main.go
// injects the concrete resolver.
type NodeRowResolver interface {
	LookupNodeRow(row int) (nodeID string, ok bool)
}

// SetNodeRowResolver injects the node-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetNodeRowResolver(r NodeRowResolver) { md.nodeRows = r }

// EdgeRowResolver maps a numeric buffer EDGE-ROW index to its edge label. Implemented by
// Buffer.SnapshotState (which wrote the Edge block in this same row order). Kept as an
// interface here so the Wiring package needs no dependency on the Buffer package — main.go
// injects the concrete resolver.
type EdgeRowResolver interface {
	LookupEdgeRow(row int) (label string, ok bool)
}

// SetEdgeRowResolver injects the edge-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetEdgeRowResolver(r EdgeRowResolver) { md.edgeRows = r }

// PortRowResolver maps a numeric buffer PORT-ROW index to its (node, port, isInput)
// identity. Implemented by Buffer.SnapshotState (which wrote the Port block in this same
// row order). Kept as an interface here so the Wiring package needs no dependency on the
// Buffer package — main.go injects the concrete resolver.
type PortRowResolver interface {
	LookupPortRow(row int) (node, port string, isInput, ok bool)
}

// SetPortRowResolver injects the port-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetPortRowResolver(r PortRowResolver) { md.portRows = r }

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace) *MoveDispatch {
	md := &MoveDispatch{
		dispatch:           map[string]chan moveMsg{},
		nodeMovers:         map[string]*nodeMover{},
		edgeMovers:         map[string]*edgeMover{},
		edgeOut:            map[string]*Out{},
		tr:                 tr,
		ov:                 defaultOverlayState(),
		directlyFadedNodes: map[string]bool{},
		directlyFadedEdges: map[string]bool{},
		layoutHolders:      map[string]*LayoutHolder{},
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr)
		nm.sendMove = md.sendMove
		nm.centerOf = md.centerOfNode
		nm.commitLocal = md.commitNodeMoveLocal
		nm.gateEqualize = md.gateEqualizeNode
		nm.gatePlace = md.gatePlaceNode
		if nb, ok := gateNeighbors[id]; ok {
			aID, bID := nb[0], nb[1]
			nm.placeEqualRadii = func(target vec3) vec3 {
				return md.placeEqualRadii(target, aID, bID)
			}
		}
		md.nodeMovers[id] = nm
		md.dispatch[id] = nm.inbox
	}
	for edgeID, ep := range edgeEndpoints {
		em := newEdgeMover(ep, edgeID, geoms[ep.Source], geoms[ep.Target], tr)
		md.edgeMovers[edgeID] = em
		md.dispatch[edgeID] = em.inbox
	}
	// Wire each nodeMover's aimed-port lookup: for (port,isInput) on nodeID, find its one
	// edge (edgeEndpoints) and read the partner's CURRENT center off the partner
	// nodeMover's atomic snap (md.nodeMovers is a read-only map after this point — only the
	// individual *nodeMover.snap is read cross-goroutine, via the existing atomic pattern).
	for id, nm := range md.nodeMovers {
		nm.partnerCenter = buildPartnerCenterFn(id, edgeEndpoints, func(otherID string) vec3 {
			other, ok := md.nodeMovers[otherID]
			if !ok {
				return vec3{}
			}
			if s := other.snap.Load(); s != nil {
				return s.c
			}
			return vec3{}
		})
	}
	// Give every nodeMover the ids of its OWN incident edges, so a lock-driven move can
	// notify its edges via sendMove (dispatch-map lookup) — no cached channel slice.
	for id, nm := range md.nodeMovers {
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				nm.edgeIDs = append(nm.edgeIDs, edgeID)
			}
		}
	}
	return md
}

// Bind wires the per-edge source Outs (keyed "source.sourceHandle" in outSink) and
// dest wires (slotReg, keyed "target.targetHandle") into each edgeMover, and seeds
// each dest wire's per-edge latency so MaxIncomingSimLatencyMs starts as the max over
// all feeding edges. Call once after node construction.
func (md *MoveDispatch) Bind(outSink map[string]*Out, slotReg SlotRegistry) {
	for edgeID, em := range md.edgeMovers {
		if o, ok := outSink[em.srcID+"."+em.srcH]; ok {
			em.out = o
			md.edgeOut[edgeID] = o
		}
		if pw, ok := slotReg[em.dstID+"."+em.dstH]; ok {
			em.dest = pw
			if em.out != nil {
				pw.SetIncomingLatency(edgeID, em.out.Geom().SimLatencyMs)
			}
		}
	}
}

// Start launches every mover's goroutine. Each node and each edge drains its own
// inbox until ctx is done — per-goroutine ownership, no central coordinator.
func (md *MoveDispatch) Start(ctx context.Context) {
	md.started = true
	for _, nm := range md.nodeMovers {
		go nm.run(ctx)
	}
	for _, em := range md.edgeMovers {
		go em.run(ctx)
	}
}

// ResendGeometry re-emits the full current geometry from the movers' held
// authoritative state: each node's node-geometry (emitNodeGeometry) and each edge's
// segment (tr.Geometry), recomputed from the edge's held endpoint geoms/handles. This
// reproduces exactly what a fresh load streams on startup, so a freshly-(re)mounted
// webview that lost its module-level edge-geometry store can rebuild it without
// restarting Go.
//
// When movers are running (md.started), each mover emits its own geometry on its own
// goroutine via a synchronous moveMsgKindResend inbox message (ack-gated), removing
// all cross-goroutine reads of mover geom. When movers are not started (test setup
// that never calls Start), the direct read path is safe — no concurrent goroutines
// own the geom yet.
func (md *MoveDispatch) ResendGeometry(ctx context.Context, tr *T.Trace) {
	if tr == nil {
		return
	}
	if !md.started {
		// Movers not running — direct read is safe (no concurrent goroutines own geom).
		for _, nm := range md.nodeMovers {
			emitNodeGeometryLocked(tr, nm.id, nm.geom, nm.partnerCenter)
		}
		for _, em := range md.edgeMovers {
			seg := edgeSegment(em.srcGeom, em.dstGeom, em.srcH, em.dstH)
			tr.Geometry(em.edgeID, em.srcID, em.dstID,
				seg.Start.X, seg.Start.Y, seg.Start.Z,
				seg.End.X, seg.End.Y, seg.End.Z)
		}
		return
	}
	// Movers running — route resend through each mover's inbox so the emit happens
	// on the owning goroutine (no cross-goroutine geom reads). Collect all acks
	// before returning so the caller observes a complete geometry stream.
	total := len(md.nodeMovers) + len(md.edgeMovers)
	acks := make([]chan struct{}, 0, total)
	for _, nm := range md.nodeMovers {
		ack := make(chan struct{}) // chan-name-ok: resend-handled acknowledgment sync signal, not a node wire
		select {
		case nm.inbox <- moveMsg{Kind: moveMsgKindResend, ack: ack}:
			acks = append(acks, ack)
		case <-ctx.Done():
			return
		}
	}
	for _, em := range md.edgeMovers {
		ack := make(chan struct{}) // chan-name-ok: resend-handled acknowledgment sync signal, not a node wire
		select {
		case em.inbox <- moveMsg{Kind: moveMsgKindResend, ack: ack}:
			acks = append(acks, ack)
		case <-ctx.Done():
			return
		}
	}
	for _, ack := range acks {
		select {
		case <-ack:
		case <-ctx.Done():
			return
		}
	}
}

// EdgeOut returns the source *Out bound to the given edge label, or nil if unknown.
// Read-only accessor for out-of-package verifiers (the headless cascade reads an
// edge's per-edge in-flight time from the loaded geometry).
func (md *MoveDispatch) EdgeOut(edgeID string) *Out {
	return md.edgeOut[edgeID]
}

// centerOfNode returns the current world center for a node id by loading the
// nodeMover's atomically-published snapshot. Safe to call from any goroutine
// without synchronization — the snap is published via atomic.Pointer after each
// center update so this never races with the mover's live geom writes.
func (md *MoveDispatch) centerOfNode(id string) (vec3, bool) {
	if nm, ok := md.nodeMovers[id]; ok {
		if s := nm.snap.Load(); s != nil {
			return s.c, true
		}
	}
	return vec3{}, false
}

// sendMove routes one moveMsg to another node's (or edge's) inbox by id, if known.
// Used by the decentralized lock-propagation cascade so a nodeMover can re-broadcast
// to its own lock-neighbors without any central worklist — the dispatch map is the
// only "directory" involved, and it's read-only lookup, not a queue.
func (md *MoveDispatch) sendMove(id string, msg moveMsg) {
	if tap := md.msgTap.Load(); tap != nil {
		(*tap)(id, msg)
	}
	if ch, ok := md.dispatch[id]; ok {
		ch <- msg
	}
}

// heldCenters / heldEdges snapshot the movers' current geometry.
// heldCenters reads the atomically-published snap (not the live geom) so it is
// safe to call from the stdin goroutine while mover goroutines write their centers.
func (md *MoveDispatch) heldCenters() map[string]vec3 {
	centers := make(map[string]vec3, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		if s := m.snap.Load(); s != nil {
			centers[id] = s.c
		}
	}
	return centers
}

// heldPolar snapshots every mover's current SCENE POLAR (r,θ,φ about the scene center) from
// the atomically-published snap — the polar source of truth for geometry math (reach, arc,
// colinearity), read safe from the stdin goroutine while movers write their positions.
func (md *MoveDispatch) heldPolar() map[string]polar {
	polars := make(map[string]polar, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		if s := m.snap.Load(); s != nil {
			polars[id] = s.p
		}
	}
	return polars
}

func (md *MoveDispatch) heldEdges() []sphereEdge {
	edges := make([]sphereEdge, 0, len(md.edgeMovers))
	for _, em := range md.edgeMovers {
		edges = append(edges, sphereEdge{Source: em.srcID, Target: em.dstID})
	}
	return edges
}

// fanCenters pushes one "center" message per node (carrying its new world center and
// reach radius) to that node's mover AND every incident edge's mover. Each owning
// goroutine writes the center onto its own held geom and re-emits — the whole-graph
// SOLVE is central; the per-mover APPLY stays decentralized.
//
// Aimed-port re-emit: when an aimed registry is installed, any nodeMover whose
// port aims AT a moved node must also re-emit its geometry so the port direction
// updates to the new target position. We send a no-center moveMsg to those nodes
// so they re-emit with the fresh target center (which the target mover just wrote
// to its geom; the aimer reads via centerOfNode at emit time).
func (md *MoveDispatch) fanCenters(newCenters map[string]vec3, reach map[string]float64) {
	// Per-node DIRECT position writes: route each moved node's own new center to its
	// own nodeMover inbox as a moveMsgKindCenter message carrying Center — nodeMover's
	// handle applies it via applyCenter, making that node's own mover goroutine the
	// sole writer of its position. One per moved node.
	//
	// This self-send is the CENTRAL-design artifact: the calling goroutine (stdin
	// reader) cannot write the moved node's snap itself, so it messages the node's
	// own mover to do it. When the commit instead ORIGINATES on the moved node's own
	// goroutine, this self-send must NOT be used — the
	// message would sit unread in this node's own inbox until this very handler
	// returns, so anything read immediately after (e.g. handleTrigger's snap.Load())
	// would observe the STALE pre-move center. See commitNodeMoveLocal, which calls
	// applyCenter directly instead and then calls fanEdgesAndPartners for the rest.
	for id, c := range newCenters {
		if ch, ok := md.dispatch[id]; ok {
			cc := c
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: reach[id]}
		}
	}
	md.fanEdgesAndPartners(newCenters)
}

// fanEdgesAndPartners is the cross-goroutine half of fanCenters: it messages every
// incident edge's mover (batched per-edge Centers) and every aimed-port partner
// (pure re-emit), for the given already-applied set of moved node centers. It never
// writes the moved node's OWN snap — that responsibility belongs to whichever caller
// applied the moved node's own center (either fanCenters' self-send above, for a
// central caller, or applyCenter called directly, for an owner-goroutine caller).
func (md *MoveDispatch) fanEdgesAndPartners(newCenters map[string]vec3) {
	// Per-edge: send ONE batched message carrying every moved endpoint of that edge,
	// so an edge whose both endpoints moved this frame recomputes/emits exactly once.
	for edgeID, em := range md.edgeMovers {
		eps := map[string]vec3{}
		if c, ok := newCenters[em.srcID]; ok {
			eps[em.srcID] = c
		}
		if c, ok := newCenters[em.dstID]; ok {
			eps[em.dstID] = c
		}
		if len(eps) == 0 {
			continue
		}
		if ch, ok := md.dispatch[edgeID]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenters, Centers: eps}
		}
	}

	// Aimed-port re-emit (see doc comment above): find every partner node — the OTHER
	// end of any edge incident to a moved node — and ask it to re-emit its OWN geometry
	// with its OWN (unchanged) center, mirroring reemitPortTorusGeometry's "same center"
	// trick. emitGeometry reads m.partnerCenter at emit time, which is the moved node's
	// FRESH atomic snap (already written above), so the partner's aimed port marker
	// picks up the new target direction. This does NOT run for torus-locked ports only —
	// it runs for every aimed connected port unconditionally, even if that breaks a
	// port∈torus lock; that is intended.
	partners := map[string]bool{}
	for _, em := range md.edgeMovers {
		if _, moved := newCenters[em.srcID]; moved {
			if _, alsoMoved := newCenters[em.dstID]; !alsoMoved {
				partners[em.dstID] = true
			}
		}
		if _, moved := newCenters[em.dstID]; moved {
			if _, alsoMoved := newCenters[em.srcID]; !alsoMoved {
				partners[em.srcID] = true
			}
		}
	}
	for partnerID := range partners {
		if _, ok := md.nodeMovers[partnerID]; !ok {
			continue
		}
		if ch, ok := md.dispatch[partnerID]; ok {
			// Center is deliberately nil (see the doc comment above): this is a PURE
			// re-emit, not a position write. Sending a non-nil Center rebuilt from
			// nm.snap.Load() here would re-apply partnerID's OWN current snapshot to
			// itself — normally an idempotent no-op, but a genuine hazard when a
			// second, concurrently-in-flight fanCenters call (e.g. the node-2→5
			// drag-equalize cascade) has ALREADY queued partnerID's real new-position
			// message on this same inbox: a stale non-nil re-read here would queue
			// BEHIND the real update and clobber it back to the pre-move position on
			// drain. nodeMover.handle's nil-Center branch re-emits from the mover's own
			// live geom (whatever it is by the time this drains), so it can never race
			// or clobber a pending position write.
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: partnerID, Center: nil}
		}
	}
}

// placeEqualRadii returns a two-neighbor gate node's landing point for a drag to target,
// constrained so its two radii — to the FIXED neighbors aID and bID — are EQUAL. The landing
// is the NEAREST point to target on the perpendicular-bisector plane of a–b (the set of points
// equidistant from a and b): p = target − ((target−mid)·n̂) n̂, with n̂ = normalize(b−a) and
// mid = (a+b)/2. This orthogonal projection is finite and continuous for EVERY target — unlike
// the earlier scene-center-ray solve, which ran to infinity / flipped sign when the bearing
// grazed the plane (the node-9 far-mouse blow-up and the node-10 runaway under node 6's
// cascade). Neither neighbor moves. Used by nodes 9 (neighbors 3,6) and 10 (6,8). Falls back to
// the raw target only when a neighbor's center is unknown or a and b coincide (no plane).
func (md *MoveDispatch) placeEqualRadii(target vec3, aID, bID string) vec3 {
	a, oka := md.centerOfNode(aID)
	b, okb := md.centerOfNode(bID)
	if !oka || !okb {
		return target
	}
	n := b.sub(a)
	if n.length() == 0 {
		return target
	}
	nhat := n.normalize()
	mid := a.add(b).scale(0.5)
	// Nearest point on the perpendicular-bisector plane of a–b to target: subtract target's
	// component along the plane normal. Finite for every target — no ray, no singularity.
	return target.sub(nhat.scale(target.sub(mid).dot(nhat)))
}

// equalizeEdgeCLocal is a two-neighbor gate node's edge-c requantize step — the per-node
// replacement for requantizeLocalPolars, called from rootMove AFTER the node has already
// moved through the normal path (fan + scene c refresh, so it repositions on screen like any
// dragged node). It mutates ONLY the node's own LayoutHolder: its local polar to aID and to
// bID (each in the node's own frame). It computes the two candidate c's (quantIR) the node's
// new position implies for those two edges, keeps the SHORTER as the new c, and writes it
// onto BOTH records — leaving each edge's own bearing (quantITheta/quantIPhi) and step
// constants untouched. The two neighbors are NEVER written (unlike the generic double-link
// requantize, which updates both ends). Used by nodes 9 (3,6) and 10 (6,8). Returns false
// only when the node has no LayoutHolder.
func (md *MoveDispatch) equalizeEdgeCLocal(nodeID, aID, bID string, newPos vec3) bool {
	lh, ok := md.layoutHolders[nodeID]
	if !ok {
		return false
	}
	// the node's local polar (own frame) to a fixed neighbor, as the drag implies it.
	local := func(to string) (qt, qp, qr int, st, sp, sr float64, ok bool) {
		c, okc := md.centerOfNode(to)
		if !okc {
			return 0, 0, 0, 0, 0, 0, false
		}
		st, sp, sr = lh.localPolarSteps(to)
		pol := cart2polar(c.sub(newPos))
		return int(math.Round(pol.Theta / st)), int(math.Round(pol.Phi / sp)), int(math.Round(pol.R / sr)), st, sp, sr, true
	}
	qtA, qpA, qrA, stA, spA, srA, okA := local(aID)
	qtB, qpB, qrB, stB, spB, srB, okB := local(bID)
	if !okA || !okB {
		return false
	}
	cNew := min(qrA, qrB)
	lh.SetLocalPolar(aID, qtA, qpA, cNew, stA, spA, srA)
	lh.SetLocalPolar(bID, qtB, qpB, cNew, stB, spB, srB)
	if md.quantOffsetPersist != nil {
		if root := md.quantOffsetPersist.root; root != "" {
			if err := WriteLocalPolars(root, nodeID, lh.LocalPolarsSnapshot()); err != nil {
				logPersistErr("local_polar_persist", nodeID, err)
			}
		}
	}
	return true
}

// placeAtDistanceFromBoth returns the point at distance d from BOTH fixed anchors a and b,
// nearest to nodeID's current position — so the node's two radii (to a and to b) both equal d.
// The equal-distance locus is the circle where the two spheres (radius d, centers a and b)
// meet: it lies in the perpendicular-bisector plane of a–b, centered at the midpoint m, with
// radius ρ = sqrt(d² − |a−b|²/4). The nearest circle point to the node's current spot is
// m + ρ·normalize(proj(cur)−m), proj = orthogonal projection of cur onto that plane. d is
// floored at |a−b|/2 (below it the spheres don't meet; the node lands at the midpoint).
// Stable: once on the circle the nearest point is itself, so it is a fixed point (no drift).
func (md *MoveDispatch) placeAtDistanceFromBoth(nodeID string, a, b vec3, d float64) (vec3, bool) {
	cur, ok := md.centerOfNode(nodeID)
	if !ok {
		return vec3{}, false
	}
	ab := b.sub(a)
	half := ab.length() / 2
	if half == 0 {
		return vec3{}, false
	}
	if d < half {
		d = half
	}
	m := a.add(b).scale(0.5)
	nhat := ab.scale(1 / (2 * half))
	q := cur.sub(nhat.scale(cur.sub(m).dot(nhat))) // project current pos onto bisector plane
	dir := q.sub(m)
	rho := math.Sqrt(math.Max(0, d*d-half*half))
	if dir.length() == 0 {
		return m, true // current projects onto the axis; land at midpoint (equidistant)
	}
	return m.add(dir.normalize().scale(rho)), true
}

// moveNodeAndSetEdgeCs moves nodeID to newPos (the same fan + scalar-triple persist rootMove
// applies to any single dragged node, so it repositions on screen), then sets nodeID's two
// edge-c records (to edgeA and edgeB) to c, preserving each record's own bearing/step
// constants. Only nodeID's own LayoutHolder is written — the edge neighbors are never moved.
func (md *MoveDispatch) moveNodeAndSetEdgeCs(nodeID string, newPos vec3, edgeA, edgeB string, c int) {
	edges := md.heldEdges()
	emit := map[string]vec3{nodeID: newPos}
	polars := md.heldPolar()
	polars[nodeID] = cart2polar(newPos.sub(md.sceneSphere.Center))
	md.fanCenters(emit, reachRFromPolar(polars, edges))
	if md.quantizedLayout {
		if nm, ok := md.nodeMovers[nodeID]; ok {
			off := measureScalar(newPos, md.sceneSphere.Center, nm.quantOffset)
			nm.quantOffset = off
			if md.quantOffsetPersist != nil {
				md.quantOffsetPersist.schedule(nodeID, off, cart2polar(newPos.sub(md.sceneSphere.Center)))
			}
		}
	}
	lh, ok := md.layoutHolders[nodeID]
	if !ok {
		return
	}
	root := ""
	if md.quantOffsetPersist != nil {
		root = md.quantOffsetPersist.root
	}
	persist := func(id string, holder *LayoutHolder) {
		if root == "" {
			return
		}
		if err := WriteLocalPolars(root, id, holder.LocalPolarsSnapshot()); err != nil {
			logPersistErr("local_polar_persist", id, err)
		}
	}
	// Set BOTH ends of each edge to c so the double-link agrees: nodeID's record to the
	// neighbor AND the neighbor's back-reference to nodeID. Each end quantizes its own
	// bearing on its own step grid but shares the propagated c. Persists both holders — the
	// far neighbor does not MOVE, only its edge-length record to nodeID is refreshed.
	setBothEnds := func(far string) {
		fc, ok := md.centerOfNode(far)
		if !ok {
			return
		}
		st, sp, sr := lh.localPolarSteps(far)
		near := cart2polar(fc.sub(newPos))
		lh.SetLocalPolar(far, int(math.Round(near.Theta/st)), int(math.Round(near.Phi/sp)), c, st, sp, sr)
		flh, ok := md.layoutHolders[far]
		if !ok {
			return
		}
		fst, fsp, fsr := flh.localPolarSteps(nodeID)
		back := cart2polar(newPos.sub(fc))
		flh.SetLocalPolar(nodeID, int(math.Round(back.Theta/fst)), int(math.Round(back.Phi/fsp)), c, fst, fsp, fsr)
		persist(far, flh)
	}
	setBothEnds(edgeA)
	setBothEnds(edgeB)
	persist(nodeID, lh)
}

// commitNodeMoveLocal is the OWNER-GOROUTINE single-node commit path
// (generalized to every node): used when the commit
// originates on nodeID's OWN mover goroutine (its own inbox handler for a
// moveMsgKindDrag or moveMsgKindEqualize). It publishes nodeID's OWN snap
// SYNCHRONOUSLY via applyCenter — safe and correct here because applyCenter's doc
// contract is "called only from this nodeMover's own inbox-drain goroutine", which
// this is. Also fans centers to incident edges/partners, persists the per-node
// quantized-offset (nodeMover.quantOffset — never a shared map, so no other mover
// goroutine's commit can race this write even for a different node id), and
// requantizes nodeID's local-polar double-links against its (unmoved) neighbors.
func (md *MoveDispatch) commitNodeMoveLocal(nodeID string, newPos vec3) {
	edges := md.heldEdges()
	polars := md.heldPolar()
	polars[nodeID] = cart2polar(newPos.sub(md.sceneSphere.Center))
	reach := reachRFromPolar(polars, edges)

	nm, ok := md.nodeMovers[nodeID]
	if ok {
		nm.applyCenter(newPos, reach[nodeID])
	}
	md.fanEdgesAndPartners(map[string]vec3{nodeID: newPos})

	if md.quantizedLayout && ok {
		off := measureScalar(newPos, md.sceneSphere.Center, nm.quantOffset)
		nm.quantOffset = off
		if md.quantOffsetPersist != nil {
			md.quantOffsetPersist.schedule(nodeID, off, cart2polar(newPos.sub(md.sceneSphere.Center)))
		}
	}

	md.requantizeLocalPolars(nodeID, newPos)
}

// gateEqualizeNode runs a two-neighbor GATE node's own edge-c equalize
// (equalizeEdgeCLocal) using its CURRENT committed center (read via centerOfNode —
// safe from any goroutine, including this node's own, since it reads the atomically-
// published snapshot rather than live geom). Called from the gate node's own mover
// goroutine via handleTrigger's gate branch, decentralizing the c-equalize step that
// the old central rootMove ran synchronously on the drag call stack
// . No-op for an unknown gate id or unresolved center.
func (md *MoveDispatch) gateEqualizeNode(nodeID string) {
	nb, ok := gateNeighbors[nodeID]
	if !ok {
		return
	}
	c, ok := md.centerOfNode(nodeID)
	if !ok {
		return
	}
	md.equalizeEdgeCLocal(nodeID, nb[0], nb[1], c)
}

// gatePlaceNode runs a gate node's (9 or 10) placeAtDistanceFromBoth + moveNodeAndSetEdgeCs
// re-solve against anchorID's fresh center (node 6's cascade — the decentralized
// replacement for the retired central propagateShortestCFrom6). anchorID identifies which of the
// node's two gateNeighbors is the anchor (always "6" in practice); the OTHER neighbor is
// resolved via gateNeighbors and read via its CURRENT published center. The two anchor
// args to placeAtDistanceFromBoth are ordered to match propagateShortestCFrom6's original
// central calls exactly (node 9: (otherNeighbor, anchor) == (3, 6); node 10: (anchor,
// otherNeighbor) == (6, 8)) so behavior is bit-for-bit identical. No-op for an unknown
// gate id, an anchorID that isn't one of its two gateNeighbors, or an unresolved center.
func (md *MoveDispatch) gatePlaceNode(nodeID, anchorID string, anchorCenter vec3, d float64, snapC int) {
	nb, ok := gateNeighbors[nodeID]
	if !ok {
		return
	}
	var a, b vec3
	switch anchorID {
	case nb[0]:
		other, okc := md.centerOfNode(nb[1])
		if !okc {
			return
		}
		a, b = anchorCenter, other
	case nb[1]:
		other, okc := md.centerOfNode(nb[0])
		if !okc {
			return
		}
		a, b = other, anchorCenter
	default:
		return
	}
	newPos, ok := md.placeAtDistanceFromBoth(nodeID, a, b, d)
	if !ok {
		return
	}
	md.moveNodeAndSetEdgeCs(nodeID, newPos, nb[0], nb[1], snapC)
}

// RootMove handles a node-drag under the flat absolute scene-polar layout: every node
// is positioned independently about the scene sphere center — there is no reference/
// parent concept, so dragging moves ONLY the dragged node (no cascade). The dragged
// node's new world position is the drag target itself — CONTINUOUS, not snapped to any
// grid (double-link local-polar model: the node's position is free; only each
// neighbor's DISTANCE to it is quantized, each on that neighbor's own small grid — see
// requantizeLocalPolars). The node's center is fanned out (re-emitting its own +
// incident edges' geometry, with the fresh reach radius), its scene-center scalar
// triple is remeasured + persisted (still the reload/render position source), and every
// affected double-link's local polars are re-quantized on BOTH ends. Returns false for
// an unknown node.
func (md *MoveDispatch) RootMove(nodeID string, target vec3) bool {
	// EVERY node's drag runs on the decentralized goroutine-message path: gate nodes
	// (1/9/10) solve equal-radii at entry then self-trigger their gate equalize; rule
	// nodes (5/2) equalize followers + cascade over channels; node 6 fans its shortest c
	// to 9/10 + node 2; plain leaves (3/7/8/…) are a free move + no-op self-trigger. There
	// is no central cascade left — rootMove is gone.
	return md.rootMoveViaMessages(nodeID, target)
}

// rootMoveViaMessages is the decentralized drag entry, widened to EVERY node (the
// generalization that came with the quantizedOffsets data-race fix): dragging any node no
// longer commits on the stdin reader's own
// goroutine — it routes a single moveMsgKindDrag to the dragged node's OWN inbox and
// returns. The dragged node's own moveMsgKindDrag handler (nodeMover.handle) does the rest,
// entirely on its own goroutine: solve equal-radii against its FIXED neighbors first if it
// is a gate node (nm.placeEqualRadii, nil for non-gate nodes), commit its own new position
// (commitLocal — fan + persist + requantize, no cross-goroutine self-send), then run its
// own self-trigger (moveMsgKindTrigger, SenderID=="") so the rest of its chain (its own
// ruleFollowers, then any rule-neighbor whose ruleSource is this node, delta-gated so the
// chain fans outward and terminates) runs as further node-to-node messages. handleTrigger
// (generic, keyed off ruleSource/ruleFollowers by id, plus special-cased branches for node
// 6 — the FIXED anchor of its own gate-place cascade — and node 2's SenderID=="6"
// re-trigger) needs no further per-node change to support this: node 2's normal trigger
// forwards to BOTH 5 and 1 (both have ruleSource == "2"); node 1 is a leaf (nothing has
// ruleSource == "1") so it forwards to nobody; node 6 is neither a ruleSource/ruleFollowers
// node nor a gateNeighbors node, so its self-trigger runs its own dedicated branch
// (computes the shortest c to 9/10, sends each a GatePlace message, then forwards a Trigger
// to node 2 with SenderID=="6"). Nodes 9 and 10 are GATE nodes, not
// rule-nodes: each node's position is first solved to the equal-radii locus against its
// FIXED neighbors (gateNeighbors) inside its own drag handler before committing, and its
// self-trigger dispatches to handleTrigger's gate branch (its own edge-c equalize only —
// no forward, no follower, no neighbor move) UNLESS the trigger is actually a GatePlace
// message (node 6's cascade), which runs the gatePlace branch in `handle` instead of
// handleTrigger. Node 6 itself is a FREE move (it is not in gateNeighbors, so its
// nm.placeEqualRadii is nil), matching its old central behavior exactly (no equal-radii
// re-solve).
func (md *MoveDispatch) rootMoveViaMessages(nodeID string, target vec3) bool {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return false
	}
	// Route the drag itself to the dragged node's OWN inbox instead of committing on
	// the stdin reader's goroutine — every node's moveMsgKindDrag handler solves
	// (gate nodes only), commits (synchronous local snap publish), and self-triggers,
	// all on its own goroutine. No central commit call here.
	md.sendMove(nodeID, moveMsg{Kind: moveMsgKindDrag, NodeID: nodeID, Target: target})
	return true
}

// requantizeLocalPolars implements the double-link local-polar model on a drag: the
// dragged node X's new position gives each of its domain neighbors M a NEW distance to
// it. That distance is quantized to a whole tick on THAT neighbor's own small grid
// (layout_holder.go localStepTheta/localStepPhi/localStepR, or M's stored per-neighbor
// step constants) — and likewise X's own local polar TO M is requantized on X's own
// grid. The two ends' quantized values are independent and never reconciled or
// reconstructed from one another (MODEL.md "no blow-up, by construction" — this is the
// local-polar analogue: nothing rebuilds X's position from a local polar). Both ends'
// LayoutHolders are updated in memory and persisted.
//
// NOT YET decentralized (left for a follow-up, per the quantizedOffsets data-race fix):
// this writes the NEIGHBOR M's own LayoutHolder from nodeID's (the mover's own) goroutine
// — a cross-goroutine write, unlike quantOffset above. It is memory-safe today because
// each LayoutHolder guards itself with its own mu (LayoutHolder.mu), not because it is
// message-routed; moveNodeAndSetEdgeCs's setBothEnds does the same cross-write to the far
// neighbor's holder. Converting these into node-to-node messages (so a holder is mutated
// only by its own node's goroutine, mirroring nodeMover.quantOffset) is the next
// decentralization step, not required to fix the reported crash.
func (md *MoveDispatch) requantizeLocalPolars(nodeID string, newPos vec3) {
	lhX, okX := md.layoutHolders[nodeID]
	if !okX {
		return
	}
	neighbors := map[string]bool{}
	for _, em := range md.edgeMovers {
		if em.srcID == nodeID {
			neighbors[em.dstID] = true
		} else if em.dstID == nodeID {
			neighbors[em.srcID] = true
		}
	}
	if len(neighbors) == 0 {
		return
	}
	root := ""
	if md.quantOffsetPersist != nil {
		root = md.quantOffsetPersist.root
	}
	xChanged := false
	for m := range neighbors {
		lhM, okM := md.layoutHolders[m]
		if !okM {
			continue
		}
		cM, ok := md.centerOfNode(m)
		if !ok {
			continue
		}
		xChanged = true

		// X's local polar TO M, on X's own effective step constants.
		tX, pX, rX := lhX.localPolarSteps(m)
		polXtoM := cart2polar(cM.sub(newPos))
		lhX.SetLocalPolar(m,
			int(math.Round(polXtoM.Theta/tX)),
			int(math.Round(polXtoM.Phi/pX)),
			int(math.Round(polXtoM.R/rX)),
			tX, pX, rX)

		// M's local polar TO X, on M's own effective step constants.
		tM, pM, rM := lhM.localPolarSteps(nodeID)
		polMtoX := cart2polar(newPos.sub(cM))
		lhM.SetLocalPolar(nodeID,
			int(math.Round(polMtoX.Theta/tM)),
			int(math.Round(polMtoX.Phi/pM)),
			int(math.Round(polMtoX.R/rM)),
			tM, pM, rM)

		if root != "" {
			if err := WriteLocalPolars(root, m, lhM.LocalPolarsSnapshot()); err != nil {
				logPersistErr("local_polar_persist", m, err)
			}
		}
	}
	if xChanged && root != "" {
		if err := WriteLocalPolars(root, nodeID, lhX.LocalPolarsSnapshot()); err != nil {
			logPersistErr("local_polar_persist", nodeID, err)
		}
	}
}

// reachRFromPolar computes each node's sphere REACH radius (max distance from a node to any
// node it outputs to) under the given polar positions and edge set. Distance is the spherical
// law-of-cosines distance between the two polar positions (polarDist) — no cartesian, no vector
// subtraction. Called by loader.go buildFromSpec and by RootMove so the fanned "center" message
// carries the new reach radius and the ring stays sized during a drag.
func reachRFromPolar(polars map[string]polar, edges []sphereEdge) map[string]float64 {
	reachR := map[string]float64{}
	for _, e := range edges {
		sp, okS := polars[e.Source]
		tp, okT := polars[e.Target]
		if !okS || !okT {
			continue
		}
		if d := polarDist(sp, tp); d > reachR[e.Source] {
			reachR[e.Source] = d
		}
	}
	return reachR
}

// NodeKind returns the kind string for the given node id, or "" if unknown.
// Used by applyEdit to resolve the node's kind when snapping a port-anchor
// world-space direction to the nearest ring-anchor index.
func (md *MoveDispatch) NodeKind(nodeID string) string {
	if nm, ok := md.nodeMovers[nodeID]; ok {
		return nm.geom.Kind
	}
	return ""
}

// Camera viewpoint API — thin delegators to the owned viewpointState (viewpoint_state.go).
// The public signatures are unchanged; the state and behavior live on md.vp.

func (md *MoveDispatch) SetViewpoint(pivot vec3, r float64, pos, up dir) {
	md.vp.SetViewpoint(pivot, r, pos, up)
}
func (md *MoveDispatch) EmitViewpoint(tr *T.Trace)                { md.vp.EmitViewpoint(tr) }
func (md *MoveDispatch) OrbitViewpoint(from, to dir, tr *T.Trace) { md.vp.OrbitViewpoint(from, to, tr) }
func (md *MoveDispatch) OrbitLockedViewpoint(from, to dir, tr *T.Trace) {
	md.vp.OrbitLockedViewpoint(from, to, tr)
}
func (md *MoveDispatch) ZoomViewpoint(factor float64, tr *T.Trace) { md.vp.ZoomViewpoint(factor, tr) }
func (md *MoveDispatch) PanViewpoint(delta vec3, tr *T.Trace) {
	// A dolly is a pure CAMERA move (the eye translates toward the cursor). It must NOT move the
	// scene sphere: coupling them left md.sceneSphere.Center diverged from the movers' held
	// center until a later broadcast reconciled it with a jump (the "zoom got canceled"
	// symptom). Moving the sphere would be a SEPARATE scene-pan gesture — which does not
	// exist today; see MODEL.md's "the sphere is currently immovable" note.
	md.vp.PanViewpoint(delta, tr)
}

// EnableViewpointPersist arms gesture-driven camera persistence: every subsequent
// EmitViewpoint (orbit/zoom/pan/home) debounces a write of the current viewpoint to
// `<topologyPath>/view/scene.json`'s cameraPolar (scene_camera_persist.go). Call AFTER
// SeedInitialViewpoint so the seed's own emit does not write the loaded/default pose back.
// Go owns this write (MODEL.md); the old path persists the camera via its own TS scene-save.
func (md *MoveDispatch) EnableViewpointPersist(topologyPath string) {
	p := &viewpointPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.vpPersist = p
	md.vp.persist = p.schedule
}

// EnableEditPersist arms disk persistence for the three FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's x/y/z in <root>/nodes/<id>/meta.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - fade (ToggleFadeSelection) → fadedNodes/fadedEdges in view/scene.json
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/scene.json
//
// Node-position + anchor persistence needs the per-node/per-port files of the directory-tree
// form; for a monolithic topology.json (no per-node files) their root is "" and those two
// persisters no-op. Fade rides scene.json, which exists for both forms. Call AFTER
// SeedInitialViewpoint + SeedFade so the seed emits do not write the loaded state back.
func (md *MoveDispatch) EnableEditPersist(topologyPath string) {
	// sceneTreeRoot handles both the directory form and the file-inside-tree form (and
	// returns "" for a true monolithic topology with no tree), making the two-form bug
	// class unrepresentable here. Do not hand-roll os.Stat/IsDir — use sceneTreeRoot.
	root := sceneTreeRoot(topologyPath)
	md.posPersist = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.anchorPersist = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.fadePersist = &fadePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.overlaysPersist = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.spherePersist = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.quantOffsetPersist = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
