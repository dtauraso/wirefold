// aimed_ports.go — dynamic port auto-aim registry.
//
// Certain port directions should not be static (ring-anchor-based) but should
// dynamically point toward their connected node's current center. This file
// defines the registry and the portDirAimed helper that falls back to portDir
// for non-registered ports.
//
// Scope: keyed by (nodeID, portName, isInput) — only the 4 ports on the
// 1↔2 and 1↔6 edges are registered, and only when nodes 1+2+6 all exist
// (same guard as the theta-lock).

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
func segmentBetweenPortsAimed(
	src nodeGeom, srcHandle, srcID string,
	tgt nodeGeom, tgtHandle, tgtID string,
	registry AimedPortRegistry,
	centerOf func(string) (vec3, bool),
) wireSegment {
	start := portWorldPosAimed(src, srcHandle, false, srcID, registry, centerOf)
	end := portWorldPosAimed(tgt, tgtHandle, true, tgtID, registry, centerOf)
	return wireSegment{Start: start, End: end}
}

// portWorldPosAimed returns the sphere-surface point in the aimed direction,
// or the node center when the port is unnamed/unknown.
func portWorldPosAimed(
	g nodeGeom,
	portName string,
	isInput bool,
	nodeID string,
	registry AimedPortRegistry,
	centerOf func(string) (vec3, bool),
) vec3 {
	center := nodeWorldPos(g)
	if portName == "" {
		return center
	}
	dir, ok := portDirAimed(g, portName, isInput, nodeID, registry, centerOf)
	if !ok {
		return center
	}
	return center.add(dir.scale(nodeRadius(g.Kind)))
}
