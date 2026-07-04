package Wiring

import T "github.com/dtauraso/wirefold/Trace"

// locks.go — the polar-equation lock engine. A lock is an EQUATION between two terms,
// each a (node, component, sign) about a shared Center: `signA·compA(A) = signB·compB(B)`,
// with the component being θ or φ of the node's polar coordinate on the Center's surface
// (pole = +y). Equations read and write LINK POLAR STATE directly (links.go) — no
// cart2polar reconstruction; the only world→polar conversion is refreshLink at load and
// the drag edge. World is derived from the link polar (polar2cart) for the render bridge.
//
// There is no leader/follower and no direction field: whatever ordering an equation has is
// carried by its two terms (their components and signs). The same node pair may carry more
// than one equation (a mirror is two: θ-equal + φ-opposite). When a node is the moved node,
// the equations touching it are satisfied by writing the OTHER term's component; that is a
// property of solving, not a stored direction.

// polarComp names the polar-coordinate component an equation term constrains: the two
// angles (θ, φ) or the radius (r). r carries no sign — a negative radius is meaningless.
type polarComp int

const (
	compTheta polarComp = iota // θ: polar angle from +y
	compPhi                    // φ: azimuth around +y
	compR                      // r: radius (distance from Center); always +1 sign
)

// polarTerm is one side of an equation: a node's polar component about the Center, times a
// sign (+1 or -1).
type polarTerm struct {
	Node string
	Comp polarComp
	Sign float64 // +1 or -1
}

// eqKind discriminates what a polarEq constrains. The zero value (eqNodeNode) is today's
// (node,comp)=(node,comp) equation, so existing/loaded equations that never set Kind decode
// as eqNodeNode unchanged. eqPortTorus is a `port ∈ torus` membership lock: STAGE 1 is
// author/persist/display only — applyPolarEqs skips it (no geometric effect yet; that is a
// later stage's solve).
type eqKind int

const (
	eqNodeNode  eqKind = iota // A.Comp = B.Comp about Center (today's equation)
	eqPortTorus               // PortNode/PortName/PortIsInput's port lies on TorusNode's border ring
)

// polarEq is one polar equation. eqNodeNode uses Center/A/B (A and B held equal after their
// signs). eqPortTorus uses PortNode/PortName/PortIsInput (the constrained port) and TorusNode
// (the ring it must lie on); Center/A/B are unused for that kind.
type polarEq struct {
	Kind   eqKind
	Center string
	A      polarTerm
	B      polarTerm
	Active bool

	// eqPortTorus fields (Kind == eqPortTorus). Inert this stage: applyPolarEqs never writes
	// through a port∈torus entry, so authoring one moves nothing.
	PortNode    string
	PortName    string
	PortIsInput bool
	TorusNode   string
}

// compOf reads a polar's θ, φ, or r.
func compOf(p polar, c polarComp) float64 {
	switch c {
	case compPhi:
		return p.Phi
	case compR:
		return p.R
	default:
		return p.Theta
	}
}

// setCompOf writes a polar's θ, φ, or r, leaving the other coordinates untouched.
func setCompOf(p *polar, c polarComp, v float64) {
	switch c {
	case compPhi:
		p.Phi = v
	case compR:
		p.R = v
	default:
		p.Theta = v
	}
}

// ensureEqLinks guarantees a movement link exists between an equation's Center and EACH of
// its term nodes, so applyPolarEqs has link-polar state to ride even when no topology EDGE
// connects them. Movement links are consumed only by the lock engine, so an extra link is
// inert until an equation references it. Called when an equation is authored (gesture.go)
// and at load (LoadPolarEqs). A freshly-added link is seeded from the two nodes' current
// world centers; if either center isn't resolvable yet, it is left unseeded and gets its
// polar on the first drag-edge refresh (refreshLinksTouching). A degenerate Center==node
// term is skipped — a node has no polar coordinate about itself.
func (md *MoveDispatch) ensureEqLinks(eq polarEq) {
	if eq.Kind == eqPortTorus {
		return // inert this stage: no solve rides a link for a port∈torus lock yet
	}
	md.ensureLink(eq.Center, eq.A.Node)
	md.ensureLink(eq.Center, eq.B.Node)
}

func (md *MoveDispatch) ensureLink(a, b string) {
	if a == "" || b == "" || a == b || md.linkBetween(a, b) != nil {
		return
	}
	md.addLink(a, b)
	pa, oka := md.centerOfNode(a)
	pb, okb := md.centerOfNode(b)
	if oka && okb {
		refreshLink(md.linkBetween(a, b), pa, pb)
	}
}

