// aimed_ports.go — port-marker placement at the emit boundary.
//
// The dynamic aim model (a port pointing toward its connected node's moving center) is GONE
// (polar-frame-rewrite.md option A): an edge runs node-to-node, so a port no longer aims at a
// partner and there is no aimed-port registry. Aiming required `targetCenter − nodeWorldPos`,
// a mid-pipeline vector subtraction the polar frame forbids.
//
// What remains here is the port MARKER placement used only at emit: ring-anchor direction at
// the port's radius, with a torus-locked port ring-projected onto the node's border ring.

package Wiring

// ringProjectDir projects dir onto the world X-Y plane (dropping Z) and normalizes it — the
// direction from a node's center to the point on its border ring (drawn with identity
// rotation, in the world z=0 plane through the center) nearest dir. When dir is (near-)
// parallel to world +Z, falls back to projecting fallbackDir the same way; when even that is
// degenerate, falls back to world +X so the result is always a well-defined unit vector.
func ringProjectDir(dir, fallbackDir vec3) vec3 {
	flat := vec3{X: dir.X, Y: dir.Y, Z: 0}
	if l := flat.length(); l > 1e-9 {
		return flat.scale(1 / l)
	}
	flat = vec3{X: fallbackDir.X, Y: fallbackDir.Y, Z: 0}
	if l := flat.length(); l > 1e-9 {
		return flat.scale(1 / l)
	}
	return vec3{X: 1, Y: 0, Z: 0}
}

// portWorldPosLocked returns a port marker's world point at the emit boundary: ring-anchor
// placement at the port's own radius from the node center. A torus-locked port instead
// ring-projects onto the node's border ring (major radius nodeRadius) so the streamed marker
// sits ON the ring. No aim, no vector subtraction — the only cartesian is composing the marker
// point for the GPU. locked may be nil (no lock check, e.g. at initial load).
func portWorldPosLocked(g nodeGeom, portName string, isInput bool, nodeID string,
	locked func(nodeID, portName string, isInput bool) bool) vec3 {
	center := nodeWorldPos(g)
	if portName == "" {
		return center
	}
	dir, ok := portDir(g, portName, isInput)
	if !ok {
		return center
	}
	if locked != nil && locked(nodeID, portName, isInput) {
		ringDir := ringProjectDir(dir, dir)
		return center.add(ringDir.scale(nodeRadius(g.Kind)))
	}
	return center.add(dir.scale(portRadiusByName(g, portName, isInput)))
}
