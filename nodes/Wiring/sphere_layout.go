// sphere_layout.go — graph-level node-position helpers for the polar layout.
// Node centers are stored absolutely (meta.json x/y/z).

package Wiring

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
