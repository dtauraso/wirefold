package Wiring

// derived.go — derive-on-read measurements over the polar root set
// (docs/planning/visual-editor/polar-coordinate-model.md §8c). Roots are the
// sole stored authority; sphere radius, surface coordinates, and ring normals
// are computed from roots when needed and never stored.

// sphereR is a center's sphere radius under soft membership: the max distance
// from the center to any of its surface nodes (the nodes it outputs to via the
// given edges). Returns 0 if the center has no resolvable surface nodes.
//
// "Grows around its nodes" — the farthest surface node sits on the ring, the
// rest inside. Computed from positions (roots → world), never stored.
func (rs rootSet) sphereR(center string, edges []sphereEdge) float64 {
	cw, ok := rs.world(center)
	if !ok {
		return 0
	}
	var r float64
	for _, e := range edges {
		if e.Source != center {
			continue
		}
		sw, ok := rs.world(e.Target)
		if !ok {
			continue
		}
		if d := sw.sub(cw).length(); d > r {
			r = d
		}
	}
	return r
}

// surfaceCoord is a surface node's polar coordinate measured FROM a center
// (the center's local frame, pole = world +y). Derived from the two roots.
func (rs rootSet) surfaceCoord(center, surface string) (polar, bool) {
	cw, ok := rs.world(center)
	if !ok {
		return polar{}, false
	}
	sw, ok := rs.world(surface)
	if !ok {
		return polar{}, false
	}
	return cart2polar(sw.sub(cw)), true
}

// Ring normals for a sphere's two great-circle tori, in the fixed world frame
// (Q10: the world/spheres do not rotate; only the camera does). The vertical
// ring (the one standing upright, containing the +y axis) lies in the x–y
// plane, normal +z. The flat ring lies in the x–z plane, normal +y.
var (
	verticalRingNormal = vec3{0, 0, 1} // chord-lock disk normal (spec §7)
	flatRingNormal     = vec3{0, 1, 0}
)
