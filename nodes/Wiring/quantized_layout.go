package Wiring

import (
	"math"
	"sort"
)

// quantized_layout.go — PHASE 1 of the quantized hierarchical polar layout
// (replaces the polar-lock system; see docs/planning for the full plan). This file is
// ADDITIVE and GUARDED OFF: nothing in the live render/move/persist path reads its
// output yet. It only computes a candidate world-position map on demand (for tests),
// using the rotational-nesting (forward-kinematics) model:
//
//   - Every node is a polar frame. A node's outgoing neighbors are placed relative to
//     it by a QUANTIZED polar offset — three integers (iTheta, iPhi, iR) — using three
//     GLOBAL step constants (same for every node in the graph).
//   - A child's (θ,φ) offset is measured in a frame whose POLE is the parent's forward
//     direction (the direction from the parent's parent to the parent). This is
//     rotational nesting: rotating a node's iTheta/iPhi bends its own forward direction,
//     which in turn re-aims every descendant hanging off it (spherical.go fromAxisFrame
//     composes this without ever going through Cartesian rotation matrices).
//   - Roots (nodes with no incoming edge) are anchored at the scene sphere center with a
//     fixed default forward direction (+x ref: Theta=π/2, Phi=0).
//
// Parent-per-node is a spanning tree over the directed edge graph: each node's parent is
// the LOWEST-ID source among its incoming edges; roots are nodes with no incoming edge.
// Cycles (a node reachable back to an ancestor) are detected and skipped so the walk
// terminates — quantizedOffset entries live only on nodes actually visited.

// Global quantization step constants — same for every node/edge in the graph. Offsets
// are always integer multiples of these: offset = (iTheta*stepTheta, iPhi*stepPhi,
// iR*stepR).
const (
	stepTheta = math.Pi / 12
	stepPhi   = math.Pi / 12
	// stepR must be smaller than the typical node-to-parent spacing, else every node
	// rounds to iR=0 and collapses onto its parent. Node spacing here is ~80 units, so
	// defaultNodeR (200) collapsed the graph; 20 keeps nodes distinct and makes a drag
	// cross an r-cell responsively. Tunable.
	stepR = 20.0
)

// rootForward is the fixed default forward direction assigned to every root node (the
// +x reference direction in the pole-frame convention of spherical.go/polar.go).
var rootForward = dir{Theta: math.Pi / 2, Phi: 0}

// quantizedOffset is the per-node quantized polar offset from its parent, plus the
// resolved parent id ("" for a root). iTheta/iPhi/iR default to zero (straight
// continuation / no offset) until authored.
type quantizedOffset struct {
	iTheta int
	iPhi   int
	iR     int
	parent string
}

// quantizedNodeLayout holds one node's composed result: its world center and its
// forward direction (the pole other nodes' children are measured against).
type quantizedNodeLayout struct {
	center  vec3
	forward dir
}

// buildSpanningTree walks the directed edge graph and assigns each node a single
// parent: the LOWEST-ID source among its incoming edges. Nodes with no incoming edge
// are roots (one per weakly-connected component, in general; ties within a component
// are resolved by lowest-id-source too). Returns the parent map (node id -> parent id,
// "" for roots) and the set of root ids. Cycles are detected during the later
// topological walk (composeQuantizedLayout), not here — this function only computes
// the local "lowest-id incoming source" relation, which is always well-defined even in
// the presence of a cycle.
func buildSpanningTree(edgeEndpoints map[string]EdgeEndpoints) (parent map[string]string, roots map[string]bool) {
	parent = map[string]string{}
	roots = map[string]bool{}

	// Undirected adjacency (edges are traversed both ways so a spanning tree covers
	// every node even in a fully bidirectional graph); ignore self-loops.
	adj := map[string]map[string]bool{}
	nodes := map[string]bool{}
	touch := func(id string) {
		nodes[id] = true
		if adj[id] == nil {
			adj[id] = map[string]bool{}
		}
	}
	for _, e := range edgeEndpoints {
		touch(e.Source)
		touch(e.Target)
		if e.Source == e.Target {
			continue
		}
		adj[e.Source][e.Target] = true
		adj[e.Target][e.Source] = true
	}

	// Deterministic node order so root selection and tree shape are stable.
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// One BFS per weakly-connected component. The first unvisited id in sorted order is
	// the lowest id in a NEW component (BFS covers a whole component before we advance),
	// so it is a deterministic root — this works even when EVERY node has an incoming
	// edge (no zero-in-degree node exists). BFS assigns each node its discovery
	// predecessor as parent, producing an acyclic spanning tree covering all nodes.
	visited := map[string]bool{}
	for _, start := range ids {
		if visited[start] {
			continue
		}
		roots[start] = true
		parent[start] = ""
		visited[start] = true
		queue := []string{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			nbrs := make([]string, 0, len(adj[cur]))
			for n := range adj[cur] {
				nbrs = append(nbrs, n)
			}
			sort.Strings(nbrs)
			for _, n := range nbrs {
				if !visited[n] {
					visited[n] = true
					parent[n] = cur
					queue = append(queue, n)
				}
			}
		}
	}
	return parent, roots
}

