// port_geometry.go — Go mirror of the port-to-port segment geometry in
// tools/topology-vscode/src/webview/three/geometry-helpers.ts.
//
// Go must compute a pulse's travel budget from the SAME segment the
// bead is drawn on: a straight line from the source OUTPUT port's sphere-surface
// point to the target INPUT port's sphere-surface point. This file reproduces
// nodeWorldPos, nodeRadius, portDir and portWorldPos so arcLengthBetweenPorts
// (loader.go / stdin_reader.go) returns the chord length.
//
// Inputs the geometry needs, per node:
//   - kind        → width/height via kindDims (generated from SPEC.md View)
//   - center      → world center (from meta.json x/y/z or origin fallback)
//   - port lists  → inputs/outputs with optional side + slot (from the spec node;
//                   falls back to registry ports with default sides when absent)
//
// Every magic number is pulled from CurveParam* constants in curve_params.go.

package Wiring

import "math"

// portGeom is one port's layout descriptor: its name and optional ring-anchor index.
type portGeom struct {
	Name     string
	AnchorId *int     // optional index into the flat ring-anchor array; nil → ring slot 0
	PortR    *float64 // optional per-port radius (distance from node center); nil → nodeRadius(kind) fallback (see portRadiusByName)
}

// nodeIdentity is the WRITE-ONCE-AT-CONSTRUCTION part of a node's geometry: set by the
// loader (loader.go) when the nodeGeom is built and never written again by any handler
// (applyCenter, setPortAnchorId, emitGeometry — grepped clean of writes to these fields).
// It is split out from nodeGeom specifically so a reader that only wants IDENTITY (e.g.
// MoveDispatch.NodeKind, called from the gesture/stdin-reader goroutine, NOT the mover's
// own goroutine) can read it with NO LOCK: the memory it touches is never in a writer's
// footprint, by construction of the type, not by coincidence of which fields a particular
// access happens to touch. See node_mover.go's geomMu doc-comment history for why a
// "the two byte ranges just don't happen to overlap today" argument is the bug class this
// split exists to make unrepresentable (memory/feedback_make_bug_class_unrepresentable.md).
type nodeIdentity struct {
	Kind  string
	Label string   // human label for this node (data.label, else node id); streamed on node-geometry events for the new-system label sidecar
	R     *float64 // optional per-node sphere radius for this node's edges; nil → defaultNodeR (see nodeR)
	// SceneCenter is the scene sphere's center — the ONLY cartesian value carried here.
	// Set once at construction (loader.go) alongside the rest of identity; never
	// reassigned afterward (grepped clean — no `.SceneCenter =` outside the literal).
	SceneCenter vec3
}

// nodeGeom carries everything the port-curve math needs for one node: the write-once
// nodeIdentity (embedded, so its fields read/promote as g.Kind/g.Label/g.R/g.SceneCenter)
// plus the MUTABLE per-node state that applyCenter/setPortAnchorId update on every move.
//
// Position is POLAR (polar-frame-rewrite.md): ScenePolar (r,θ,φ) about SceneCenter is the
// source of truth; the node's world center is DERIVED only at the display/geometry boundary
// as SceneCenter + polar2cart(ScenePolar) (nodeWorldPos). HasPos is false for
// hand-written/partial specs that carry no position (nodeWorldPos then falls back to the
// world origin).
type nodeGeom struct {
	nodeIdentity
	// ScenePolar is the node's position as (r,θ,φ) about SceneCenter — the polar source of
	// truth. World is derived: SceneCenter + polar2cart(ScenePolar). Valid only when HasPos.
	// Mutated only by setNodeWorld (applyCenter's sole write path), on the node's own
	// mover goroutine.
	ScenePolar polar
	HasPos     bool // false for hand-written/partial specs with no position → nodeWorldPos returns origin
	// ReachR is the sphere REACH radius: the max distance from this node's center to
	// any node it outputs to (its surface children), under the resolved centers. It is
	// streamed in the NodeGeometry sphereR field and consumed by the TS SphereRing so the
	// "show the sphere" ring reaches every surface node even when a child was placed by a
	// different parent. 0 when the node has no outgoing edges (childless).
	ReachR float64
	// Inputs/Outputs: slice HEADERS are set once at construction (loader.go) and never
	// reassigned; individual elements' AnchorId ARE mutated in place (setPortAnchorId) —
	// so these are mutable state, not identity, even though the header never moves.
	Inputs  []portGeom
	Outputs []portGeom
}

// defaultNodeR is the default starting sphere radius (world units) used for a
// node that omits an explicit r. Tunable — chosen as a sensible starting size
// for the polar layout.
const defaultNodeR = 200.0

