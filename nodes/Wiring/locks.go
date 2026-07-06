package Wiring

import (
	"sort"

	T "github.com/dtauraso/wirefold/Trace"
)

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

// lockRecalc is the DECENTRALIZED lock solver in the SCENE-POLAR frame (polar-frame-rewrite.md:
// locks constrain scene-polar components between nodes; there is no center-node frame and no
// double-link graph). Given that `from` just moved to scene polar `fromPolar`, it checks EVERY
// active eqNodeNode constraint tying `self` to `from` and, for each, sets self's constrained
// scene-polar component to the sign-adjusted copy of the sender's component:
//
//	signSelf·compSelf(self) = signFrom·compFrom(from)  ⇒  compSelf(self) = signFrom·compFrom(from)·signSelf
//
// This is PURE POLAR — reads/writes ScenePolar components directly, no cart2polar, no vector
// subtraction, no center. Later constraints compose on earlier writes (a mirror's θ+φ pair).
// eqPortTorus locks are NOT node-center constraints and are not solved here (they only
// ring-project a port marker at emit, portWorldPosLocked).
//
// Returns (newPolar, false) when no active eqNodeNode touches (self,from), or when the change
// moves self less than lockPropEps in world space (polarDist between old and new scene polar) —
// the anti-divergence fixpoint: an over-constrained set converges and the cascade goes silent.
func (md *MoveDispatch) lockRecalc(m *nodeMover, from string, fromPolar polar) (polar, bool) {
	self := m.id
	sp := m.geom.ScenePolar
	orig := sp
	applied := false
	for _, eq := range md.polarEqsSnap() {
		if !eq.Active || eq.Kind != eqNodeNode {
			continue
		}
		var selfTerm, fromTerm polarTerm
		switch {
		case eq.A.Node == self && eq.B.Node == from:
			selfTerm, fromTerm = eq.A, eq.B
		case eq.B.Node == self && eq.A.Node == from:
			selfTerm, fromTerm = eq.B, eq.A
		default:
			continue
		}
		target := fromTerm.Sign * compOf(fromPolar, fromTerm.Comp) * selfTerm.Sign
		setCompOf(&sp, selfTerm.Comp, target)
		applied = true
	}
	if !applied {
		return polar{}, false
	}
	// World displacement between two scene-polar positions about the same center IS their
	// polar law-of-cosines distance — pure polar, no cartesian.
	if polarDist(orig, sp) <= lockPropEps {
		return polar{}, false
	}
	return sp, true
}

// lockNeighbors returns one moveMsg per DISTINCT neighbor `self` is tied to by an active
// eqNodeNode lock, excluding `exclude` (so the cascade never echoes straight back). Each
// message carries self's CURRENT scene polar (selfPolar) so the receiver copies a component
// directly — no cartesian. selfPolar is passed by the caller (RootMove's drag target polar, or
// a follower's just-applied new polar in nodeMover.handle) rather than re-read, to avoid racing
// the asynchronous "center" fan-out. eqPortTorus locks add no neighbor (port-marker only).
func (md *MoveDispatch) lockNeighbors(m *nodeMover, selfPolar polar, exclude string) []moveMsg {
	self := m.id
	var out []moveMsg
	seen := map[string]bool{}
	add := func(otherID string) {
		if otherID == "" || otherID == self || otherID == exclude || seen[otherID] {
			return
		}
		seen[otherID] = true
		out = append(out, moveMsg{
			Kind:      moveMsgKindLockUpdate,
			NodeID:    otherID,
			From:      self,
			FromPolar: selfPolar,
		})
	}
	for _, eq := range md.polarEqsSnap() {
		if !eq.Active || eq.Kind != eqNodeNode {
			continue
		}
		switch self {
		case eq.A.Node:
			add(eq.B.Node)
		case eq.B.Node:
			add(eq.A.Node)
		}
	}
	return out
}

// portTorusLocked returns true if there is an ACTIVE eqPortTorus lock on the given
// (node, port, isInput). Used by portWorldPosAimed to decide whether to ring-project
// a port (aimed_ports.go); a port∈torus lock never moves a node center.
func (md *MoveDispatch) portTorusLocked(node, port string, isInput bool) bool {
	for _, eq := range md.polarEqsSnap() {
		if eq.Kind == eqPortTorus && eq.Active &&
			eq.PortNode == node && eq.PortName == port && eq.PortIsInput == isInput {
			return true
		}
	}
	return false
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
	eqsSnap := md.polarEqsSnap()
	locks := make([]T.PolarLockPayload, len(eqsSnap))
	for i, eq := range eqsSnap {
		selected := containsInt(md.selectedLocks, i)
		owned := eqOwner(eq) == md.ruleCenter
		if eq.Kind == eqPortTorus {
			locks[i] = T.PolarLockPayload{
				Active:      eq.Active,
				Kind:        int(eqPortTorus),
				PortNode:    eq.PortNode,
				PortName:    eq.PortName,
				PortIsInput: eq.PortIsInput,
				TorusNode:   eq.TorusNode,
				Selected:    selected,
				Owned:       owned,
			}
			continue
		}
		locks[i] = T.PolarLockPayload{
			Center:   eq.Center,
			ANode:    eq.A.Node,
			ACode:    ruleTermCode(eq.A.Comp, eq.A.Sign),
			BNode:    eq.B.Node,
			BCode:    ruleTermCode(eq.B.Comp, eq.B.Sign),
			Active:   eq.Active,
			Kind:     int(eqNodeNode),
			Selected: selected,
			Owned:    owned,
		}
	}
	// Per-row Selected is now authoritative; the scalar index is kept for wire-shape
	// stability only (set to -1, unused by consumers).
	tr.PolarLocks(locks, -1)
}

