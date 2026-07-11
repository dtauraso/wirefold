// loader.go — runtime topology loader.
//
// LoadTopology reads topology.json, allocates one PacedWire per destination
// port (fan-in safe), and returns ([]Node, SlotRegistry, *MoveDispatch).
// An edge-label-keyed WireRegistry is built internally to bind each source Out to
// its wire, but it is not returned: fade ops route via MoveDispatch.dispatch and no
// caller consumed the map.
//
// Key behaviors:
//   - One *PacedWire per (destNode, destPort); multiple edges sharing a
//     destination port reuse the same wire (fan-in support).
//   - SlotRegistry maps "target.targetHandle" → wire for create/delete ops.
//   - Input nodes: data.init values pre-seeded via pw.Send in a goroutine.
//   - HoldNewSendOld: data.state["held"] → Held via wire:"data.state" tag.
//   - Slice output ports (ToEdge): all outbound wires appended in spec order.
//   - Output ports with no outbound edge: dead-end chan int (buf 1).

package Wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	T "github.com/dtauraso/wirefold/Trace"
)

// specPosition is the 3-D canvas position of a node as stored in view.nodes.
type specPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"` // optional; defaults to 0 when absent
}

// specPort mirrors the per-node inputs/outputs entries in topology.json.
// AnchorId is the only placement field; side/slot/anchor have been removed.
type specPort struct {
	Name     string   `json:"name"`
	AnchorId *int     `json:"anchorId,omitempty"` // optional ring-anchor index (flat array); highest priority
	PortR    *float64 `json:"portR,omitempty"`    // optional per-port radius (distance from node center); nil → nodeRadius(kind) fallback
}

// specNode mirrors the JSON node shape.
type specNode struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Index   *int       `json:"index,omitempty"`
	Data    *NodeData  `json:"data,omitempty"`
	Inputs  []specPort `json:"inputs,omitempty"`
	Outputs []specPort `json:"outputs,omitempty"`
	R       *float64   `json:"r,omitempty"` // optional per-node sphere radius for this node's edges (nil → default; see nodeR)
	X       float64    `json:"x"`           // legacy absolute world center (cartesian; back-compat)
	Y       float64    `json:"y"`
	Z       float64    `json:"z"`
	// Scene polar (polar-model.md phase 2): the node's position as (r,θ,φ) about the scene
	// sphere. When present AND a persisted scene sphere exists, world = sceneCenter +
	// polar2cart(scenePolar) is AUTHORITATIVE over x/y/z (which stay for back-compat).
	ScenePolarR     *float64 `json:"scenePolarR,omitempty"`
	ScenePolarTheta *float64 `json:"scenePolarTheta,omitempty"`
	ScenePolarPhi   *float64 `json:"scenePolarPhi,omitempty"`
	// Quantized polar offset (quantized_layout.go, PHASE 3): the node's (iTheta,iPhi,iR)
	// integer offset about its spanning-tree parent's forward direction. All three MUST be
	// present together (all-or-nothing) for the stored offset to be adopted; a node with any
	// of these absent (an "old scene") is snapped from its scenePolar-derived world center
	// instead (loader.go computeQuantizedLayout / quantized_layout.go snapQuantizedOffsets).
	QuantITheta *int `json:"quantITheta,omitempty"`
	QuantIPhi   *int `json:"quantIPhi,omitempty"`
	QuantIR     *int `json:"quantIR,omitempty"`
	// Reference is the node's OWNED reference (the peer whose frame its triple is measured
	// in). nil ⇒ seed from the spanning tree; a stored value OVERRIDES the seed (manual
	// reference picking). "" means the node is its own root.
	Reference *string `json:"reference,omitempty"`
}

// label returns the node's human label: data.label when present and non-empty,
// otherwise the node id. Mirrors the TS `n.data?.label ?? n.id` fallback so the
// new-system label sidecar renders the same pill text the old spec store produced.
func (n specNode) label() string {
	if n.Data != nil && n.Data.Label != "" {
		return n.Data.Label
	}
	return n.ID
}