// nodeR returns the node's sphere radius: *g.R when set, else defaultNodeR.
func nodeR(g nodeGeom) float64 {
	if g.R != nil {
		return *g.R
	}
	return defaultNodeR
}

// Interior bead render dimensions — mirror scene-content.tsx INTERIOR_BEAD_R +
// torus tube fraction; keep in sync. Each interior bead draws a sphere of radius
// interiorBeadR PLUS a torus ring whose OUTER radius is
// interiorBeadR*(1+interiorTorusTubeFrac). The slot pitch must space by the torus
// outer radius (the larger extent), not the sphere, so adjacent rings don't touch.
const (
	interiorBeadR         = 5.0  // sphere radius (INTERIOR_BEAD_R)
	interiorTorusTubeFrac = 0.12 // torus tube fraction; outer radius = r*(1+frac)
	interiorBeadGap       = 0.2  // small gap BETWEEN adjacent toruses
)

// interiorTorusOuterR is the torus outer radius — the bead's true visual extent.
const interiorTorusOuterR = interiorBeadR * (1 + interiorTorusTubeFrac) // 5.6

// interiorSlot is the 2x2 grid half-pitch, computed TORUS-AWARE from the bead's
// torus outer radius plus half the desired inter-torus gap. Adjacent same-row
// beads are 2*interiorSlot apart, so torus-to-torus gap = 2*interiorSlot -
// 2*rt = interiorBeadGap. Pitch follows bead size (beads are a fixed visual
// size), NOT the node radius — nodeRadius is used only for the wall-fit guarantee.
const interiorSlot = interiorTorusOuterR + interiorBeadGap/2 // 5.9

// interiorSlotOffset returns the NODE-LOCAL OFFSET of the 2x2 interior grid slot
// at (row, col), relative to the node center (NOT a world position): row 0 =
// top/backup, row 1 = bottom/working; col 0 = left, col 1 = right. The grid is
// sized by the bead's TORUS OUTER RADIUS so adjacent rings keep a small gap and
// never overlap:
//
//	slot   = interiorTorusOuterR + interiorBeadGap/2
//	dx = (col - 0.5) * 2*slot
//	dy = (0.5 - row) * 2*slot
//	dz = 0
//
// The grid is centered on the node, so offsets are symmetric about (0,0). TS
// renders the bead as a child of the node group, so its world position =
// node center + offset is composed by the scene graph (no node center added on
// the Go side). Discrete — beads snap to these slot centers. The corner bead's
// torus reach (|offset| + rt) must stay inside the node sphere radius r (see
// TestInteriorBeadsInsideSphere). The Z offset is always 0 (grid is planar).
func interiorSlotOffset(row, col int) vec3 {
	slot := interiorSlot
	pitch := 2 * slot
	return vec3{
		X: (float64(col) - 0.5) * pitch,
		Y: (0.5 - float64(row)) * pitch,
		Z: 0,
	}
}

// kindWidthHeight returns the render width/height for a kind, mirroring the
// TS defaults (width ?? 110, height ?? 60) when the kind is unknown.
func kindWidthHeight(kind string) (float64, float64) {
	if d, ok := kindDims[kind]; ok {
		return d.Width, d.Height
	}
	return 110, 60
}

// nodeRadius mirrors nodeRadius() in geometry-helpers.ts:
//
//	min(width, height) / CurveParamNodeRadiusDivisor
func nodeRadius(kind string) float64 {
	w, h := kindWidthHeight(kind)
	return min(w, h) / float64(CurveParamNodeRadiusDivisor)
}

// nodeWorldPos derives a node's world center from its polar source of truth:
// SceneCenter + polar2cart(ScenePolar). This is the ONE polar→cartesian conversion for a
// node center; it happens only here, at the geometry/display boundary. A node with no
// position (HasPos false — hand-written/partial specs) falls back to the world origin.
func nodeWorldPos(g nodeGeom) vec3 {
	if !g.HasPos {
		return vec3{}
	}
	return g.SceneCenter.add(polar2cart(g.ScenePolar))
}

// setNodeWorld updates a node's polar source of truth from a world center at an INPUT
// boundary (a pointer-derived world point, or a re-propagated solve center). The held
// representation stays polar: ScenePolar = cart2polar(world − SceneCenter). Cartesian
// enters only here and at nodeWorldPos — never as a stored cartesian center.
func setNodeWorld(g *nodeGeom, world vec3) {
	g.ScenePolar = cart2polar(world.sub(g.SceneCenter))
	g.HasPos = true
}

// Ring-anchor geometry constants. These are Go-local until the TS side adopts them.
// d = anchor diameter, p = padding between anchors along the circumference.
const (
	ringAnchorDiameter = 8.0 // port anchor circle diameter (pixels)
	ringAnchorPadding  = 2.0 // gap between adjacent anchors along the ring
)

