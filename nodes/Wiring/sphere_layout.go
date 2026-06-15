// sphere_layout.go — graph-level node-position computation for the sphere-chain
// layout (B3 of sphere-chain layout). ADDITIVE + GATED.
//
// MODEL (MODEL.md / sphere-chain spec): each node is a sphere of radius R (nodeR,
// port_geometry.go). The anchor sits at the origin. Every other node M is placed
// at `parent_center + R_parent * Dir_M`, where Dir_M is M's stored unit direction
// on its parent's sphere (specNode.Dir) and R_parent = the placing parent node's R
// (nodeR(parent)). Positions PROPAGATE outward from the anchor by BFS over the
// CONNECTION graph (edges, undirected for placement): the first already-placed node
// to reach M becomes M's parent. Back/cross edges to already-placed nodes do not
// re-place them, so cycles terminate.
//
// This is a WHOLE-GRAPH computation: a single node cannot know its own world
// center without its parent's center, which is why placement lives here (graph
// level) rather than in nodeWorldPos (per-node). The caller injects the resulting
// centers back onto each nodeGeom.Center; nodeWorldPos then returns that override
// and the lattice path is bypassed for migrated nodes only.

package Wiring

// sphereEdge is the minimal undirected connection used for placement propagation:
// the two endpoint node IDs. Direction is irrelevant for WHERE a node sits.
type sphereEdge struct {
	Source string
	Target string
}

// defaultDir is the fallback unit direction used to place a reached node whose
// stored Dir is nil. C1 will populate Dir for every migrated node, so a nil Dir is
// an edge case; +Y keeps the node on the parent's sphere surface deterministically.
var defaultDir = vec3{X: 0, Y: 1, Z: 0}

// computeSphereChainPositions resolves world centers for every node by sphere-chain
// propagation from an anchor.
//
//   - GATE: sphere-chain mode is active only if at least one node has R set. If NO
//     node carries R, returns nil so the caller keeps the lattice path.
//   - ANCHOR: node "1" if present, else the first node in nodes (map iteration is
//     non-deterministic, so a stable anchor matters; "1" is the conventional seed).
//     The anchor sits at the origin (0,0,0).
//   - BFS over the undirected connection graph (adjacency built from edges' source
//     and target, both directions). For each node M reached from already-placed
//     parent P: pos[M] = pos[P] + nodeR(P) * Dir_M (M's stored unit dir; nil → +Y).
//   - CYCLES: a node is placed at most once (visited set); back/cross edges to an
//     already-placed node are ignored, so the BFS terminates on any cycle.
//   - DISCONNECTED: nodes never reached from the anchor are left OUT of the map;
//     the caller leaves their nodeGeom.Center nil, so they keep their lattice pos.
//
// Returns a map from node ID → world center. Empty/nil map ⇒ lattice path.
func computeSphereChainPositions(nodes map[string]nodeGeom, edges []sphereEdge) map[string]vec3 {
	// GATE: require at least one node with an explicit R for sphere-chain mode.
	active := false
	for _, g := range nodes {
		if g.R != nil {
			active = true
			break
		}
	}
	if !active {
		return nil
	}

	// Pick a stable anchor: "1" if present, else any node (first by deterministic
	// scan is impossible over a map; "1" is the documented seed and topologies use
	// it, so the else-branch is only a degenerate fallback).
	anchor := ""
	if _, ok := nodes["1"]; ok {
		anchor = "1"
	} else {
		for id := range nodes {
			anchor = id
			break
		}
	}
	if anchor == "" {
		return map[string]vec3{}
	}

	// Build undirected adjacency from the edge list.
	adj := map[string][]string{}
	for _, e := range edges {
		if e.Source == "" || e.Target == "" {
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
		adj[e.Target] = append(adj[e.Target], e.Source)
	}

	pos := map[string]vec3{anchor: {X: 0, Y: 0, Z: 0}}
	queue := []string{anchor}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		pCenter := pos[p]
		pR := nodeR(nodes[p])
		for _, m := range adj[p] {
			if _, placed := pos[m]; placed {
				continue // back/cross edge — already placed, do not re-place (cycle-safe)
			}
			pos[m] = pCenter.add(dirOf(nodes[m]).scale(pR))
			queue = append(queue, m)
		}
	}
	return pos
}

// dirOf returns the node's stored unit direction on its parent's sphere, or the
// +Y fallback when Dir is nil (C1 will populate Dir; nil is the pre-migration case).
func dirOf(g nodeGeom) vec3 {
	if g.Dir != nil {
		return vec3{X: g.Dir[0], Y: g.Dir[1], Z: g.Dir[2]}
	}
	return defaultDir
}
