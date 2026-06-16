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