// ringAnchorCount returns the number of evenly-spaced anchors that fit around a
// node's ring given radius R: N = floor(2*pi*R / (d+p)), minimum 1.
func ringAnchorCount(R float64) int {
	pitch := ringAnchorDiameter + ringAnchorPadding
	n := int(2 * math.Pi * R / pitch)
	return max(n, 1)
}

// ringAnchorPolar returns anchor index i's position (unit radius) on a node's
// EQUATORIAL ring — the polar-torus a port rides: theta held at pi/2 (the
// equator), phi swept evenly across N anchors. This is the polar-native
// definition of the ring; ringAnchorDir derives the cartesian direction from it
// only at the boundary. i is taken mod N so out-of-range indices wrap safely.
func ringAnchorPolar(R float64, i int) polar {
	N := ringAnchorCount(R)
	i = ((i % N) + N) % N // safe mod
	phi := float64(i) * 2 * math.Pi / float64(N)
	return polar{R: 1, Theta: math.Pi / 2, Phi: phi}
}

// ringAnchorDir returns the unit direction for anchor index i in a ring of N
// evenly-spaced slots around a node of radius R, derived from ringAnchorPolar
// (the node's equatorial ring — theta=pi/2, phi swept) via polar2cart.
func ringAnchorDir(R float64, i int) vec3 {
	return polar2cart(ringAnchorPolar(R, i))
}

