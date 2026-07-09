package Wiring

import "math"

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
	stepR     = defaultNodeR
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
	nodes := map[string]bool{}
	for _, e := range edgeEndpoints {
		nodes[e.Source] = true
		nodes[e.Target] = true
	}
	for id := range nodes {
		best := ""
		for _, e := range edgeEndpoints {
			if e.Target != id {
				continue
			}
			if e.Source == id {
				continue // ignore self-loop
			}
			if best == "" || e.Source < best {
				best = e.Source
			}
		}
		parent[id] = best
	}
	roots = map[string]bool{}
	for id, p := range parent {
		if p == "" {
			roots[id] = true
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
		visit(id, quantizedNodeLayout{center: anchor, forward: rootForward})
	}
	return result
}

// cart converts a unit direction to a Cartesian unit vector (R=1), matching polar2cart's
// pole convention (pole = +y).
func cart(d dir) vec3 {
	return polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi})
}