// quantizedOffsetsFromParents seeds a quantizedOffset map (parent field only, angular/
// radial fields left at their zero default) from a parent map produced by
// buildSpanningTree — the shape MoveDispatch.quantizedOffsets is meant to hold.
func quantizedOffsetsFromParents(parent map[string]string) map[string]quantizedOffset {
	offsets := make(map[string]quantizedOffset, len(parent))
	for id, p := range parent {
		offsets[id] = quantizedOffset{parent: p}
	}
	return offsets
}

// ComposeQuantizedLayout composes md.quantizedOffsets (if md.quantizedLayout is armed)
// into world centers, anchored at md.sceneSphere.Center. It is PHASE 1 scaffolding: no
// live caller invokes this yet (guarded off by quantizedLayout defaulting to false); it
// exists so tests can exercise the MoveDispatch-owned fields end to end.
func (md *MoveDispatch) ComposeQuantizedLayout() map[string]quantizedNodeLayout {
	if !md.quantizedLayout {
		return nil
	}
	parent := map[string]string{}
	roots := map[string]bool{}
	for id, off := range md.quantizedOffsets {
		parent[id] = off.parent
		if off.parent == "" {
			roots[id] = true
		}
	}
	return composeQuantizedLayout(parent, roots, md.quantizedOffsets, md.sceneSphere.Center)
}

// composeQuantizedLayout walks the spanning tree (buildSpanningTree) from each root,
// applying the forward-kinematics rule at every step:
//
//	childDir    = fromAxisFrame(parentForward, iTheta*stepTheta, iPhi*stepPhi)
//	childForward = childDir
//	childPos    = parentPos + (iR*stepR) * cart(childDir)
//
// offsets supplies each node's quantized (iTheta,iPhi,iR); a node absent from offsets
// (or with a zero-value entry) continues straight (iTheta=iPhi=0) with no radial step
// (iR=0, i.e. it coincides with its parent) — callers that want a node to actually
// move away from its parent must set iR explicitly. anchor is the fixed root position
// (md.sceneSphere.Center in production). Nodes unreachable from any root (isolated —
// no edges) are omitted from the result. Cycles are detected (a node whose parent chain
// revisits itself) and skipped rather than infinite-looping.
func composeQuantizedLayout(
	parent map[string]string,
	roots map[string]bool,
	offsets map[string]quantizedOffset,
	anchor vec3,
) map[string]quantizedNodeLayout {
	anchors := map[string]vec3{}
	for id := range roots {
		anchors[id] = anchor
	}
	return composeQuantizedLayoutAnchored(parent, roots, offsets, anchors)
}

// composeQuantizedLayoutAnchored is the PHASE 3 generalization of composeQuantizedLayout:
// each root gets its OWN anchor (anchors[id]) instead of one anchor shared by every root.
// This is what lets a multi-component graph's roots each keep their EXISTING world center
// (RootMove on a root just moves its own anchor) while composeQuantizedLayout itself stays
// the single-anchor convenience wrapper the Phase 1/2 tests already exercise.
func composeQuantizedLayoutAnchored(
	parent map[string]string,
	roots map[string]bool,
	offsets map[string]quantizedOffset,
	anchors map[string]vec3,
) map[string]quantizedNodeLayout {
	result := map[string]quantizedNodeLayout{}
	// children: parent id -> its direct children ids, for a top-down BFS/DFS walk.
	children := map[string][]string{}
	for id, p := range parent {
		if p == "" {
			continue
		}
		children[p] = append(children[p], id)
	}

	visiting := map[string]bool{}

	var visit func(id string, layout quantizedNodeLayout)
	visit = func(id string, layout quantizedNodeLayout) {
		if visiting[id] {
			return // cycle guard: never revisit a node already on the current path
		}
		if _, done := result[id]; done {
			return
		}
		visiting[id] = true
		result[id] = layout
		for _, childID := range children[id] {
			off := offsets[childID]
			childDir := fromAxisFrame(layout.forward, float64(off.iTheta)*stepTheta, float64(off.iPhi)*stepPhi)
			childPos := layout.center.add(cart(childDir).scale(float64(off.iR) * stepR))
			visit(childID, quantizedNodeLayout{center: childPos, forward: childDir})
		}
		visiting[id] = false
	}

	for id := range roots {
		visit(id, quantizedNodeLayout{center: anchors[id], forward: rootForward})
	}
	return result
}

