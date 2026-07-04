// aimed_ports.go — dynamic port auto-aim registry.
//
// Certain port directions should not be static (ring-anchor-based) but should
// dynamically point toward their connected node's current center. This file
// defines the registry and the portDirAimed helper that falls back to portDir
// for non-registered ports.
//
// Scope: keyed by (nodeID, portName, isInput) — every edge-connected port is
// registered, derived from the loaded edge list. Both endpoint nodes must have
// geometry (centers) for a registration to be created.

package Wiring

// AimedPortKey identifies one port on one node.
type AimedPortKey struct {
	NodeID   string
	PortName string
	IsInput  bool
}

// AimedPortRegistry maps a port to the node id whose current center the port
// should aim toward. nil registry (or a missing key) falls back to portDir.
type AimedPortRegistry map[AimedPortKey]string

// portDirAimed returns the direction from this node's center toward the
// registered target node's current center (dynamic aim), or falls back to
// portDir when the port is not registered or the target center is unavailable.
// centerOf returns the current world center for a node id, and a boolean
// indicating whether the node is known.
func portDirAimed(
	g nodeGeom,
	portName string,
	isInput bool,
	nodeID string,
	registry AimedPortRegistry,
	centerOf func(string) (vec3, bool),
) (vec3, bool) {
	if registry != nil && centerOf != nil {
		key := AimedPortKey{nodeID, portName, isInput}
		if targetID, ok := registry[key]; ok {
			if targetCenter, ok2 := centerOf(targetID); ok2 {
				diff := targetCenter.sub(nodeWorldPos(g))
				if l := diff.length(); l > 0 {
					return diff.scale(1 / l), true
				}
			}
		}
	}
	return portDir(g, portName, isInput)
}

// segmentBetweenPortsAimed returns the wireSegment for an edge, using aimed
// port directions for registered ports. Non-registered ports fall back to
// portWorldPos. When registry is nil this is identical to segmentBetweenPorts.
// locked, when non-nil, reports whether (nodeID, portName, isInput) carries an
// ACTIVE `port ∈ torus` lock (MoveDispatch.portTorusLocked) — a locked port's
// position is ring-projected (see portWorldPosAimed) so the streamed marker and
// this edge's endpoint stay coincident.
func segmentBetweenPortsAimed(
	src nodeGeom, srcHandle, srcID string,
	tgt nodeGeom, tgtHandle, tgtID string,
	registry AimedPortRegistry,
	centerOf func(string) (vec3, bool),
	locked func(nodeID, portName string, isInput bool) bool,
) wireSegment {
	start := portWorldPosAimed(src, srcHandle, false, srcID, registry, centerOf, locked)
	end := portWorldPosAimed(tgt, tgtHandle, true, tgtID, registry, centerOf, locked)
	return wireSegment{Start: start, End: end}
}

// ringProjectDir projects dir onto the world X-Y plane (dropping Z) and
// normalizes it — the direction from a node's center to the point on its
// border ring (drawn with identity rotation, in the world z=0 plane through
// the center) nearest dir. When dir is (near-)parallel to world +Z (its X-Y
// projection is ~zero), falls back to projecting fallbackDir the same way;
// when even that is degenerate, falls back to world +X so the result is
// always a well-defined unit vector in the ring plane.
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

// portWorldPosAimed returns the sphere-surface point in the aimed direction,
// or the node center when the port is unnamed/unknown. When locked reports an
// ACTIVE `port ∈ torus` lock for this exact port, the aimed direction is
// ring-projected (ringProjectDir) and the radius is nodeRadius(g.Kind) — the
// node-border ring's major radius — so the port sits ON the ring instead of
// merely aiming toward the connected node. locked may be nil (no lock check,
// e.g. at initial load before any lock exists).
func portWorldPosAimed(
	g nodeGeom,
	portName string,
	isInput bool,
	nodeID string,
	registry AimedPortRegistry,
	centerOf func(string) (vec3, bool),
	locked func(nodeID, portName string, isInput bool) bool,
) vec3 {
	center := nodeWorldPos(g)
	if portName == "" {
		return center
	}
	dir, ok := portDirAimed(g, portName, isInput, nodeID, registry, centerOf)
	if !ok {
		return center
	}
	if locked != nil && locked(nodeID, portName, isInput) {
		fallbackDir, _ := portDir(g, portName, isInput)
		ringDir := ringProjectDir(dir, fallbackDir)
		return center.add(ringDir.scale(nodeRadius(g.Kind)))
	}
	return center.add(dir.scale(portRadiusByName(g, portName, isInput)))
}
