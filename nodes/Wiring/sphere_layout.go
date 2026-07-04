// sphere_layout.go — graph-level node-position helpers for the polar layout.
// Node centers are stored absolutely (meta.json x/y/z).

package Wiring

import "math"

// sphereEdge is a DIRECTED connection: Source outputs to Target.
type sphereEdge struct {
	Source string
	Target string
}

// sceneSphere is the FIRST-CLASS scene reference (polar-model.md): the fixed frame every
// node's SCENE polar (r, θ, φ) is measured about. It is NOT the derived content-sphere
// centroid (contentSphereOf) — that moves when nodes move, which is circular. The Center
// is the single cartesian value in the system (the world anchor, persisted in scene.json);
// Radius is long enough to fit the whole diagram and re-fits on pan. A node's world center
// is DERIVED as Center + polar2cart(scenePolar); its scene polar is cart2polar(world −
// Center). Panning moves Center and recomputes every node's scene polar + re-fits Radius.
type sceneSphere struct {
	Center vec3
	Radius float64
}

// contentFitSceneSphere derives a sensible DEFAULT scene sphere from the current node
// centers (bbox midpoint + fit radius) — used only when scene.json has no persisted sphere
// yet, so an existing scene gets a sane reference without any authored value. Once
// persisted, the stored Center is authoritative and is NOT re-derived from node positions.
func contentFitSceneSphere(centers map[string]vec3) sceneSphere {
	c, r := contentSphereOf(centers)
	return sceneSphere{Center: c, Radius: r}
}

// fitSceneRadius returns a radius long enough to fit every node center about the GIVEN
// (fixed) center — unlike contentSphereOf, which also derives the center. Used on pan: the
// sceneSphere Center moves by the pan delta but stays the authoritative anchor, and only the
// Radius re-fits around it. max(center-distance)*1.1, floor 1; empty centers → 1.
func fitSceneRadius(centers map[string]vec3, center vec3) float64 {
	maxDist := 0.0
	for _, p := range centers {
		if math.IsInf(p.X, 0) || math.IsNaN(p.X) || math.IsInf(p.Y, 0) || math.IsNaN(p.Y) ||
			math.IsInf(p.Z, 0) || math.IsNaN(p.Z) {
			continue
		}
		d := p.sub(center).length()
		if d > maxDist {
			maxDist = d
		}
	}
	r := maxDist * 1.1
	if r < 1 {
		r = 1
	}
	return r
}
