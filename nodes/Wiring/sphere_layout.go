// sphere_layout.go — graph-level node-position computation for the sphere-chain
// layout (B3 of sphere-chain layout). ADDITIVE + GATED.
//
// MODEL (MODEL.md / sphere-chain spec): each node is a sphere of radius R (nodeR,
// port_geometry.go). The anchor sits at the origin. Every other node M is placed
// at `parent_center + R_parent * Dir_M`, where Dir_M is M's stored unit direction
// on its parent's sphere (specNode.Dir) and R_parent = the placing parent node's R
// (nodeR(parent)). Positions PROPAGATE outward from the anchor by BFS over the
// DIRECTED connection graph (an edge S->T means S outputs to T, so T sits on S's
// sphere — T is a CHILD of S). M's parent is the node that OUTPUTS to it; it is
// never an undirected neighbor. Back/cross edges to already-placed nodes do not
// re-place them, so cycles terminate.
//
// This is a WHOLE-GRAPH computation: a single node cannot know its own world
// center without its parent's center, which is why placement lives here (graph
// level) rather than in nodeWorldPos (per-node). The caller injects the resulting
// centers back onto each nodeGeom.Center; nodeWorldPos then returns that override
// and the lattice path is bypassed for migrated nodes only.

package Wiring

import (
	"math"
	"sort"
)

// sphereEdge is a DIRECTED connection used for placement propagation: Source
// outputs to Target, so Target sits on Source's sphere surface. Direction matters:
// the center node (Source) carries the sphere; the surface node (Target) is placed
// on it. This is why a node's "parent" is the node that OUTPUTS to it, never an
// undirected neighbor — node 4 (7->4) is on node 7's sphere; the 4->5 edge puts
// node 5 on node 4's sphere and has nothing to do with where node 4 sits.
type sphereEdge struct {
	Source string
	Target string
}