// snapToRingAnchorIndex returns the ring-anchor index (0..N-1) whose direction
// best matches the given direction vector for a node of the given kind. The
// winning index i maximises dot(normalize(dir), ringAnchorDir(R, i)). If dir is
// the zero vector, 0 is returned as a safe default.
func snapToRingAnchorIndex(kind string, dir vec3) int {
	R := nodeRadius(kind)
	N := ringAnchorCount(R)
	nd := dir.normalize()
	if nd.length() == 0 {
		return 0
	}
	best := -1
	bestDot := -2.0
	for i := range N {
		d := ringAnchorDir(R, i)
		dot := nd.X*d.X + nd.Y*d.Y + nd.Z*d.Z
		if dot > bestDot {
			bestDot = dot
			best = i
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// portDir mirrors portDir() in geometry-helpers.ts: the unit direction (in the
// y-up frame, z=0) from node center toward the named port, derived from
// side + slot/auto-spacing. Returns (zeroVec, false) if the port is not found.
func portDir(g nodeGeom, portName string, isInput bool) (vec3, bool) {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	idx := -1
	for i, p := range list {
		if p.Name == portName {
			idx = i
			break
		}
	}
	if idx < 0 {
		return vec3{}, false
	}
	port := list[idx]

	// AnchorId: index into the flat ring-anchor array. nil → ring slot 0 as default.
	anchorIdx := 0
	if port.AnchorId != nil {
		anchorIdx = *port.AnchorId
	}
	R := nodeRadius(g.Kind)
	return ringAnchorDir(R, anchorIdx), true
}

// portRadiusByName returns the per-port radius (distance from node center at
// which this port is drawn and its edge attaches) for the named port on g.
// This is the AUTHORITATIVE port-placement radius: it returns the port's own
// stored PortR when set. The nodeRadius(kind) formula (min(w,h)/4) is used ONLY
// as a fallback for ports that have no stored PortR — e.g. registry-default
// ports synthesized in specNode.toNodeGeom for hand-written/partial specs that
// omit inputs/outputs. This is the one remaining call site of that formula for
// port placement; every materialized port file carries its own portR.
func portRadiusByName(g nodeGeom, portName string, isInput bool) float64 {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	for _, p := range list {
		if p.Name == portName {
			if p.PortR != nil {
				return *p.PortR
			}
			break
		}
	}
	return nodeRadius(g.Kind)
}

// portRingPolar returns a port's LOCAL polar offset about its own node: the
// equatorial ring position (theta = pi/2) at the port's ring-anchor azimuth
// (phi, from AnchorId), at the port's OWN radius r_i (portRadiusByName — never
// overridden). This is the polar-torus a port rides: a ring at theta=pi/2,
// radius r_i, swept in phi. Placement is independent of any torus lock — a
// lock is movement-only and changes nothing here.
func portRingPolar(g nodeGeom, portName string, isInput bool) polar {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	anchorIdx := 0
	for _, p := range list {
		if p.Name == portName {
			if p.AnchorId != nil {
				anchorIdx = *p.AnchorId
			}
			break
		}
	}
	p := ringAnchorPolar(nodeRadius(g.Kind), anchorIdx)
	p.R = portRadiusByName(g, portName, isInput)
	return p
}

// portWorldPos returns the port's world position: the node's world center plus
// its local polar ring offset (portRingPolar), converted to cartesian at this
// one GPU boundary. Falls back to the node center when the port is
// unnamed/unknown. This is the authoritative port placement (Go owns
// geometry); the TS renderer plots from Go's streamed segments.
func portWorldPos(g nodeGeom, portName string, isInput bool) vec3 {
	center := nodeWorldPos(g)
	if portName == "" {
		return center
	}
	if _, ok := portDir(g, portName, isInput); !ok {
		return center
	}
	return center.add(polar2cart(portRingPolar(g, portName, isInput)))
}

// portDegenerateEps is the minimum partner-direction length below which an aimed port
// falls back to its ring-anchor placement: the partner center coincides with (or is
// indistinguishable from) self, so there is no well-defined aim direction.
const portDegenerateEps = 1e-9

// portWorldPosAimed returns a port's world position under the AIMED model
// (port→edge→port colinearity): a CONNECTED port (hasPartner==true) sits on its own
// node's sphere surface, in the direction of its single partner node's CENTER —
// `nodeWorldPos(self) + r_i * normalize(partnerCenter - nodeWorldPos(self))`. An
// edgeless port (hasPartner==false), or a partner center that is degenerate (≈ self,
// no well-defined direction), falls back to the existing ring-anchor placement
// (portWorldPos). partnerCenter is a CARTESIAN world point supplied by the caller —
// the one cartesian subtraction this function performs is a fresh, per-call display-
// boundary computation (never a stored offset; see aimed_ports.go).
func portWorldPosAimed(self nodeGeom, portName string, isInput bool, partnerCenter vec3, hasPartner bool) vec3 {
	if !hasPartner {
		return portWorldPos(self, portName, isInput)
	}
	center := nodeWorldPos(self)
	dir := partnerCenter.sub(center)
	if dir.length() < portDegenerateEps {
		return portWorldPos(self, portName, isInput)
	}
	return center.add(dir.normalize().scale(portRadiusByName(self, portName, isInput)))
}

// edgeSegment is the straight world segment the renderer draws for an edge: the source node's
// OUTPUT port to the target node's INPUT port. Both ports are AIMED at each other's node center
// (portWorldPosAimed), so both ports plus both centers are colinear — the edge is radial (same
// θ,φ) at each end by construction. This is the GPU boundary: nodeWorldPos/portWorldPosAimed are
// the polar→cartesian conversions, done here because WebGL needs cartesian line endpoints.
func edgeSegment(src, tgt nodeGeom, srcPort, dstPort string) wireSegment {
	start := portWorldPosAimed(src, srcPort, false, nodeWorldPos(tgt), true)
	end := portWorldPosAimed(tgt, dstPort, true, nodeWorldPos(src), true)
	return wireSegment{Start: start, End: end}
}

// edgeArcPolar is the pulse's travel budget for an edge: the distance between the two AIMED
// port positions, computed ENTIRELY IN POLAR from each node's stored ScenePolar.
//
// Both ports are aimed at the other node's center, so the two ports and the two centers are
// COLINEAR by construction (see portWorldPosAimed). That makes the port-to-port distance pure
// arithmetic: the center-to-center distance less each port's own radius. No cartesian is
// involved, so there is nothing to convert back.
//
// This deliberately does NOT go through edgeSegment. Doing so would build cartesian endpoints
// out of ScenePolar (nodeWorldPos = SceneCenter + polar2cart) and then cart2polar them straight
// back — a polar→cartesian→polar round trip that re-derives a quantity both nodes already hold,
// and whose Acos(v.Y/r)/Atan2(v.Z,v.X) degenerates for a node sitting near the scene pole.
//
// The radius subtracted is portRadiusByName, not nodeRadius(kind): portWorldPosAimed places the
// port at exactly that radius, and a materialized port may carry a PortR that differs from the
// node default (portRadiusByName falls back to nodeRadius when PortR is nil).
func edgeArcPolar(src, tgt nodeGeom, srcPort, dstPort string) float64 {
	raw := polarDist(src.ScenePolar, tgt.ScenePolar) -
		portRadiusByName(src, srcPort, false) -
		portRadiusByName(tgt, dstPort, true)
	// Overlapping nodes can drive the subtraction negative; a negative arc would poison
	// ticksToCross. The CurveParamMinArcLength floor below is the real guard, this only
	// keeps the quantize step well-defined.
	if raw < 0 {
		raw = 0
	}
	quantized := math.Round(raw/edgeLengthCellWu) * edgeLengthCellWu
	if quantized < CurveParamMinArcLength {
		return CurveParamMinArcLength
	}
	return quantized
}