// toNodeGeom builds the geometry descriptor for arc-length computation,
// resolving the port lists from the spec node (falling back to the kind's
// registry ports with default sides when the spec omits inputs/outputs).
func (n specNode) toNodeGeom(sceneCenter vec3, hasScene bool) nodeGeom {
	// Position is POLAR (polar-frame-rewrite.md). The stored ScenePolar (r,θ,φ about the scene
	// sphere center) is the ONLY stored position and is adopted directly — there is no cartesian
	// x/y/z load path. When it is absent the node has no position (HasPos false → nodeWorldPos
	// returns origin).
	g := nodeGeom{Kind: n.Type, Label: n.label(), R: n.R, SceneCenter: sceneCenter}
	if n.ScenePolarR != nil && n.ScenePolarTheta != nil && n.ScenePolarPhi != nil {
		g.ScenePolar = polar{R: *n.ScenePolarR, Theta: *n.ScenePolarTheta, Phi: *n.ScenePolarPhi}
		g.HasPos = true
	}
	_ = hasScene // scene presence no longer gates polar adoption; the stored polar is authoritative.
	g.Inputs = specPortsToGeom(n.Inputs)
	g.Outputs = specPortsToGeom(n.Outputs)
	// Fallback to registry ports when the spec omits the lists (keeps geometry
	// well-defined for hand-written topologies that rely on default placement).
	if len(g.Inputs) == 0 || len(g.Outputs) == 0 {
		if bind, ok := Registry[n.Type]; ok {
			if len(g.Inputs) == 0 {
				for _, p := range bind.Ports {
					if p.Dir == PortIn {
						g.Inputs = append(g.Inputs, portGeom{Name: p.Name})
					}
				}
			}
			if len(g.Outputs) == 0 {
				for _, p := range bind.Ports {
					if p.Dir == PortOut || p.Dir == PortOutMulti {
						g.Outputs = append(g.Outputs, portGeom{Name: p.Name})
					}
				}
			}
		}
	}
	return g
}

// outMultiBaseName strips a trailing digit suffix from a sourceHandle when the
// base name is an OutMulti port on the given kind, per kindOutMultiPorts (kind →
// set of OutMulti port names). e.g. "ToNext0" → "ToNext" for a kind with OutMulti
// port "ToNext". Returns the canonical port name and whether it resolved. Shared
// by buildFromSpec and validateSpec so the two normalizations can never drift.
func outMultiBaseName(handle, kind string, kindOutMultiPorts map[string]map[string]bool) (string, bool) {
	if len(handle) == 0 {
		return handle, false
	}
	last := handle[len(handle)-1]
	if last < '0' || last > '9' {
		return handle, false
	}
	base := handle[:len(handle)-1]
	if kindOutMultiPorts[kind][base] {
		return base, true
	}
	return handle, false
}

func specPortsToGeom(ports []specPort) []portGeom {
	out := make([]portGeom, 0, len(ports))
	for _, p := range ports {
		out = append(out, portGeom(p))
	}
	return out
}

// NodeData mirrors the JSON data block on a node.
type NodeData struct {
	// Label is the node's human label (optional). When absent, the node id is used
	// as the label. Streamed on node-geometry events for the new-system label sidecar.
	Label  string         `json:"label,omitempty"`
	Init   []int          `json:"init,omitempty"`
	Repeat bool           `json:"repeat,omitempty"`
	State  map[string]int `json:"state,omitempty"` // field-seeding: struct fields via wire:"data.state"
	// SendRules is the node-owned per-output-port send policy, keyed by output
	// port name (the sourceHandle, e.g. "ToNext0"). Absent ports default to
	// consumeGated. The send rule belongs to the SOURCE NODE, not the edge.
	SendRules map[string]string `json:"sendRules,omitempty"`
}

// specEdge mirrors the JSON edge shape.
// Fields tagged wire:"prop,..." are wire props emitted to wire-defs.ts by gen-node-defs.
type specEdge struct {
	Label        string `json:"label"          wire:"prop,required,tsType:string"`
	Kind         string `json:"kind"           wire:"prop,required,tsType:EdgeKind"`
	Source       string `json:"source"`
	SourceHandle string `json:"sourceHandle"`
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle"`
}

// topoView is the viewer-state block inside the JSON (view.nodes carries positions).
type topoView struct {
	Nodes map[string]specPosition `json:"nodes"`
}

// topoSpec is the top-level JSON shape.
type topoSpec struct {
	Nodes []specNode `json:"nodes"`
	Edges []specEdge `json:"edges"`
	View  topoView   `json:"view"`
}