// cart converts a unit direction to a Cartesian unit vector (R=1), matching polar2cart's
// pole convention (pole = +y).
func cart(d dir) vec3 {
	return polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi})
}

// snapQuantizedOffsets is PHASE 2: the INVERSE of composeQuantizedLayout — given each
// node's current world center (heldCenters()) and the edge graph, derive the quantized
// offset (iTheta, iPhi, iR, parent) that would reproduce that layout under
// composeQuantizedLayout. It walks the same spanning tree (buildSpanningTree) top-down,
// so a parent's SNAPPED forward (not its raw/unsnapped world forward) is always resolved
// before its children are visited — this keeps compose∘snap self-consistent: composing
// the offsets this function returns reproduces each child's direction using the exact
// snapped angles, not the pre-snap continuous ones.
//
// Roots get offset {0,0,0,""} — buildSpanningTree's convention is that a root's own
// current center becomes the anchor (matching ComposeQuantizedLayout's md.sceneSphere.Center
// role for callers that then compose from that same center) and rootForward is its fixed
// forward, exactly as composeQuantizedLayout assigns on the way out.
//
// For a child C of processed parent P:
//
//	childDirWorld = cart2polar(centers[C] - centers[P])   // the one cartesian boundary
//	(c, psi)      = azimuthFrom(P.snappedForward, childDirWorld)
//	iTheta        = round(c / stepTheta)
//	iPhi          = round(psi / stepPhi)
//	iR            = round(|centers[C]-centers[P]| / stepR)
//	C.snappedForward = fromAxisFrame(P.snappedForward, iTheta*stepTheta, iPhi*stepPhi)
//
// Nodes missing a center, or unreachable from a root whose center is known, are omitted
// (nothing to snap against). Cycles are detected and skipped, mirroring
// composeQuantizedLayout's visiting-guard.
// snapQuantizedOffsets measures each node's quantized triple relative to its REFERENCE
// (parent[id]) from the given centers. parent is the OWNED reference map (peer-to-peer:
// each node holds its reference), seeded once from the spanning tree and thereafter passed
// in — this no longer recomputes buildSpanningTree. parent[id] == "" marks a root.
func snapQuantizedOffsets(centers map[string]vec3, parent map[string]string) map[string]quantizedOffset {
	children := map[string][]string{}
	roots := map[string]bool{}
	for id, p := range parent {
		if p == "" {
			roots[id] = true
			continue
		}
		children[p] = append(children[p], id)
	}

	result := map[string]quantizedOffset{}
	forwardOf := map[string]dir{}
	visiting := map[string]bool{}

	var visit func(id string, forward dir, offset quantizedOffset)
	visit = func(id string, forward dir, offset quantizedOffset) {
		if visiting[id] {
			return // cycle guard: never revisit a node already on the current path
		}
		if _, done := result[id]; done {
			return
		}
		if _, known := centers[id]; !known {
			return // nothing to snap against
		}
		visiting[id] = true
		forwardOf[id] = forward
		result[id] = offset
		for _, childID := range children[id] {
			childCenter, ok := centers[childID]
			if !ok {
				continue
			}
			delta := childCenter.sub(centers[id])
			r := delta.length()
			childDirWorld := dir{}
			if r > 0 {
				p := cart2polar(delta)
				childDirWorld = dir{Theta: p.Theta, Phi: p.Phi}
			}
			c, psi := azimuthFrom(forward, childDirWorld)
			iTheta := int(math.Round(c / stepTheta))
			iPhi := int(math.Round(psi / stepPhi))
			iR := int(math.Round(r / stepR))
			snappedForward := fromAxisFrame(forward, float64(iTheta)*stepTheta, float64(iPhi)*stepPhi)
			visit(childID, snappedForward, quantizedOffset{iTheta: iTheta, iPhi: iPhi, iR: iR, parent: id})
		}
		visiting[id] = false
	}

	for id := range roots {
		if _, known := centers[id]; known {
			visit(id, rootForward, quantizedOffset{parent: ""})
		}
	}
	return result
}

// SnapQuantizedOffsets fills md.quantizedOffsets from the movers' CURRENT held centers
// (heldCenters()) and the current edge graph (heldEdges()). It is PHASE 2 scaffolding,
// callable from tests only for now: nothing in the live move/render/persist path calls
// this yet (md.quantizedLayout still gates ComposeQuantizedLayout, and nothing sets it).