// eqOwner returns the single node id that owns eq for the equation panel: Center for
// eqNodeNode, TorusNode (the "owning node" a port∈torus lock is authored against — see
// trySelectSphereRule's torus-hit case) for eqPortTorus. An equation belongs to exactly one
// center; the panel shows a row iff eqOwner(eq) == the panel's current center (md.ruleCenter).
func eqOwner(eq polarEq) string {
	if eq.Kind == eqPortTorus {
		return eq.TorusNode
	}
	return eq.Center
}

// containsInt reports whether v is present in xs.
func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// SelectLock toggles md.polarEqs[i]'s membership in the ORDERED md.selectedLocks list: if i
// is already selected it is removed (preserving the order of the rest); otherwise, if i is
// in range, it is appended. Out-of-range i is a no-op. Re-emits the committed list so the
// panel highlight and diagram guide overlays (which follow selectedLocks) follow.
func (md *MoveDispatch) SelectLock(i int, tr *T.Trace) {
	if idx := indexOfInt(md.selectedLocks, i); idx >= 0 {
		next := append([]int(nil), md.selectedLocks[:idx]...)
		next = append(next, md.selectedLocks[idx+1:]...)
		md.selectedLocks = next
	} else if i >= 0 && i < len(md.polarEqsSnap()) {
		md.selectedLocks = append(append([]int(nil), md.selectedLocks...), i)
	}
	md.emitPolarLocks(tr)
}

// indexOfInt returns the index of v in xs, or -1 if not present.
func indexOfInt(xs []int, v int) int {
	for idx, x := range xs {
		if x == v {
			return idx
		}
	}
	return -1
}

// pruneSelectionOffCenter unhighlights any selected equation whose Center is not the panel's
// new Center — the equation panel lists only equations under the current Center, so an
// off-center selection is no longer in the visible list. The panel row highlight and the
// diagram guide overlay both follow selectedLocks, so pruning here removes both for that
// entry. No-op when nothing selected or every selection is still on-center.
func (md *MoveDispatch) pruneSelectionOffCenter(center string, tr *T.Trace) {
	if len(md.selectedLocks) == 0 {
		return
	}
	eqsSnap := md.polarEqsSnap()
	next := make([]int, 0, len(md.selectedLocks))
	changed := false
	for _, i := range md.selectedLocks {
		// Same ownership definition the panel's Owned bit uses (eqOwner): node-node → Center,
		// port-torus → TorusNode. Display and selection must agree on which center owns an eq.
		if i >= 0 && i < len(eqsSnap) && eqOwner(eqsSnap[i]) == center {
			next = append(next, i)
		} else {
			changed = true
		}
	}
	if changed {
		md.selectedLocks = next
		md.emitPolarLocks(tr)
	}
}

// ToggleLockActive flips md.polarEqs[i].Active (activate/deactivate). Out-of-range i is a
// no-op. Re-emits the committed list and schedules persistence.
func (md *MoveDispatch) ToggleLockActive(i int, tr *T.Trace) {
	eqsSnap := md.polarEqsSnap()
	if i < 0 || i >= len(eqsSnap) {
		return
	}
	next := append([]polarEq(nil), eqsSnap...)
	next[i].Active = !next[i].Active
	md.setPolarEqs(next)
	md.emitPolarLocks(tr)
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqsSnap())
	}
	// A toggled eqPortTorus lock changes its port's resolved geometry (ring-projected
	// when active, plain aimed when not) — re-emit immediately so the port visibly
	// moves rather than waiting for the next unrelated node move.
	if eq := next[i]; eq.Kind == eqPortTorus {
		md.reemitPortTorusGeometry(eq.PortNode)
	}
}

// DeleteSelectedLock deletes every selected md.polarEqs entry that is deactivated (!Active)
// — an active equation must be deactivated first, so a selected-but-active entry is left in
// place. Deletes high-to-low index so earlier deletions don't shift later ones. No-op if
// nothing is selected or none of the selected entries are deactivated. Clears selectedLocks,
// re-emits the committed list, and schedules persistence.
func (md *MoveDispatch) DeleteSelectedLock(tr *T.Trace) {
	if len(md.selectedLocks) == 0 {
		return
	}
	eqsSnap := md.polarEqsSnap()
	toDelete := make([]int, 0, len(md.selectedLocks))
	for _, i := range md.selectedLocks {
		if i >= 0 && i < len(eqsSnap) && !eqsSnap[i].Active {
			toDelete = append(toDelete, i)
		}
	}
	if len(toDelete) == 0 {
		return
	}
	sort.Sort(sort.Reverse(sort.IntSlice(toDelete)))
	next := append([]polarEq(nil), eqsSnap...)
	for _, i := range toDelete {
		next = append(next[:i], next[i+1:]...)
	}
	md.setPolarEqs(next)
	md.selectedLocks = nil
	md.emitPolarLocks(tr)
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqsSnap())
	}
}