// WireRegistry maps edge label → *PacedWire. Each entry points to the wire owned by
// the destination port; multiple edges sharing a destination port map to the same *PacedWire.
// It is an internal build aid (binding source Out → wire); it is not returned, and
// fade is routed via MoveDispatch (per-wire dispatch), not via this map.
type WireRegistry map[string]*PacedWire

// LoadTopology reads the JSON file at jsonPath and constructs []Node plus a
// SlotRegistry (keyed by "target.targetHandle" for delivery acks) and a MoveDispatch
// (key→inbox registry for the decentralized node-move path: each node and edge owns
// its own recompute).
//
// clk is the single monotonic clock injected into every PacedWire so each wire
// times its own delivery on it (MODEL.md: exactly one clock). Production passes a
// RealClock; tests pass a FakeClock they advance deterministically.
func LoadTopology(ctx context.Context, jsonPath string, tr *T.Trace, clk Clock) ([]Node, SlotRegistry, *MoveDispatch, error) {
	spec, err := parseSpec(jsonPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := validateSpec(&spec); err != nil {
		return nil, nil, nil, err
	}
	// Load the persisted scene sphere (if any) BEFORE positioning nodes, so nodes stored as
	// scene polar can be placed as sceneCenter + polar2cart(scenePolar). A persisted sphere
	// is not derived from node positions, so there is no circularity; a fresh/legacy scene
	// has none and nodes fall back to cartesian x/y/z (polar-model.md phase 2b).
	sphere, hasScene := loadSceneSphere(jsonPath)
	return buildFromSpec(ctx, spec, tr, clk, sphere, hasScene)
}

// parseSpec reads and parses the topology spec at path — a directory tree
// (loadTree) or a monolithic topology.json — into a topoSpec, WITHOUT validating
// or building. Shared by LoadTopology (which then validates + builds) and
// EmitSpecLine (which only needs the parsed spec).
func parseSpec(path string) (topoSpec, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() { // path-resolution-ok: loader dispatch, not scene path resolution
		return loadTree(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return topoSpec{}, fmt.Errorf("LoadTopology: read %s: %w", path, err)
	}
	var spec topoSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return topoSpec{}, fmt.Errorf("LoadTopology: parse %s: %w", path, err)
	}
	return spec, nil
}

// buildCtx carries the shared state threaded through the buildFromSpec phase
// helpers below. Each phase method populates its own fields from spec (and
// fields written by earlier phases); buildFromSpec calls them in order and
// stays a short orchestrator. Splitting on struct fields (rather than
// threading a long parameter list) mirrors the original function's data
// flow exactly — no behavior changes, only the grouping into named steps.
type buildCtx struct {
	ctx      context.Context
	spec     topoSpec
	tr       *T.Trace
	clk      Clock
	sphere   sceneSphere
	hasScene bool

	// Phase 1: node geometry + world centers.
	nodeGeoms map[string]nodeGeom
	centers   map[string]vec3

	// Phase 1b: quantized hierarchical polar layout (quantized_layout.go, PHASE 3) —
	// resolved BEFORE reach/wire/dispatch phases so every later phase computes from the
	// COMPOSED (authoritative) centers, not the raw loaded ones.
	quantizedOffsets map[string]quantizedOffset
	// references is the owned per-node reference map (node id → reference id, "" for a
	// root), seeded from the spanning tree and overridable per node (manual picking).
	references map[string]string

	// Phase 4: per-destination-port wire allocation + per-edge geometry.
	destWire      map[string]*PacedWire
	edgeWire      WireRegistry
	edgeEndpoints map[string]EdgeEndpoints
	edgeArc       map[string]float64
	edgeLatency   map[string]float64
	edgeSegments  map[string]wireSegment

	// Phase 5: the MoveDispatch.
	md *MoveDispatch

	// Phase 6: id→type map and per-kind OutMulti port set.
	nodeType          map[string]string
	kindOutMultiPorts map[string]map[string]bool

	// Phase 7: inbound/outbound edge maps.
	inbound        map[string]map[string]string
	outbound       map[string]map[string][]string
	outboundHandle map[string]map[string][]string

	// Phase 8: built nodes + the paced-Out sink.
	outSink map[string]*Out
	nodes   []Node

	// layoutPorts is the hidden layout graph: one *LayoutPort per node id,
	// mirroring the domain edge set one-for-one (source -> target). Built by
	// buildLayoutEdges and injected into each node struct's `Layout` field by
	// buildNodes, the same way the shared clock is threaded through pb.
	layoutPorts map[string]*LayoutPort
}

// buildFromSpec constructs nodes, wires, and the MoveDispatch from an already-parsed
// and validated topoSpec. It orchestrates the phase helpers below in the same order
// the original monolithic function performed them; behavior is unchanged.
func buildFromSpec(ctx context.Context, spec topoSpec, tr *T.Trace, clk Clock, sphere sceneSphere, hasScene bool) ([]Node, SlotRegistry, *MoveDispatch, error) {
	b := &buildCtx{ctx: ctx, spec: spec, tr: tr, clk: clk, sphere: sphere, hasScene: hasScene}

	b.computeNodeGeometry()
	b.computeQuantizedLayout()
	b.computeReachRadii()
	b.allocateWires()
	b.buildMoveDispatch()
	b.buildLayoutEdges()
	b.buildTypeMaps()
	b.buildEdgeMaps()
	if err := b.buildNodes(); err != nil {
		return nil, nil, nil, err
	}
	b.bindDispatch()

	return b.nodes, SlotRegistry(b.destWire), b.md, nil
}

// computeNodeGeometry builds the id→geometry map used for arc-length computation
// at wire construction (nodeGeom carries kind/dims/port side+slot so the Go arc
// length mirrors buildPortCurve exactly), plus the shared world-center map built
// ONCE from that geometry and reused by the reach-radius pass, the aimed-port
// registry, and the edge-geometry centerOf closure. Each node's world center is
// loaded directly from its spec (meta.json x/y/z, injected as nodeGeom.Center in
// toNodeGeom); nothing later mutates a node's Center, so this snapshot stays
// authoritative for the whole build (the reach-radius pass writes ReachR, which
// does not affect centers).
func (b *buildCtx) computeNodeGeometry() {
	nodeGeoms := map[string]nodeGeom{}
	for _, n := range b.spec.Nodes {
		nodeGeoms[n.ID] = n.toNodeGeom(b.sphere.Center, b.hasScene)
	}
	b.nodeGeoms = nodeGeoms

	centers := map[string]vec3{}
	for id, g := range nodeGeoms {
		if g.HasPos {
			centers[id] = nodeWorldPos(g)
		}
	}
	b.centers = centers
}

// computeQuantizedLayout is PHASE 3: makes the quantized hierarchical polar layout
// (quantized_layout.go) AUTHORITATIVE for every node's world center. It resolves each
// node's quantizedOffset — the stored quantITheta/quantIPhi/quantIR when ALL THREE are
// present (a scene saved under this model), otherwise the offset SNAPPED from the node's
// current (pre-quantized) scenePolar-derived center (an old scene, or a node whose
// scenePolar was hand-authored) — then recomposes every node's world center from those
// offsets and overwrites b.nodeGeoms/b.centers with the composed result. Every later phase
// (reach radii, per-edge arc/segment, the movers seeded in buildMoveDispatch) therefore
// operates on the COMPOSED centers, and md.quantizedLayout defaults to true (buildMoveDispatch)
// so the live drag path (RootMove) treats this same offset model as authoritative too.
//
// Isolated nodes (no edges at all) are not covered by buildSpanningTree/snapQuantizedOffsets
// (which only walk the edge graph); they are folded in here as their own root with a
// zero offset, so EVERY node in the spec ends up with an entry.
func (b *buildCtx) computeQuantizedLayout() {
	// Owned reference map (peer-to-peer): each node's reference comes ONLY from its stored
	// `reference` (manually chosen). No spanning-tree seed. A node with no stored reference
	// is a root ("" — snaps to the scene sphere). The scalar triple (iTheta,iPhi,iR) is then
	// MEASURED from the loaded position relative to that reference's frame; positions are not
	// recomposed (individual), the triple is stored bookkeeping.
	references := make(map[string]string, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		if n.Reference != nil {
			references[n.ID] = *n.Reference
		} else {
			references[n.ID] = "" // no stored reference → a root
		}
	}
	b.references = references

	// The scalar triple is the STORED quantI* when a scene was saved under this model
	// (all three present); otherwise it is MEASURED from the node's currently-loaded
	// (pre-quantized, scenePolar-derived) center — the fallback for an un-migrated node.
	measured := measureScalars(b.centers, references, b.sphere.Center)
	offsets := make(map[string]quantizedOffset, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		if n.QuantITheta != nil && n.QuantIPhi != nil && n.QuantIR != nil {
			offsets[n.ID] = quantizedOffset{
				iTheta: *n.QuantITheta,
				iPhi:   *n.QuantIPhi,
				iR:     *n.QuantIR,
				parent: references[n.ID],
			}
			continue
		}
		if off, ok := measured[n.ID]; ok {
			offsets[n.ID] = off
			continue
		}
		offsets[n.ID] = quantizedOffset{parent: references[n.ID]} // centerless → keep its reference
	}
	b.quantizedOffsets = offsets

	// Recompose every node's world center from the authoritative scalar triples
	// (references-before-dependents), overwriting the raw loaded centers/geoms so every
	// later phase operates on the composed result.
	derived := deriveCenters(offsets, references, b.sphere.Center)
	for id, pos := range derived {
		b.centers[id] = pos
		if g, ok := b.nodeGeoms[id]; ok {
			setNodeWorld(&g, pos)
			b.nodeGeoms[id] = g
		}
	}
}

// computeReachRadii computes each node's REACH radius (max distance from its
// center to any node it outputs to) under the loaded centers — non-rooted layout
// — streamed in NodeGeometry's sphereR field so the TS SphereRing reaches every
// surface node. Computed before newMoveDispatch so each node/edge mover captures
// it in its held geom.
func (b *buildCtx) computeReachRadii() {
	edges := make([]sphereEdge, 0, len(b.spec.Edges))
	for _, e := range b.spec.Edges {
		edges = append(edges, sphereEdge{Source: e.Source, Target: e.Target})
	}
	polars := map[string]polar{}
	for id, g := range b.nodeGeoms {
		if g.HasPos {
			polars[id] = g.ScenePolar
		}
	}
	for id, r := range reachRFromPolar(polars, edges) {
		g := b.nodeGeoms[id]
		g.ReachR = r
		b.nodeGeoms[id] = g
	}
}

// allocateWires allocates one *PacedWire per destination port (fan-in safe) and
// computes each edge's own travel-time (arc length / sim latency) and
// straight-segment endpoints.
//   - destWire: "destNode.destPort" → *PacedWire (owned by the destination).
//   - edgeWire: edge label → *PacedWire (same pointer; for stdin_reader lookup).
//   - edgeEndpoints: edge label → source/target node IDs + handles (for NodeMoveRegistry).
//   - edgeArc / edgeLatency: each edge's OWN travel-time (per-edge geometry),
//     distinct from the dest wire's MaxIncomingSimLatencyMs aggregate.
//   - edgeSegments: each edge's straight-segment endpoints (Start/End) so the
//     bead's position stream evaluates P(t)=Start+t*(End-Start).
//
// All keyed by edge label; consumed by buildNodes when binding the source Out.
func (b *buildCtx) allocateWires() {
	destWire := map[string]*PacedWire{}
	edgeWire := WireRegistry{}
	edgeEndpoints := map[string]EdgeEndpoints{}
	edgeArc := map[string]float64{}
	edgeLatency := map[string]float64{}
	edgeSegments := map[string]wireSegment{}
	for _, e := range b.spec.Edges {
		destKey := e.Target + "." + e.TargetHandle
		// Per-edge segment + arc, node-to-node (polar-frame-rewrite.md option A). The arc
		// (pulse travel budget) is the polar law-of-cosines distance between the two node
		// positions (edgeArcPolar) — pure polar. The segment is the world node-to-node line
		// for the renderer (edgeSegment), the GPU-boundary cartesian.
		srcG, tgtG := b.nodeGeoms[e.Source], b.nodeGeoms[e.Target]
		seg := edgeSegment(srcG, tgtG, e.SourceHandle, e.TargetHandle)
		arcLength := edgeArcPolar(srcG, tgtG, e.SourceHandle, e.TargetHandle)
		simLatencyMs := arcLength / PulseSpeedWuPerMs
		edgeArc[e.Label] = arcLength
		edgeLatency[e.Label] = simLatencyMs
		edgeSegments[e.Label] = seg
		pw, exists := destWire[destKey]
		if !exists {
			pw = NewPacedWire(arcLength, PulseSpeedWuPerTick)
			pw.Target = e.Target
			pw.TargetHandle = e.TargetHandle
			pw.Trace = b.tr
			pw.SetClock(b.clk) // one clock shared by every wire; times its own delivery
			destWire[destKey] = pw
		}
		// Fan-in MaxIncomingSimLatencyMs is NOT pre-written here. md.Bind (called from
		// bindDispatch) calls PacedWire.SetIncomingLatency for every feeding edge, which
		// is the CANONICAL source: it records each edge's SimLatencyMs and recomputes the
		// per-port aggregate as the max over all of them. A manual raise here would just
		// be overwritten by that authoritative pass.
		edgeWire[e.Label] = pw
		edgeEndpoints[e.Label] = EdgeEndpoints{
			Source: e.Source, Target: e.Target,
			SourceHandle: e.SourceHandle, TargetHandle: e.TargetHandle,
		}
	}
	b.destWire = destWire
	b.edgeWire = edgeWire
	b.edgeEndpoints = edgeEndpoints
	b.edgeArc = edgeArc
	b.edgeLatency = edgeLatency
	b.edgeSegments = edgeSegments
}

// buildLayoutEdges builds the hidden layout graph: one *LayoutPort per node id
// (including isolated nodes with no edges), then mirrors every domain spec
// edge (source -> target) onto it via connectTo. This is a parallel edge set
// to the domain wires built by allocateWires — same connectivity, carrying
// LayoutMsg instead of beads. buildNodes injects each node's port via
// pb.layout, the same closure-injection mechanism EmitGeometry uses.
//
// Runs AFTER buildMoveDispatch so each port's apply/applyDirect closures can call
// straight into THAT node's own nodeMover.applyCenter (node_move.go, SLICE 3: an
// in-process call, not a channel hop — this node's own Update() goroutine is the
// sole writer of nm.geom's position fields) and through the (possibly
// still-unarmed) quantOffsetPersist. b.md may be nil in tests that call
// buildLayoutEdges directly without building a MoveDispatch first; apply/
// applyDirect are left nil in that case (Handle nil-guards them).
func (b *buildCtx) buildLayoutEdges() {
	ports := make(map[string]*LayoutPort, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		p := NewLayoutPort(n.ID)
		off := b.quantizedOffsets[n.ID]
		p.iTheta = off.iTheta
		p.iPhi = off.iPhi
		p.iR = off.iR
		p.forwardsRadius = ForwardsRadius(n.Type)
		if b.md != nil {
			id := n.ID
			ref := b.references[n.ID]
			iTheta, iPhi := off.iTheta, off.iPhi
			md := b.md
			p.apply = func(center vec3, iR int) {
				md.applyLayoutCenter(id, center, iTheta, iPhi, iR, ref)
			}
			p.applyDirect = func(center vec3, reach float64) {
				md.applyLayoutCenterDirect(id, center, reach)
			}
		}
		ports[n.ID] = p
	}
	for _, e := range b.spec.Edges {
		src, ok := ports[e.Source]
		if !ok {
			continue
		}
		dst, ok := ports[e.Target]
		if !ok {
			continue
		}
		src.connectTo(dst)
	}
	b.layoutPorts = ports
	if b.md != nil {
		b.md.layoutPorts = ports
	}
}

// buildMoveDispatch builds the MoveDispatch from initial geometry and edge
// endpoints. It creates one nodeMover per node and one edgeMover per edge; each
// owns its geometry and recomputes itself on a node-move (no central
// coordinator). The trace lets each mover stream its own node/edge geometry on a
// move. Outs + dest wires are bound later (bindDispatch) once node construction
// has populated them. Also declares the double-link movement graph (links.go;
// polar locks ride on it in a later step — the lock system and the central polar
// position store have been removed, so node positions live in the movers' held
// geometry) and installs the aimed-port registry for drag-time aiming.
func (b *buildCtx) buildMoveDispatch() {
	md := newMoveDispatch(b.nodeGeoms, b.edgeEndpoints, b.tr)
	if b.hasScene {
		// Persisted scene sphere: install it now so md.sceneSphere is consistent straight out
		// of LoadTopology (a fresh/legacy scene has none — main.go's LoadSceneSphere then
		// content-fits it from the loaded node centers).
		md.sceneSphere = b.sphere
	}

	// Phase 3: the quantized layout is authoritative by default — md.quantizedOffsets was
	// already resolved (stored offset, or snapped from the pre-quantized center) by
	// computeQuantizedLayout, which also overwrote b.nodeGeoms so the nodeMovers newMoveDispatch
	// just built above are already seeded from the COMPOSED centers.
	md.quantizedLayout = true
	md.quantizedOffsets = b.quantizedOffsets
	md.references = b.references
	b.md = md
}

// buildTypeMaps builds the id→type map and per-kind OutMulti port set (needed
// for sourceHandle normalization in buildEdgeMaps).
func (b *buildCtx) buildTypeMaps() {
	nodeType := map[string]string{}
	for _, n := range b.spec.Nodes {
		nodeType[n.ID] = n.Type
	}
	kindOutMultiPorts := map[string]map[string]bool{}
	for kind, bind := range Registry {
		outMultis := map[string]bool{}
		for _, p := range bind.Ports {
			if p.Dir == PortOutMulti {
				outMultis[p.Name] = true
			}
		}
		kindOutMultiPorts[kind] = outMultis
	}
	b.nodeType = nodeType
	b.kindOutMultiPorts = kindOutMultiPorts
}

// buildEdgeMaps builds the inbound and outbound edge maps.
//   - inbound:  target node id → port name → destKey ("destNode.destPort")
//   - outbound: source node id → port name → []edge label
//   - outboundHandle: source node id → port name → []sourceHandle (indexed, same order as outbound)
//
// For OutMulti ports, sourceHandle may be "<portName><index>" — normalize to portName.
func (b *buildCtx) buildEdgeMaps() {
	inbound := map[string]map[string]string{}
	outbound := map[string]map[string][]string{}
	outboundHandle := map[string]map[string][]string{}
	for _, e := range b.spec.Edges {
		if inbound[e.Target] == nil {
			inbound[e.Target] = map[string]string{}
		}
		if outbound[e.Source] == nil {
			outbound[e.Source] = map[string][]string{}
		}
		if outboundHandle[e.Source] == nil {
			outboundHandle[e.Source] = map[string][]string{}
		}
		inbound[e.Target][e.TargetHandle] = e.Target + "." + e.TargetHandle
		srcKey := e.SourceHandle
		if base, isMulti := outMultiBaseName(e.SourceHandle, b.nodeType[e.Source], b.kindOutMultiPorts); isMulti {
			srcKey = base
		}
		outbound[e.Source][srcKey] = append(outbound[e.Source][srcKey], e.Label)
		outboundHandle[e.Source][srcKey] = append(outboundHandle[e.Source][srcKey], e.SourceHandle)
	}
	b.inbound = inbound
	b.outbound = outbound
	b.outboundHandle = outboundHandle
}

// nodeSendRule looks up the node-owned per-output-port send rule for the
// given node id and output port name (sourceHandle). The rule lives on the
// SOURCE NODE's data.sendRules map, keyed by output port name. Ports not
// listed default to consumeGated.
func nodeSendRule(n specNode, port string) SendRule {
	if n.Data == nil || n.Data.SendRules == nil {
		return RuleConsumeGated
	}
	// ParseSendRule returns RuleConsumeGated for "" AND on error (unrecognised
	// value), so the fallback is already baked into its return value; the
	// error is deliberately ignored here (validate.go rejects bad values
	// before we reach here, so this is defence-in-depth only, and nodeSendRule's
	// callers aren't set up to handle a propagated error).
	rule, _ := ParseSendRule(n.Data.SendRules[port])
	return rule
}

// buildNodes builds each node from the wire allocation and edge maps computed by
// earlier phases. outSink collects every paced source Out keyed by "node.handle"
// so node-move can update per-edge travel-time on the Out.
func (b *buildCtx) buildNodes() error {
	outSink := map[string]*Out{}
	nodes := make([]Node, 0, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		bind := Registry[n.Type]
		pb := newPortBindings()
		pb.outSink = outSink
		pb.clock = b.clk                // shared clock for clock-paced interior animation (Input refill slide)
		pb.layout = b.layoutPorts[n.ID] // hidden layout-graph port mirroring this node's domain edges

		for _, port := range bind.Ports {
			switch port.Dir {
			case PortIn:
				dk, ok := b.inbound[n.ID][port.Name]
				if ok {
					pb.SetSinglePaced(port.Name, b.destWire[dk])
				}
				// If no inbound edge, reflectBuild falls back to dead-end chan.

			case PortOut:
				labels := b.outbound[n.ID][port.Name]
				if len(labels) > 0 {
					// Look up wire by destination of the first outbound edge.
					// For fan-in, the destination port owns the wire.
					// Send rule is node-owned, keyed by this output port name.
					rule := nodeSendRule(n, port.Name)
					lbl := labels[0]
					pb.SetSinglePacedRule(port.Name, b.edgeWire[lbl], rule, b.edgeArc[lbl], b.edgeLatency[lbl], b.edgeSegments[lbl], lbl)
				}
				// If no outbound edge, reflectBuild falls back to dead-end chan.

			case PortOutMulti:
				labels := b.outbound[n.ID][port.Name]
				handles := b.outboundHandle[n.ID][port.Name]
				for i, lbl := range labels {
					handle := port.Name
					if i < len(handles) {
						handle = handles[i]
					}
					// Per-port (per fan-out element): the rule is keyed by the
					// concrete output port name (sourceHandle, e.g. "ToNext0").
					rule := nodeSendRule(n, handle)
					pb.AppendMultiPacedWithHandle(port.Name, handle, b.edgeWire[lbl], rule, b.edgeArc[lbl], b.edgeLatency[lbl], b.edgeSegments[lbl], lbl)
				}
				// If no outbound edges, builder falls back to a dead-end slice.
			}
		}

		// Reuse the exact partnerCenter lookup already installed on this node's mover
		// (buildMoveDispatch runs before buildNodes) so the INITIAL geometry emit and every
		// later re-emit compute a connected port's aim identically.
		var pc partnerCenterFn
		if nm, ok := b.md.nodeMovers[n.ID]; ok {
			pc = nm.partnerCenter
		}
		nd, err := bind.Build(b.ctx, n.ID, n.Data, pb, b.tr, b.nodeGeoms[n.ID], pc)
		if err != nil {
			return fmt.Errorf("LoadTopology: build node %q: %w", n.ID, err)
		}
		nodes = append(nodes, nd)
	}
	b.outSink = outSink
	b.nodes = nodes
	return nil
}

// bindDispatch binds per-edge source Outs and dest wires into each edgeMover so
// a node-move updates per-edge travel-time and the per-port window aggregate,
// and seeds each dest wire's per-edge latency for the MaxIncomingSimLatencyMs
// aggregate.
func (b *buildCtx) bindDispatch() {
	b.md.Bind(b.outSink, SlotRegistry(b.destWire))
}

// EmitSpecLine reads the topology spec at jsonPath and writes a single
// {"kind":"spec","nodes":[...],"edges":[...],"view":{...}} JSON line to w.
// Called by main.go before node goroutines start so the TS webview receives
// the full spec on startup without reading topology/ files directly.
func EmitSpecLine(w io.Writer, jsonPath string) error {
	spec, err := parseSpec(jsonPath)
	if err != nil {
		return err
	}
	// emitEdge adds the canonical "id" field (== label) that parseSpec requires
	// for edge identity. specEdge itself carries only label (the on-disk tree
	// shape), so we widen it here at the bridge boundary.
	type emitEdge struct {
		ID string `json:"id"`
		specEdge
	}
	edges := make([]emitEdge, len(spec.Edges))
	for i, e := range spec.Edges {
		edges[i] = emitEdge{ID: e.Label, specEdge: e}
	}
	type specMsg struct {
		Kind  string     `json:"kind"`
		Nodes []specNode `json:"nodes"`
		Edges []emitEdge `json:"edges"`
		View  topoView   `json:"view"`
	}
	b, err := json.Marshal(specMsg{Kind: "spec", Nodes: spec.Nodes, Edges: edges, View: spec.View})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}
