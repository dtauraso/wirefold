package Wiring

import "math"

// prism.go — the enclosing rectangular prism (container) and the node root
// coordinate, for the polar layout model
// (docs/planning/visual-editor/polar-coordinate-model.md §1, §8a, §8b).
//
// The prism is the axis-aligned bounding box of the diagram's node positions.
// Its center is the polar origin (= the large sphere's center). Saved files
// store node positions as Cartesian in this frame; runtime uses polar roots
// measured from the prism center.

// prism is an axis-aligned rectangular box. Min/Max are opposite corners.
type prism struct {
	Min, Max vec3
}

// center returns the box midpoint — the polar origin.
func (p prism) center() vec3 {
	return vec3{
		X: (p.Min.X + p.Max.X) / 2,
		Y: (p.Min.Y + p.Max.Y) / 2,
		Z: (p.Min.Z + p.Max.Z) / 2,
	}
}

// contains reports whether v lies within the (inclusive) box.
func (p prism) contains(v vec3) bool {
	return v.X >= p.Min.X && v.X <= p.Max.X &&
		v.Y >= p.Min.Y && v.Y <= p.Max.Y &&
		v.Z >= p.Min.Z && v.Z <= p.Max.Z
}

// prismFromPoints builds the axis-aligned bounding box of the given Cartesian
// points. Returns the zero prism for an empty input.
func prismFromPoints(pts []vec3) prism {
	if len(pts) == 0 {
		return prism{}
	}
	mn, mx := pts[0], pts[0]
	for _, v := range pts[1:] {
		mn = vec3{math.Min(mn.X, v.X), math.Min(mn.Y, v.Y), math.Min(mn.Z, v.Z)}
		mx = vec3{math.Max(mx.X, v.X), math.Max(mx.Y, v.Y), math.Max(mx.Z, v.Z)}
	}
	return prism{Min: mn, Max: mx}
}

// largeSphereRadius is the radius that circumscribes all points about the given
// center — the max distance from center to any point — so every node sits
// inside the large sphere.
func largeSphereRadius(center vec3, pts []vec3) float64 {
	var r float64
	for _, v := range pts {
		if d := v.sub(center).length(); d > r {
			r = d
		}
	}
	return r
}

// nodeRoot is a node's authoritative outer polar coordinate, measured from the
// large sphere's center (the prism center). It replaces the stored x/y/z.
type nodeRoot struct {
	polar
}

// rootFromCartesian converts a node's world Cartesian position to its root,
// measuring from the given origin (prism center).
func rootFromCartesian(pos, origin vec3) nodeRoot {
	return nodeRoot{cart2polar(pos.sub(origin))}
}

// worldFromRoot converts a root back to a world Cartesian position about origin.
func worldFromRoot(r nodeRoot, origin vec3) vec3 {
	return polar2cart(r.polar).add(origin)
}

// rootSet is the runtime polar layout: the container frame plus every node's
// authoritative outer polar coordinate. Built at load from world Cartesian
// centers (spec §8b: prism → origin → roots). World positions are recovered via
// worldFromRoot(roots[id], origin); the prism/origin are recomputed
// deterministically from the same centers, so save/load is stable.
type rootSet struct {
	prism  prism
	origin vec3
	radius float64             // large sphere radius (circumscribes the nodes)
	roots  map[string]nodeRoot // node id → outer polar coord
}

// buildRoots constructs the rootSet from id→world-center positions: bounding-box
// prism, its center as the polar origin, the circumscribing radius, and each
// node's root measured from the origin.
func buildRoots(centers map[string]vec3) rootSet {
	pts := make([]vec3, 0, len(centers))
	for _, c := range centers {
		pts = append(pts, c)
	}
	p := prismFromPoints(pts)
	origin := p.center()
	rs := rootSet{
		prism:  p,
		origin: origin,
		radius: largeSphereRadius(origin, pts),
		roots:  make(map[string]nodeRoot, len(centers)),
	}
	for id, c := range centers {
		rs.roots[id] = rootFromCartesian(c, origin)
	}
	return rs
}

// world recovers a node's world Cartesian position from its root.
func (rs rootSet) world(id string) (vec3, bool) {
	r, ok := rs.roots[id]
	if !ok {
		return vec3{}, false
	}
	return worldFromRoot(r, rs.origin), true
}

// reOrigin re-bases the polar frame to a new origin while preserving every
// node's world Cartesian position. For each node: recover world from old root,
// re-encode relative to newOrigin. The prism and radius fields are intentionally
// NOT updated — the prism is a load-time bounding box used only to seed the
// initial origin, not kept current during pan.
func (rs *rootSet) reOrigin(newOrigin vec3) {
	for id, root := range rs.roots {
		w := worldFromRoot(root, rs.origin)
		rs.roots[id] = rootFromCartesian(w, newOrigin)
	}
	rs.origin = newOrigin
}