// applyPolarEqs returns the new world positions of the nodes written to satisfy every
// equation that touches movedID. For each such equation the moved node's term is the
// input; the OTHER term's constrained component is solved for and written on its link,
// keeping that node's remaining polar coordinates. Equations are applied in order, so two
// equations over the same pair (e.g. a mirror's θ and φ) compose: the second reads the
// first's write. World is derived (polar2cart) only for the render bridge.
func (md *MoveDispatch) applyPolarEqs(movedID string, pos func(string) (vec3, bool)) map[string]vec3 {
	out := map[string]vec3{}
	for _, eq := range md.polarEqs {
		if !eq.Active {
			continue
		}
		if eq.Kind == eqPortTorus {
			continue // STAGE 1: authorable/persisted/displayed only — no solve yet
		}
		var moved, other polarTerm
		switch movedID {
		case eq.A.Node:
			moved, other = eq.A, eq.B
		case eq.B.Node:
			moved, other = eq.B, eq.A
		default:
			continue
		}
		movedLink := md.linkBetween(eq.Center, moved.Node)
		otherLink := md.linkBetween(eq.Center, other.Node)
		if movedLink == nil || otherLink == nil {
			continue
		}
		mp, ok := movedLink.polarOf(eq.Center, moved.Node)
		if !ok {
			continue
		}
		op, ok := otherLink.polarOf(eq.Center, other.Node)
		if !ok {
			continue
		}
		// signMoved·compMoved = signOther·compOther  ⇒  compOther = signMoved·compMoved/signOther.
		// Sign ∈ {+1,-1}, so dividing by it is multiplying by it.
		target := moved.Sign * compOf(mp, moved.Comp) * other.Sign
		np := op
		setCompOf(&np, other.Comp, target)
		otherLink.setPolar(eq.Center, other.Node, np)
		c, ok := pos(eq.Center)
		if !ok {
			continue
		}
		out[other.Node] = polar2cart(np).add(c)
	}
	return out
}

// portTorusLocked returns true if there is an ACTIVE eqPortTorus lock on the given
// (node, port, isInput). Used by applyPortTorusColinearity to find coupled edges.
func (md *MoveDispatch) portTorusLocked(node, port string, isInput bool) bool {
	for _, eq := range md.polarEqs {
		if eq.Kind == eqPortTorus && eq.Active &&
			eq.PortNode == node && eq.PortName == port && eq.PortIsInput == isInput {
			return true
		}
	}
	return false
}

// applyPortTorusColinearity implements STAGE 2 of the `port ∈ torus` lock: when both
// endpoints of an edge have an ACTIVE eqPortTorus lock on their respective ports (the
// source's out-port pinned to its own node's border ring, the destination's in-port
// pinned to its own node's border ring), the edge S.out→D.in is colinear (S_center,
// S.port, D.port, D_center on one line) IFF S_center.z == D_center.z — because the
// node-border ring is drawn in the world X-Y plane (identity rotation, unit-scaled
// TorusGeometry — buffer-scene.tsx ~251-265) and the aimed port sits on that ring
// exactly when the aim direction (unit(otherCenter-thisCenter)) has zero z, i.e. when
// the two centers share a z.
//
// So the solve is: for each coupled edge with movedID as one endpoint, set the OTHER
// endpoint's z to movedID's z (x/y unchanged) and emit that as its new world center.
// The existing portWorldPosAimed recompute (driven by the emitted center) then places
// both ports back on their rings, colinear — no separate port write needed.
//
// Edge list: reused from md.edgeMovers (srcID/srcH/dstID/dstH), the same live
// edge-endpoint state RootMove already reads via heldEdges — no new adjacency was
// added.
//
// ONE-HOP PER CALL: a single call only solves the edges directly touching movedID.
// RootMove drives transitivity: it runs this inside its bounded worklist fixpoint
// (alongside applyPolarEqs), calling it once per worklist node as propagation reaches
// it — so a chain of torus-coupled edges (A—torus—B—torus—C) fully resolves when
// either end (or the middle) moves, not just the hop directly off the dragged node.
func (md *MoveDispatch) applyPortTorusColinearity(movedID string, pos func(string) (vec3, bool)) map[string]vec3 {
	out := map[string]vec3{}
	moved, ok := pos(movedID)
	if !ok {
		return out
	}
	for _, em := range md.edgeMovers {
		if em.srcID == em.dstID {
			continue // guard against a degenerate self-edge
		}
		coupled := md.portTorusLocked(em.srcID, em.srcH, false) &&
			md.portTorusLocked(em.dstID, em.dstH, true)
		if !coupled {
			continue
		}
		var depID string
		switch movedID {
		case em.srcID:
			depID = em.dstID
		case em.dstID:
			depID = em.srcID
		default:
			continue
		}
		if depID == movedID {
			continue // guard against moving the dragged node itself
		}
		dep, ok := pos(depID)
		if !ok {
			continue
		}
		dep.Z = moved.Z
		out[depID] = dep
	}
	return out
}

