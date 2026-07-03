package Wiring

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

// polarEq is one polar equation about a Center: A and B held equal after their signs.
type polarEq struct {
	Center string
	A      polarTerm
	B      polarTerm
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

// applyPolarEqs returns the new world positions of the nodes written to satisfy every
// equation that touches movedID. For each such equation the moved node's term is the
// input; the OTHER term's constrained component is solved for and written on its link,
// keeping that node's remaining polar coordinates. Equations are applied in order, so two
// equations over the same pair (e.g. a mirror's θ and φ) compose: the second reads the
// first's write. World is derived (polar2cart) only for the render bridge.
func (md *MoveDispatch) applyPolarEqs(movedID string, pos func(string) (vec3, bool)) map[string]vec3 {
	out := map[string]vec3{}
	for _, eq := range md.polarEqs {
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
