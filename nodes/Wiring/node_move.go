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
	"sort"
	"sync"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// moveMsgKind discriminates moveMsg payloads.
const (
	// The node-move kind ("move", the zero value "") carries no payload and is a
	// no-op in every mover switch, so it has no constant — the switches simply
	// fall through. The remaining kinds each select a distinct payload.
	moveMsgKindAnchor  = "anchor"  // per-port anchor update (drag along the ring)
	moveMsgKindCenter  = "center"  // polar-layout re-propagated world center for one node
	moveMsgKindCenters = "centers" // batched centers for an edge: update both endpoints, recompute ONCE
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

// Per-node cascade rule data (sourceID, followers, forwardTargets, isGate,
// gateA/gateB, anchoredGates, followerOwner — the nodeMover fields below) replaces
// what used to be three package-level maps (ruleSource/ruleFollowers/gateNeighbors)
// HARDCODED to the current 10-node topology. Each is now DERIVED once at load
// (loader.go deriveCascadeRules) from the spec each node carries on its own
// LocalPolars entries (layout_holder.go LocalPolar.Role: "source"/"follower"/"")
// and its own Gate flag (loader.go specNode.Gate) — a graph without these markers
// simply gets no cascade for that node, instead of silently missing an id from a
// table that lived here.

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
//
// Every PRODUCTION send is fire-and-forget: the sender drops the message in an inbox
// and returns. No production path observes the receiver finishing — a node does its own
// local work on its own goroutine and drives its own outputs (MODEL.md: no ack, no
// send-gating, no delivery signal).
//
// testDone is the one exception and it is NOT a production mechanism: it exists only so
// a test can block until an async mover has handled a message before asserting (see
// node_move_test.go's `deliver`). It is needed because an edgeMover publishes no atomic
// snapshot a test could safely poll. Production ALWAYS leaves it nil — if you find
// yourself setting it outside a _test.go file, you are reintroducing the ack the model
// forbids; make the receiver's own goroutine do the work instead.
type moveMsg struct {
	Kind   string
	NodeID string
	// Anchor payload (Kind == "anchor"): identify the port whose anchor changed.
	// Port/IsInput name the port on NodeID; AnchorId is the snapped ring-anchor index
	// (Go snaps from the incoming world-space direction; TS never computes the index).
	Port     string
	IsInput  bool
	AnchorId int
	// Center (Kind == "center"): the re-propagated world center for NodeID under the
	// polar layout. Each owning node/edge goroutine writes it onto its held geom
	// and re-emits its own geometry. (RootMove is now a thin wrapper over
	// rootMoveViaMessages — the decentralized node-to-node message cascade; there is
	// no central fan-out step.)
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
	// testDone: see the type comment. Test-only; production leaves it nil.
	testDone chan struct{}
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
	// (handle's anchor/default cases, the sole writer of port-anchor fields
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
	// Cascade rule fields — DERIVED once at load (loader.go deriveCascadeRules) from
	// this node's own LocalPolars roles and Gate flag; see the package doc comment
	// above where the old ruleSource/ruleFollowers/gateNeighbors maps used to live.
	//
	// sourceID: the ONE neighbor this node measures its reference length L against
	// ("" if this node has no source role — not a rule-node).
	sourceID string
	// followers: the neighbors this node repositions (Equalize to L) when triggered.
	followers []string
	// forwardTargets: every OTHER node Y whose OWN sourceID is this node's id — the
	// inverse of sourceID, so this node's self-trigger can forward without knowing
	// the whole graph's rule table.
	forwardTargets []string
	// isGate: this node is a two-neighbor GATE node (solves equal-radii against
	// gateA/gateB on a direct drag, self-triggers its own edge-c equalize).
	isGate bool
	// gateA / gateB: this node's two FIXED neighbors (only meaningful when isGate),
	// in the same order as its own LocalPolars list.
	gateA, gateB string
	// anchoredGates: every gate node g (isGate) whose gateA/gateB names THIS node —
	// i.e. gate nodes anchored on this node's position. Used when this node moves
	// independently (not via its own rule cascade) to re-solve each anchored gate's
	// landing point (the decentralized replacement for the old node-6-shaped
	// GatePlace fan-out, generalized off which node the gates are anchored to).
	anchoredGates []string
	// followerOwner: the rule-node R such that this node appears in R.followers (the
	// inverse of followers), or "" if none. Used so a node with anchoredGates>0 can
	// forward its own re-trigger to whichever rule-node treats it as a follower,
	// without a hardcoded target id.
	followerOwner string
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

// handleTrigger runs this node's OWN cascade rule on receiving a moveMsgKindTrigger,
// entirely from fields this node's own mover carries (sourceID/followers/
// forwardTargets/isGate/gateA/gateB/anchoredGates/followerOwner — all DERIVED once at
// load, loader.go deriveCascadeRules, from this node's own LocalPolars roles and Gate
// flag; see the package doc comment above node_move.go's cascade-rule fields).
//
// Four shapes, tried in order — each keyed off this node's OWN derived fields, never
// an id literal:
//
//  1. anchoredGates>0: this node is the FIXED ANCHOR one or more gate nodes solve
//     their equal-radii landing against. It moved freely to its new committed
//     position; re-solve each anchored gate's shortest c-distance and GatePlace it,
//     then forward a Trigger (SenderID = this node) to whichever rule-node treats
//     this node as a follower (followerOwner), if any.
//  2. msg.SenderID is one of THIS node's own followers (it moved independently, not
//     via this node's own Equalize): treat the sender as the effective source and
//     this node's OWN sourceID as the effective follower for this one re-trigger,
//     then STOP (no further forward) — the follower-owner reacting to its follower's
//     own independent move, not a normal drag cascade.
//  3. isGate && msg.SenderID == "": self-trigger only, run this node's OWN edge-c
//     equalize. A FORWARDED trigger (SenderID != "") is not a direct drag of this
//     node, so a node that is ALSO a rule-node (sourceID/followers set) falls
//     through to shape 4 instead of gating here.
//  4. Generic rule-node cascade: re-derive L = dist(self, source), Equalize every
//     follower to L, and ALWAYS forward to every forwardTarget (no delta-gate — see
//     the package doc comment: termination is by idempotence, not a change check).
func (m *nodeMover) handleTrigger(msg moveMsg) {
	if m.centerOf == nil || m.sendMove == nil {
		return
	}
	s := m.snap.Load()
	if s == nil {
		return
	}
	selfCenter := s.c

	// The anchor-fanout only applies to a node with NO rule role of its own (mirrors
	// the historical node-6 special case: "neither a ruleSource/ruleFollowers
	// rule-node nor a gateNeighbors gate node"). A node that IS a rule-node (e.g.
	// node 2, itself adjacent to gate node 1) does NOT re-solve its adjacent gates
	// on its own trigger — only a pure anchor does; a rule-node's own trigger goes
	// through the swap/generic branches below instead.
	isRuleNode := m.sourceID != "" || len(m.followers) > 0
	if len(m.anchoredGates) > 0 && !isRuleNode {
		step := localStepR
		type cand struct {
			id string
			c  vec3
			ok bool
		}
		cands := make([]cand, 0, len(m.anchoredGates))
		anyOk := false
		for _, g := range m.anchoredGates {
			c, ok := m.centerOf(g)
			cands = append(cands, cand{id: g, c: c, ok: ok})
			anyOk = anyOk || ok
		}
		if !anyOk {
			return
		}
		shortest := math.Inf(1)
		for _, cd := range cands {
			if !cd.ok {
				continue
			}
			d := math.Round(cd.c.sub(selfCenter).length() / step)
			if d < shortest {
				shortest = d
			}
		}
		d := shortest * step
		m.tr.Breadcrumb("cascade.anchor.trigger", m.id, "", fmt.Sprintf("d=%.4f", d))
		for _, cd := range cands {
			if !cd.ok {
				continue
			}
			m.sendMove(cd.id, moveMsg{Kind: moveMsgKindGatePlace, NodeID: cd.id, SenderID: m.id, FromCenter: selfCenter, TargetC: d, SnapC: int(shortest)})
		}
		if m.followerOwner != "" {
			m.sendMove(m.followerOwner, moveMsg{Kind: moveMsgKindTrigger, NodeID: m.followerOwner, SenderID: m.id})
		}
		return
	}

	isMyFollower := false
	for _, f := range m.followers {
		if f == msg.SenderID {
			isMyFollower = true
			break
		}
	}
	if isMyFollower {
		// The sender is my own follower, but it moved on its OWN (it triggered ME,
		// which only happens via anchoredGates' followerOwner forward above) — swap
		// roles for this one re-trigger: re-equalize MY source-peer to the fresh
		// distance to the sender, holding the sender fixed.
		if m.sourceID == "" {
			return
		}
		senderCenter, ok := m.centerOf(msg.SenderID)
		if !ok {
			return
		}
		L := senderCenter.sub(selfCenter).length()
		m.sendMove(m.sourceID, moveMsg{Kind: moveMsgKindEqualize, NodeID: m.sourceID, FromCenter: selfCenter, TargetC: L})
		m.tr.Breadcrumb("cascade.swap", m.sourceID, "", fmt.Sprintf("targetC=%.4f", L))
		return
	}

	if m.isGate && msg.SenderID == "" {
		if m.gateEqualize != nil {
			m.gateEqualize(m.id)
			m.tr.Breadcrumb("gate.equalize", m.id, "", fmt.Sprintf("senderID=%q", msg.SenderID))
		}
		return
	}

	if m.sourceID == "" {
		return
	}
	sourceCenter, ok := m.centerOf(m.sourceID)
	if !ok {
		return
	}
	L := sourceCenter.sub(selfCenter).length()
	m.tr.Breadcrumb("cascade.trigger", m.id, "", fmt.Sprintf("source=%s L=%.4f senderID=%q", m.sourceID, L, msg.SenderID))
	for _, f := range m.followers {
		m.sendMove(f, moveMsg{Kind: moveMsgKindEqualize, NodeID: f, FromCenter: selfCenter, TargetC: L})
		m.tr.Breadcrumb("cascade.equalize", f, "", fmt.Sprintf("targetC=%.4f", L))
	}
	// Always forward — no delta-gate. Termination is by IDEMPOTENCE (the receiver
	// recomputes its own source length and equalizes its followers to it; if
	// nothing moved, the followers are already at that length, so the re-broadcast
	// is a no-op), not a sender-side "did anything change?" check. See the
	// project_lock_propagation_decentralized memory note this mirrors.
	for _, y := range m.forwardTargets {
		if y == msg.SenderID {
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
			if msg.testDone != nil {
				close(msg.testDone)
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

// handle applies one inbox message to this edge. For a move message it updates the
// matching endpoint geom, recomputes the edge's segment + arc, writes them onto the
// source Out, revises any in-flight bead, emits the new edge geometry, and updates
// the dest port's latency aggregate. A move that touches neither endpoint is ignored.
func (m *edgeMover) handle(msg moveMsg) {
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
	// Plain "move" messages have no effect under the polar layout;
	// position updates arrive as "center" messages instead.
	_ = msg
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
			if msg.testDone != nil {
				close(msg.testDone)
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
	// nodeSeeds/edgeSeeds are every node/edge's load-time seed geometry, captured ONCE at
	// construction (newMoveDispatch) in spec order — the deterministic directory-sorted
	// order LoadTopology read the topology in, NOT map iteration order. Exposed via
	// NodeSeeds/EdgeSeeds so main.go can seed the buffer's row tables from the diagram
	// itself before any node goroutine starts (CLAUDE.md: rows are a projection of the
	// diagram, not a discovery log built by racing goroutines to their first emit).
	nodeSeeds []NodeGeomSeed
	edgeSeeds []EdgeGeomSeed
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
	// posPersist / anchorPersist are the debounced disk persisters for the two FSM-applied
	// edits (node-drag position, ring-move anchor). Armed by EnableEditPersist after the
	// startup seed; nil until armed (tests that never arm).
	posPersist      *nodePosPersister
	anchorPersist   *anchorPersister
	overlaysPersist *overlaysPersister
	// quantOffsetPersist is the debounced disk persister for a node's scalar triple
	// (iTheta,iPhi,iR) about the scene center (quant_offset_persist.go) — the sole
	// persisted position source under the flat polar model. Armed by EnableEditPersist;
	// scheduled from RootMove for the dragged node.
	quantOffsetPersist *quantOffsetPersister
	// spherePersist is the debounced disk persister for the scene sphere (sphere_layout.go
	// md.sceneSphere), armed by EnableEditPersist. Its DEBOUNCE has no caller by design: the
	// sphere is "established once and never moves" (MODEL.md), so there is nothing to coalesce.
	// It is only ever flushed — by LoadSceneSphere on a content-fit, and by handleSaveMsg.
	// nil until armed (tests that never arm).
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

// NodeGeomSeed is one node's load-time seed geometry, exported in spec order and consumed
// by main.go's pre-launch tr.NodeGeometry loop (see the row-seeding comment in main.go).
// Ports are the SAME aimed-vs-static port geometry (buildPortGeoms/aimedPortPosDir,
// builders.go) the node's own live emit later produces — computed here from the same
// load-time geoms map, since every node's center is already known at load (buildPartnerCenterFn
// resolves partner centers straight off geoms, no goroutine needed). main.go copies these
// fields into the tr.NodeGeometry call (which additionally resolves the numeric KindID,
// since that table lives in Buffer).
type NodeGeomSeed struct {
	ID, Label, Kind              string
	CX, CY, CZ, Radius, SphereR  float64
	Ports                        []T.PortGeom
	VRX, VRY, VRZ, FRX, FRY, FRZ float64
}

// EdgeGeomSeed is one edge's load-time topology AND its real segment endpoints — the same
// edgeSegment(srcGeom, dstGeom, srcH, dstH) computation the edge's own live recomputeGeometry
// (node_move.go) uses, evaluated here against the load-time geoms so the seed row is never a
// degenerate 0,0,0→0,0,0 segment.
type EdgeGeomSeed struct {
	Label, SrcNode, DstNode string
	SX, SY, SZ, EX, EY, EZ  float64
}

// NodeSeeds returns every node's load-time seed geometry in SPEC ORDER (see
// MoveDispatch.nodeSeeds). Call after LoadTopology returns, before launching any node
// goroutine, and stream each entry via tr.NodeGeometry (main.go).
func (md *MoveDispatch) NodeSeeds() []NodeGeomSeed { return md.nodeSeeds }

// EdgeSeeds returns every edge's load-time seed topology (with real endpoint geometry) in
// SPEC ORDER. Call alongside NodeSeeds; stream each entry via tr.Geometry (main.go).
func (md *MoveDispatch) EdgeSeeds() []EdgeGeomSeed { return md.edgeSeeds }

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them. nodeOrder/edgeOrder are the
// SPEC order (deterministic directory-sorted order, not map iteration order) used to
// build md.nodeSeeds/edgeSeeds for buffer row seeding.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace, nodeOrder, edgeOrder []string) *MoveDispatch {
	// nil order (test call sites that don't care about seed order) falls back to sorted
	// map keys — still deterministic, just not necessarily spec order.
	if nodeOrder == nil {
		nodeOrder = make([]string, 0, len(geoms))
		for id := range geoms {
			nodeOrder = append(nodeOrder, id)
		}
		sort.Strings(nodeOrder)
	}
	if edgeOrder == nil {
		edgeOrder = make([]string, 0, len(edgeEndpoints))
		for label := range edgeEndpoints {
			edgeOrder = append(edgeOrder, label)
		}
		sort.Strings(edgeOrder)
	}
	md := &MoveDispatch{
		dispatch:      map[string]chan moveMsg{},
		nodeMovers:    map[string]*nodeMover{},
		edgeMovers:    map[string]*edgeMover{},
		edgeOut:       map[string]*Out{},
		tr:            tr,
		ov:            defaultOverlayState(),
		layoutHolders: map[string]*LayoutHolder{},
	}
	// Static partner-center lookup for the seed pass: every node's center is already known
	// off the load-time geoms map (no goroutine/atomic-snap needed), so this is the SAME
	// buildPartnerCenterFn the dynamic movers use below, just closed over geoms directly
	// instead of md.nodeMovers' atomic snaps. Kept per-node (not shared) to match
	// buildPartnerCenterFn's (nodeID, edgeEndpoints, centerOf) shape.
	seedPartnerCenter := func(nodeID string) partnerCenterFn {
		return buildPartnerCenterFn(nodeID, edgeEndpoints, func(otherID string) vec3 {
			if g, ok := geoms[otherID]; ok {
				return nodeWorldPos(g)
			}
			return vec3{}
		})
	}
	md.nodeSeeds = make([]NodeGeomSeed, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		g, ok := geoms[id]
		if !ok {
			continue
		}
		label := g.Label
		if label == "" {
			label = id
		}
		var cx, cy, cz float64
		if g.HasPos {
			c := nodeWorldPos(g)
			cx, cy, cz = c.X, c.Y, c.Z
		}
		ports := buildPortGeoms(g, aimedPortPosDir(g, seedPartnerCenter(id)))
		md.nodeSeeds = append(md.nodeSeeds, NodeGeomSeed{
			ID: id, Label: label, Kind: g.Kind,
			CX: cx, CY: cy, CZ: cz,
			Radius: nodeRadius(g.Kind), SphereR: effectiveRadius(g),
			Ports: ports,
			VRX:   verticalRingNormalX, VRY: verticalRingNormalY, VRZ: verticalRingNormalZ,
			FRX: flatRingNormalX, FRY: flatRingNormalY, FRZ: flatRingNormalZ,
		})
	}
	md.edgeSeeds = make([]EdgeGeomSeed, 0, len(edgeOrder))
	for _, label := range edgeOrder {
		ep, ok := edgeEndpoints[label]
		if !ok {
			continue
		}
		// Real endpoint geometry: the same edgeSegment computation recomputeGeometry
		// (below) uses on every live move, evaluated once here against the load-time
		// geoms so the seed row is never a degenerate 0,0,0->0,0,0 segment.
		var sx, sy, sz, ex, ey, ez float64
		if srcG, ok := geoms[ep.Source]; ok {
			if dstG, ok := geoms[ep.Target]; ok {
				seg := edgeSegment(srcG, dstG, ep.SourceHandle, ep.TargetHandle)
				sx, sy, sz = seg.Start.X, seg.Start.Y, seg.Start.Z
				ex, ey, ez = seg.End.X, seg.End.Y, seg.End.Z
			}
		}
		md.edgeSeeds = append(md.edgeSeeds, EdgeGeomSeed{
			Label: label, SrcNode: ep.Source, DstNode: ep.Target,
			SX: sx, SY: sy, SZ: sz, EX: ex, EY: ey, EZ: ez,
		})
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr)
		nm.sendMove = md.sendMove
		nm.centerOf = md.centerOfNode
		nm.commitLocal = md.commitNodeMoveLocal
		nm.gateEqualize = md.gateEqualizeNode
		nm.gatePlace = md.gatePlaceNode
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
	for _, nm := range md.nodeMovers {
		go nm.run(ctx)
	}
	for _, em := range md.edgeMovers {
		go em.run(ctx)
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

// cascadeRoleSpec is one node's authored cascade role — the per-node replacement for
// what used to be entries in three hardcoded package-level tables
// (ruleSource/ruleFollowers/gateNeighbors). loader.go's deriveCascadeRoles builds
// these from each node's own LocalPolars entries (Role: "source"/"follower"/"") and
// its own Gate flag; tests that build a MoveDispatch directly (bypassing the
// loader) declare the same data explicitly via ApplyCascadeRoles.
type cascadeRoleSpec struct {
	SourceID  string   // the ONE neighbor this node measures its reference length against.
	Followers []string // neighbors this node repositions (Equalize) when triggered.
	Gate      bool     // this node is a two-neighbor GATE node.
	GateA     string   // gate's first fixed neighbor (only meaningful when Gate).
	GateB     string   // gate's second fixed neighbor (only meaningful when Gate).
	// AnchoredGates: gate nodes that name THIS node as their "anchor" neighbor (a
	// gate's LocalPolar entry to this node has Role=="anchor" — loader.go
	// deriveCascadeRoles' second pass). EXPLICITLY authored per gate-neighbor pair,
	// NOT derived from mere gate-adjacency: a node can be one of a gate's two fixed
	// neighbors WITHOUT being its anchor (e.g. nodes 2 and 3 are gate 1's two fixed
	// neighbors, but neither is marked anchor — gate 1 only re-solves on its OWN
	// drag; only the historical node-6-shaped relationship to gates 9/10 is marked
	// anchor). Getting this wrong by deriving it from adjacency alone was a real bug
	// caught by the A/B drag proof (dragging node 3 wrongly re-solved gate 1).
	AnchoredGates []string
}

// ApplyCascadeRoles sets every named node's own cascade-role fields (sourceID,
// followers, isGate/gateA/gateB, anchoredGates) from roles, wires each gate node's
// placeEqualRadii closure, then derives the one CROSS-node field that isn't already
// explicit in roles: forwardTargets (the inverse of sourceID — every Y whose own
// source is this node) and followerOwner (the inverse of followers — the one
// rule-node, if any, that treats this node as a follower). anchoredGates is NOT
// derived from gate-adjacency (a node can be one of a gate's two fixed neighbors
// without being its anchor — see cascadeRoleSpec.AnchoredGates's doc) — it is taken
// verbatim from roles. Call once, after every node's mover exists (newMoveDispatch)
// and before Start. Unknown node ids in roles are ignored (a node absent from roles
// keeps its zero-value fields — no rule, not a gate, anchors nothing).
func (md *MoveDispatch) ApplyCascadeRoles(roles map[string]cascadeRoleSpec) {
	ids := make([]string, 0, len(md.nodeMovers))
	for id := range md.nodeMovers {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic derivation order

	for _, id := range ids {
		nm := md.nodeMovers[id]
		r, ok := roles[id]
		if !ok {
			continue
		}
		nm.sourceID = r.SourceID
		nm.followers = append([]string(nil), r.Followers...)
		nm.isGate = r.Gate
		nm.gateA, nm.gateB = r.GateA, r.GateB
		nm.anchoredGates = append([]string(nil), r.AnchoredGates...)
		if nm.isGate {
			aID, bID := nm.gateA, nm.gateB
			nm.placeEqualRadii = func(target vec3) vec3 {
				return md.placeEqualRadii(target, aID, bID)
			}
		}
	}
	for _, id := range ids {
		md.nodeMovers[id].forwardTargets = nil
		md.nodeMovers[id].followerOwner = ""
	}
	for _, yID := range ids {
		src := md.nodeMovers[yID].sourceID
		if src == "" {
			continue
		}
		if sm, ok := md.nodeMovers[src]; ok {
			sm.forwardTargets = append(sm.forwardTargets, yID)
		}
	}
	for _, rID := range ids {
		for _, f := range md.nodeMovers[rID].followers {
			if fol, ok := md.nodeMovers[f]; ok {
				fol.followerOwner = rID
			}
		}
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
	nm, ok := md.nodeMovers[nodeID]
	if !ok || !nm.isGate {
		return
	}
	c, ok := md.centerOfNode(nodeID)
	if !ok {
		return
	}
	md.equalizeEdgeCLocal(nodeID, nm.gateA, nm.gateB, c)
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
	nm, ok := md.nodeMovers[nodeID]
	if !ok || !nm.isGate {
		return
	}
	gA, gB := nm.gateA, nm.gateB
	var a, b vec3
	switch anchorID {
	case gA:
		other, okc := md.centerOfNode(gB)
		if !okc {
			return
		}
		a, b = anchorCenter, other
	case gB:
		other, okc := md.centerOfNode(gA)
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
	md.moveNodeAndSetEdgeCs(nodeID, newPos, gA, gB, snapC)
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
	// symptom). Nothing moves the sphere — MODEL.md: "It is established once and never moves."
	// Pan-moves-the-sphere is REJECTED doctrine, not a gap to fill; if it is ever revisited it
	// must be its own gesture, never a side effect of a camera move.
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

// EnableEditPersist arms disk persistence for the FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's x/y/z in <root>/nodes/<id>/meta.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/scene.json
//
// Node-position + anchor persistence needs the per-node/per-port files of the directory-tree
// form; for a monolithic topology.json (no per-node files) their root is "" and those two
// persisters no-op. Call AFTER SeedInitialViewpoint so the seed emits do not write the
// loaded state back.
func (md *MoveDispatch) EnableEditPersist(topologyPath string) {
	// sceneTreeRoot handles both the directory form and the file-inside-tree form (and
	// returns "" for a true monolithic topology with no tree), making the two-form bug
	// class unrepresentable here. Do not hand-roll os.Stat/IsDir — use sceneTreeRoot.
	root := sceneTreeRoot(topologyPath)
	md.posPersist = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.anchorPersist = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.overlaysPersist = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.spherePersist = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.quantOffsetPersist = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
