package Wiring

// links.go — the double-link movement graph with LINK-CARRIED POLAR STATE
// (docs/planning/visual-editor/double-link-polar-model.md). A movementLink is a double
// link between two nodes; it holds the polar relationship between them — the radius and
// the direction of each node on the OTHER's surface (pole = +y). This polar state is the
// layout's working representation: locks read and write it directly, with NO cart2polar
// reconstruction. World positions are DERIVED from it (polar2cart) for the render bridge;
// the only world→polar conversion is at the drag edge (refresh), where the mouse hands in
// a world point.

// movementLink is one undirected double link. BfromA is B's polar coordinate in A's frame
// (its radius-point on A's surface); AfromB is A's in B's frame. Both share R = |A−B|.
type movementLink struct {
	A      string
	B      string
	BfromA polar // B seen from A (pole +y)
	AfromB polar // A seen from B
}

// addLink appends a double link A↔B (polar state filled in by refresh).
func (md *MoveDispatch) addLink(a, b string) {
	md.links = append(md.links, movementLink{A: a, B: b})
}

// polarOf returns `node`'s polar in `frame`'s frame for this link, where {frame,node}
// are the link's two endpoints. ok=false if the pair doesn't match the link.
func (lk movementLink) polarOf(frame, node string) (polar, bool) {
	switch {
	case lk.A == frame && lk.B == node:
		return lk.BfromA, true
	case lk.B == frame && lk.A == node:
		return lk.AfromB, true
	}
	return polar{}, false
}

// setPolar writes `node`'s polar in `frame`'s frame on this link (and leaves the reverse
// direction's R consistent — callers refresh the reverse separately when needed).
func (lk *movementLink) setPolar(frame, node string, p polar) {
	switch {
	case lk.A == frame && lk.B == node:
		lk.BfromA = p
	case lk.B == frame && lk.A == node:
		lk.AfromB = p
	}
}

// touches reports whether node id is an endpoint of this link.
func (lk movementLink) touches(id string) bool { return lk.A == id || lk.B == id }

// refreshLink recomputes a link's polar state from the two endpoints' world positions.
// This is the ONE world→polar conversion (cart2polar), used at load and at the drag edge.
func refreshLink(lk *movementLink, posA, posB vec3) {
	lk.BfromA = cart2polar(posB.sub(posA))
	lk.AfromB = cart2polar(posA.sub(posB))
}

// refreshLinksTouching recomputes the polar of every link incident to nodeID from current
// world positions (pos resolves a node's world; the dragged node resolves to its target).
// This is the drag-edge cart2polar: a mouse-driven world move enters the polar model here.
func (md *MoveDispatch) refreshLinksTouching(nodeID string, pos func(string) (vec3, bool)) {
	for i := range md.links {
		lk := &md.links[i]
		if !lk.touches(nodeID) {
			continue
		}
		a, okA := pos(lk.A)
		b, okB := pos(lk.B)
		if !okA || !okB {
			continue
		}
		refreshLink(lk, a, b)
	}
}

// linkBetween returns a pointer to the link connecting a and b (either orientation),
// or nil if there is none.
func (md *MoveDispatch) linkBetween(a, b string) *movementLink {
	for i := range md.links {
		lk := &md.links[i]
		if (lk.A == a && lk.B == b) || (lk.A == b && lk.B == a) {
			return lk
		}
	}
	return nil
}

// initLinkPolar fills every link's polar state from the current node world positions
// (called once at load). pos resolves a node's world center.
func (md *MoveDispatch) initLinkPolar(pos func(string) (vec3, bool)) {
	for i := range md.links {
		lk := &md.links[i]
		a, okA := pos(lk.A)
		b, okB := pos(lk.B)
		if !okA || !okB {
			continue
		}
		refreshLink(lk, a, b)
	}
}

// registerMovementLinks declares the double-link movement graph by DERIVING it
// from the topology edges: one undirected link per unique unordered {source,
// target} pair, in first-occurrence order, registered only when both endpoints
// are loaded. A bidirectional pair (e.g. 1↔4, present as both the 1→4 edge and
// the 4→1 feedback edge) collapses to a single link. No node ids are hardcoded —
// the graph follows whatever topology was loaded.
func (md *MoveDispatch) registerMovementLinks(edges []sphereEdge, has func(string) bool) {
	seen := map[[2]string]bool{}
	for _, e := range edges {
		a, b := e.Source, e.Target
		if a == b {
			continue
		}
		key := [2]string{a, b}
		if a > b {
			key = [2]string{b, a}
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if has(a) && has(b) {
			md.addLink(a, b)
		}
	}
}