// reemitPortTorusGeometry re-emits nodeID's node-geometry plus every incident edge's
// segment, so a `port ∈ torus` lock's geometric effect (portWorldPosAimed's
// ring-projection, see aimed_ports.go) is visible IMMEDIATELY when the lock is
// authored (addPortTorusLock) or toggled active/inactive (ToggleLockActive) — not
// only on the next unrelated node move. Mirrors fanCenters' aimedReemit trick: send
// each mover a no-op "same center" message so it recomputes+re-emits on its own
// goroutine. Dual-pathed like ResendGeometry: direct emit when movers aren't
// started (headless tests), inbox-routed when they are (production).
func (md *MoveDispatch) reemitPortTorusGeometry(nodeID string) {
	nm, ok := md.nodeMovers[nodeID]
	if !ok {
		return
	}
	if !md.started {
		nm.emitGeometry()
		for _, em := range md.edgeMovers {
			if em.srcID == nodeID || em.dstID == nodeID {
				em.emitGeometry()
			}
		}
		return
	}
	s := nm.snap.Load()
	if s == nil {
		return
	}
	if ch, ok := md.dispatch[nodeID]; ok {
		cc := s.c
		ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: nodeID, Center: &cc, ReachR: s.reach}
	}
	for edgeID, em := range md.edgeMovers {
		if em.srcID != nodeID && em.dstID != nodeID {
			continue
		}
		if ch, ok := md.dispatch[edgeID]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenters, Centers: map[string]vec3{nodeID: s.c}}
		}
	}
}

// emitPolarLocks emits the FULL committed polar-equation lock list (KindPolarLocks). Call
// from every mutation point: rule completion (gesture.go), ToggleLockActive, SelectLock,
// DeleteSelectedLock, and LoadPolarEqs. No-op when tr is nil (headless tests).
func (md *MoveDispatch) emitPolarLocks(tr *T.Trace) {
	if tr == nil {
		return
	}
	locks := make([]T.PolarLockPayload, len(md.polarEqs))
	for i, eq := range md.polarEqs {
		if eq.Kind == eqPortTorus {
			locks[i] = T.PolarLockPayload{
				Active:      eq.Active,
				Kind:        int(eqPortTorus),
				PortNode:    eq.PortNode,
				PortName:    eq.PortName,
				PortIsInput: eq.PortIsInput,
				TorusNode:   eq.TorusNode,
			}
			continue
		}
		locks[i] = T.PolarLockPayload{
			Center: eq.Center,
			ANode:  eq.A.Node,
			ACode:  ruleTermCode(eq.A.Comp, eq.A.Sign),
			BNode:  eq.B.Node,
			BCode:  ruleTermCode(eq.B.Comp, eq.B.Sign),
			Active: eq.Active,
			Kind:   int(eqNodeNode),
		}
	}
	tr.PolarLocks(locks, md.selectedLockIndex)
}

// SelectLock focuses md.polarEqs[i] as the panel's clicked row (selectedLockIndex). Out-of-
// range i clears the focus (-1). Clicking the ALREADY-selected row toggles it OFF (-1) — that
// unhighlights the equation, and since the diagram guides follow selectedLockIndex, it also
// removes the guide overlay. Re-emits the committed list so the panel highlight follows.
func (md *MoveDispatch) SelectLock(i int, tr *T.Trace) {
	if i < 0 || i >= len(md.polarEqs) || i == md.selectedLockIndex {
		md.selectedLockIndex = -1
	} else {
		md.selectedLockIndex = i
	}
	md.emitPolarLocks(tr)
}

// pruneSelectionOffCenter unhighlights the selected equation when the panel's Center moves to
// a node that equation does NOT belong to — the equation panel lists only equations under the
// current Center, so an off-center selection is no longer in the visible list. The panel row
// highlight and the diagram guide overlay both follow selectedLockIndex, so clearing it here
// removes both at once. No-op when nothing is selected or the selection is still on-center.
func (md *MoveDispatch) pruneSelectionOffCenter(center string, tr *T.Trace) {
	if md.selectedLockIndex >= 0 && md.selectedLockIndex < len(md.polarEqs) &&
		md.polarEqs[md.selectedLockIndex].Center != center {
		md.selectedLockIndex = -1
		md.emitPolarLocks(tr)
	}
}

// ToggleLockActive flips md.polarEqs[i].Active (activate/deactivate). Out-of-range i is a
// no-op. Re-emits the committed list and schedules persistence.
func (md *MoveDispatch) ToggleLockActive(i int, tr *T.Trace) {
	if i < 0 || i >= len(md.polarEqs) {
		return
	}
	md.polarEqs[i].Active = !md.polarEqs[i].Active
	md.emitPolarLocks(tr)
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqs)
	}
	// A toggled eqPortTorus lock changes its port's resolved geometry (ring-projected
	// when active, plain aimed when not) — re-emit immediately so the port visibly
	// moves rather than waiting for the next unrelated node move.
	if eq := md.polarEqs[i]; eq.Kind == eqPortTorus {
		md.reemitPortTorusGeometry(eq.PortNode)
	}
}

// DeleteSelectedLock deletes md.polarEqs[selectedLockIndex], but ONLY when that equation
// exists AND is deactivated (!Active) — an active equation must be deactivated first. No-op
// otherwise. Fixes up selectedLockIndex, re-emits the committed list, and schedules
// persistence.
func (md *MoveDispatch) DeleteSelectedLock(tr *T.Trace) {
	i := md.selectedLockIndex
	if i < 0 || i >= len(md.polarEqs) {
		return
	}
	if md.polarEqs[i].Active {
		return
	}
	md.polarEqs = append(md.polarEqs[:i], md.polarEqs[i+1:]...)
	md.selectedLockIndex = -1
	md.emitPolarLocks(tr)
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqs)
	}
}
