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

// measureScalars is the plain-polar INVERSE measurement: given each node's current world
// center (centers) and its OWNED reference (references[id], "" for a root), derive the
// integer scalar triple (iTheta, iPhi, iR) that is the node's LOCAL POLAR coordinate about
// its reference as the origin (or sceneCenter for a root) — the model this file implements
// (see the package-level Model doc in node_move.go / CLAUDE.md). No forward-kinematics frame
// is involved: this is a PLAIN polar measurement, origin = the reference's own current world
// center (or sceneCenter for a root), no rotation/orientation carried between nodes.
//
// Nodes missing a center, or whose reference is missing a center, are omitted (nothing to
// measure against).
func measureScalars(centers map[string]vec3, references map[string]string, sceneCenter vec3) map[string]quantizedOffset {
	result := make(map[string]quantizedOffset, len(references))
	for id, ref := range references {
		origin := sceneCenter
		if ref != "" {
			c, ok := centers[ref]
			if !ok {
				continue
			}
			origin = c
		}
		pos, ok := centers[id]
		if !ok {
			continue
		}
		p := cart2polar(pos.sub(origin))
		result[id] = quantizedOffset{
			iTheta: int(math.Round(p.Theta / stepTheta)),
			iPhi:   int(math.Round(p.Phi / stepPhi)),
			iR:     int(math.Round(p.R / stepR)),
			parent: ref,
		}
	}
	return result
}

// deriveCenters is the plain-polar FORWARD computation: given each node's scalar triple
// (scalars, from measureScalars or loaded meta.json quantI*) and its OWNED reference
// (references), compute every reachable node's world center. References are resolved
// BEFORE dependents (BFS from the roots — ref == ""), so a node's reference is always
// already derived when the node itself is visited:
//
//	origin      = sceneCenter, or derived[ref] if ref != ""
//	derived[id] = origin + polar2cart({R: iR*stepR, Theta: iTheta*stepTheta, Phi: iPhi*stepPhi})
//
// Nodes unreachable from any root (a dangling or cyclic reference) are left unset — the
// walk terminates via a visited guard rather than looping forever on a cycle.
func deriveCenters(scalars map[string]quantizedOffset, references map[string]string, sceneCenter vec3) map[string]vec3 {
	children := map[string][]string{}
	roots := []string{}
	for id, ref := range references {
		if ref == "" {
			roots = append(roots, id)
		} else {
			children[ref] = append(children[ref], id)
		}
	}
	sort.Strings(roots)

	derived := map[string]vec3{}
	visited := map[string]bool{}

	var visit func(id string, origin vec3)
	visit = func(id string, origin vec3) {
		if visited[id] {
			return
		}
		visited[id] = true
		o, ok := scalars[id]
		if !ok {
			return
		}
		pos := origin.add(polar2cart(polar{
			R:     float64(o.iR) * stepR,
			Theta: float64(o.iTheta) * stepTheta,
			Phi:   float64(o.iPhi) * stepPhi,
		}))
		derived[id] = pos
		kids := append([]string(nil), children[id]...)
		sort.Strings(kids)
		for _, childID := range kids {
			visit(childID, pos)
		}
	}
	for _, id := range roots {
		visit(id, sceneCenter)
	}
	return derived
}