// buildDirectedChildren builds the placement adjacency: for each directed edge
// Source->Target, Target is a CHILD of Source (Target sits on Source's sphere). The
// child lists are sorted by id so propagation is deterministic regardless of the
// caller's edge order (callers build the edge slice by ranging a Go map, whose
// order is randomized — undirected + unsorted is what made node 4's parent flip
// between 7 and 5 and flicker). A node reached by two incoming edges (e.g. node 5:
// 4->5 and 6->5) is placed by its first parent in sorted order.
func buildDirectedChildren(edges []sphereEdge) map[string][]string {
	children := map[string][]string{}
	for _, e := range edges {
		if e.Source == "" || e.Target == "" {
			continue
		}
		children[e.Source] = append(children[e.Source], e.Target)
	}
	for k := range children {
		sort.Strings(children[k])
	}
	return children
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
//   - BFS over the DIRECTED connection graph (children built from edges' source ->
//     target only; T sits on S's sphere). For each child M reached from already-placed
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

	// Pick a stable anchor: "1" if present, else any node ("1" is the documented seed
	// and topologies use it; the else-branch is only a degenerate fallback).
	anchor := sphereChainAnchor(nodes)
	if anchor == "" {
		return map[string]vec3{}
	}

	// Directed placement adjacency: a node sits on the sphere of the node that
	// OUTPUTS to it. Propagate outward from the anchor following output edges.
	children := buildDirectedChildren(edges)

	pos := map[string]vec3{anchor: {X: 0, Y: 0, Z: 0}}
	queue := []string{anchor}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		pCenter := pos[p]
		pR := nodeR(nodes[p])
		for _, m := range children[p] {
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

// sphereChainAnchor picks the stable anchor id used as the root of sphere-chain
// propagation: "1" if present, else any node (degenerate fallback). Empty string
// when there are no nodes.
func sphereChainAnchor(nodes map[string]nodeGeom) string {
	if _, ok := nodes["1"]; ok {
		return "1"
	}
	for id := range nodes {
		return id
	}
	return ""
}

// sphereChainParents returns the BFS parent of every reached node (the same
// already-placed node that PLACES it in computeSphereChainPositions), keyed by node
// id. The anchor has no parent (absent from the map). DIRECTED adjacency (parent =
// the node that outputs to the child); first reacher wins (cycle-safe), matching
// the position propagation.
//
// This is the inverse lookup E1 needs: a drag re-aims a node's Dir on its PARENT's
// sphere, so the move handler must know which node is the parent. Recomputed from the
// same inputs as the position pass so the two stay in lock-step.
func sphereChainParents(nodes map[string]nodeGeom, edges []sphereEdge) map[string]string {
	anchor := sphereChainAnchor(nodes)
	if anchor == "" {
		return map[string]string{}
	}
	children := buildDirectedChildren(edges)
	parent := map[string]string{}
	visited := map[string]bool{anchor: true}
	queue := []string{anchor}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, m := range children[p] {
			if visited[m] {
				continue
			}
			visited[m] = true
			parent[m] = p // m sits on p's sphere because p outputs to m
			queue = append(queue, m)
		}
	}
	return parent
}

// quantizeDirToStep snaps newDir onto the sphere by rounding its angular displacement
// from oldDir to the nearest whole multiple of step radians, then rotating oldDir by
// that quantized angle toward newDir. step is the node's diameter step angle on the
// parent's sphere (diameterStepAngle); a smaller node gets finer steps. Both inputs
// are treated as unit directions; the result is unit length.
//
//   - step <= 0 (degenerate R or diameter): return newDir normalized (no quantization).
//   - oldDir and newDir (anti-)parallel: the rotation axis is undefined; return the
//     quantized rotation about an arbitrary perpendicular axis, or oldDir when the
//     rounded step count is 0 (the move stayed within half a step).
func quantizeDirToStep(oldDir, newDir vec3, step float64) vec3 {
	od := oldDir.normalize()
	nd := newDir.normalize()
	if step <= 0 {
		return nd
	}
	dot := od.X*nd.X + od.Y*nd.Y + od.Z*nd.Z
	if dot > 1 {
		dot = 1
	} else if dot < -1 {
		dot = -1
	}
	angle := math.Acos(dot) // 0..pi between old and new
	steps := math.Round(angle / step)
	if steps == 0 {
		return od // stayed within half a diameter step → no move
	}
	q := steps * step
	if q > math.Pi {
		q = math.Pi
	}
	// Rotation axis = od × nd (perpendicular to both). Degenerate when (anti-)parallel.
	axis := vec3{
		X: od.Y*nd.Z - od.Z*nd.Y,
		Y: od.Z*nd.X - od.X*nd.Z,
		Z: od.X*nd.Y - od.Y*nd.X,
	}
	al := axis.length()
	if al == 0 {
		// (Anti-)parallel: pick an arbitrary axis perpendicular to od.
		axis = vec3{X: 1, Y: 0, Z: 0}
		if math.Abs(od.X) > 0.9 {
			axis = vec3{X: 0, Y: 1, Z: 0}
		}
		// Re-orthogonalize against od.
		d := axis.X*od.X + axis.Y*od.Y + axis.Z*od.Z
		axis = vec3{X: axis.X - d*od.X, Y: axis.Y - d*od.Y, Z: axis.Z - d*od.Z}
		al = axis.length()
		if al == 0 {
			return od
		}
	}
	axis = vec3{X: axis.X / al, Y: axis.Y / al, Z: axis.Z / al}
	// Rodrigues' rotation of od about axis by angle q.
	cosA := math.Cos(q)
	sinA := math.Sin(q)
	cross := vec3{
		X: axis.Y*od.Z - axis.Z*od.Y,
		Y: axis.Z*od.X - axis.X*od.Z,
		Z: axis.X*od.Y - axis.Y*od.X,
	}
	axDotOd := axis.X*od.X + axis.Y*od.Y + axis.Z*od.Z
	res := vec3{
		X: od.X*cosA + cross.X*sinA + axis.X*axDotOd*(1-cosA),
		Y: od.Y*cosA + cross.Y*sinA + axis.Y*axDotOd*(1-cosA),
		Z: od.Z*cosA + cross.Z*sinA + axis.Z*axDotOd*(1-cosA),
	}
	return res.normalize()
}
