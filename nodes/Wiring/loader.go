// loader.go — runtime topology loader.
//
// LoadTopology reads topology.json, allocates one PacedWire per destination
// port (fan-in safe), and returns ([]Node, SlotRegistry, *MoveDispatch).
// An edge-label-keyed WireRegistry is built internally to bind each source Out to
// its wire, but it is not returned: no caller consumed the map.
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
	"math"
	"os"
	"reflect"
	"sort"

	T "github.com/dtauraso/wirefold/Trace"
)

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
	// Scene polar (polar-model.md phase 2): the node's position as (r,θ,φ) about the scene
	// sphere. When present AND a persisted scene sphere exists, world = sceneCenter +
	// polar2cart(scenePolar) is AUTHORITATIVE over x/y/z (which stay for back-compat).
	ScenePolarR     *float64 `json:"scenePolarR,omitempty"`
	ScenePolarTheta *float64 `json:"scenePolarTheta,omitempty"`
	ScenePolarPhi   *float64 `json:"scenePolarPhi,omitempty"`
	// Quantized polar offset (quantized_layout.go): the node's (iTheta,iPhi,iR) integer
	// offset about the ONE scene sphere center — every node is independent (no reference/
	// parent). All three MUST be present together (all-or-nothing) for the stored offset
	// to be adopted; a node with any of these absent (an "old scene") is measured from its
	// scenePolar-derived world center instead (loader.go computeQuantizedLayout).
	QuantITheta *int `json:"quantITheta,omitempty"`
	QuantIPhi   *int `json:"quantIPhi,omitempty"`
	QuantIR     *int `json:"quantIR,omitempty"`
	// Per-node step constants (quantized_layout.go quantizedOffset.cTheta/cPhi/cR): this
	// node's OWN quantization step, turning its integer scalars into a world offset. nil
	// (unset) falls back to the global default (stepTheta/stepPhi/stepR).
	StepTheta *float64 `json:"stepTheta,omitempty"`
	StepPhi   *float64 `json:"stepPhi,omitempty"`
	StepR     *float64 `json:"stepR,omitempty"`
	// LocalPolars is this node's list of per-neighbor local polars (layout_holder.go
	// LocalPolar) — one per domain double-link this node is an endpoint of, measured
	// with ITSELF as center. Absent (nil) → computed fresh at load (computeLocalPolars).
	LocalPolars []specLocalPolar `json:"localPolars,omitempty"`
	// LocalPole is this node's rotating local pole (rotating_pole.go LayoutHolder) — the
	// frame every entry in LocalPolars above is quantized about. Absent (nil) → no pole
	// persisted yet; computeLocalPolars seeds one (initLocalPole) and it persists on the
	// node's next local-polar write.
	LocalPoleTheta *float64 `json:"localPoleTheta,omitempty"`
	LocalPolePhi   *float64 `json:"localPolePhi,omitempty"`
	// Gate marks this node as a two-neighbor GATE node (node_move.go): on a direct
	// drag it solves its own equal-radii landing position against its two domain
	// neighbors (derived from LocalPolars, in the same order), commits, and
	// self-triggers its own edge-c equalize. NOT derivable from degree (other
	// 2-link nodes exist that are plain leaves) — authored in the spec.
	Gate bool `json:"gate,omitempty"`
}

// specLocalPolar mirrors one entry of a node's persisted localPolars list
// (loader_tree.go jsonMeta.LocalPolars carries the same shape).
type specLocalPolar struct {
	To string `json:"to"`
	// Role names this neighbor's part in the decentralized cascade rule
	// (node_move.go) — "source" / "follower" / "" (neither). See
	// layout_holder.go LocalPolar.Role's doc.
	Role        string  `json:"role,omitempty"`
	QuantITheta int     `json:"quantITheta"`
	QuantIPhi   int     `json:"quantIPhi"`
	QuantIR     int     `json:"quantIR"`
	StepTheta   float64 `json:"stepTheta,omitempty"`
	StepPhi     float64 `json:"stepPhi,omitempty"`
	StepR       float64 `json:"stepR,omitempty"`
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
func (n specNode) toNodeGeom(sceneCenter vec3) nodeGeom {
	// Position is POLAR (polar-frame-rewrite.md). The stored ScenePolar (r,θ,φ about the scene
	// sphere center) is the ONLY stored position and is adopted directly — there is no cartesian
	// x/y/z load path. When it is absent the node has no position (HasPos false → nodeWorldPos
	// returns origin). Scene presence does not gate polar adoption: the stored polar is
	// authoritative regardless.
	g := nodeGeom{Kind: n.Type, Label: n.label(), R: n.R, SceneCenter: sceneCenter}
	if n.ScenePolarR != nil && n.ScenePolarTheta != nil && n.ScenePolarPhi != nil {
		g.ScenePolar = polar{R: *n.ScenePolarR, Theta: *n.ScenePolarTheta, Phi: *n.ScenePolarPhi}
		g.HasPos = true
	}
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
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	SourceHandle string `json:"sourceHandle"`
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle"`
}

// topoSpec is the top-level JSON shape.
type topoSpec struct {
	Nodes []specNode `json:"nodes"`
	Edges []specEdge `json:"edges"`
}

// WireRegistry maps edge label → *PacedWire. Each entry points to the wire owned by
// the destination port; multiple edges sharing a destination port map to the same *PacedWire.
// It is an internal build aid (binding source Out → wire); it is not returned.
type WireRegistry map[string]*PacedWire

// LoadTopology reads the JSON file at jsonPath and constructs []Node plus a
// SlotRegistry (keyed by "target.targetHandle" for delivery acks) and a MoveDispatch
// (key→inbox registry for the decentralized node-move path: each node and edge owns
// its own recompute).
//
// clk is the single monotonic clock injected into every PacedWire so each wire
// times its own delivery on it (MODEL.md: exactly one clock). Production and
// tests alike pass a RealClock — the model is sleep-only.
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
// or building. LoadTopology validates + builds from the result.
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

	// Phase 1b: quantized flat absolute scene-polar layout (quantized_layout.go) —
	// resolved BEFORE reach/wire/dispatch phases so every later phase computes from the
	// COMPOSED (authoritative) centers, not the raw loaded ones. Every node is a root
	// measured about the scene center — no reference/parent concept.
	quantizedOffsets map[string]quantizedOffset

	// Phase 1c: double-link LOCAL POLAR data (layout_holder.go) — every domain
	// double-link (bidirectional edge pair) gives each endpoint its own local
	// polar to the other, measured with ITSELF as center. Computed AFTER the
	// quantized layout so it reads the composed (authoritative) centers, and
	// injected into each built node's LocalPolars field (buildNodes) — additive,
	// does not feed back into position (quantizedOffsets stays authoritative).
	localPolars map[string][]LocalPolar
	// localPoles is each node's resolved rotating local pole (rotating_pole.go), computed
	// alongside localPolars above from the SAME composed world centers — persisted
	// (specNode.LocalPoleTheta/Phi) if present, otherwise freshly seeded+kicked.
	localPoles map[string]dir

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
}

// buildFromSpec constructs nodes, wires, and the MoveDispatch from an already-parsed
// and validated topoSpec. It orchestrates the phase helpers below in the same order
// the original monolithic function performed them; behavior is unchanged.
func buildFromSpec(ctx context.Context, spec topoSpec, tr *T.Trace, clk Clock, sphere sceneSphere, hasScene bool) ([]Node, SlotRegistry, *MoveDispatch, error) {
	b := &buildCtx{ctx: ctx, spec: spec, tr: tr, clk: clk, sphere: sphere, hasScene: hasScene}

	b.computeNodeGeometry()
	b.computeQuantizedLayout()
	b.computeLocalPolars()
	b.computeReachRadii()
	b.allocateWires()
	b.buildMoveDispatch()
	b.buildTypeMaps()
	b.buildEdgeMaps()
	if err := b.buildNodes(); err != nil {
		return nil, nil, nil, err
	}
	b.emitLayoutLinks()
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
		nodeGeoms[n.ID] = n.toNodeGeom(b.sphere.Center)
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

// computeQuantizedLayout makes the quantized flat absolute scene-polar layout
// (quantized_layout.go) AUTHORITATIVE for every node's world center. It resolves each
// node's quantizedOffset — the stored quantITheta/quantIPhi/quantIR when ALL THREE are
// present (a scene saved under this model), otherwise the offset MEASURED from the
// node's current (pre-quantized) scenePolar-derived center (an old scene, or a node
// whose scenePolar was hand-authored) — then recomputes every node's world center
// directly about the scene center (every node independent — no reference/parent) and
// overwrites b.nodeGeoms/b.centers with the result. Every later phase (reach radii,
// per-edge arc/segment, the movers seeded in buildMoveDispatch) therefore operates on
// the composed centers, and md.quantizedLayout defaults to true (buildMoveDispatch) so
// the live drag path (RootMove) treats this same offset model as authoritative too.
func (b *buildCtx) computeQuantizedLayout() {
	ids := make(map[string]bool, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		ids[n.ID] = true
	}

	// The scalar triple is the STORED quantI* when a scene was saved under this model
	// (all three present); otherwise it is MEASURED from the node's currently-loaded
	// (pre-quantized, scenePolar-derived) center — the fallback for an un-migrated node.
	// prior carries each node's stored per-node step constants (when present in the
	// spec) so measureScalars preserves them into the fallback-measured offset instead
	// of defaulting to global constants for a node that DOES have its own.
	prior := make(map[string]quantizedOffset, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		o := quantizedOffset{}
		if n.StepTheta != nil {
			o.cTheta = *n.StepTheta
		}
		if n.StepPhi != nil {
			o.cPhi = *n.StepPhi
		}
		if n.StepR != nil {
			o.cR = *n.StepR
		}
		prior[n.ID] = o
	}

	measured := measureScalars(b.centers, ids, b.sphere.Center, prior)
	offsets := make(map[string]quantizedOffset, len(b.spec.Nodes))
	// exact marks nodes whose EXACT position was persisted as scenePolar (r,θ,φ). For
	// those, the loaded center (toNodeGeom placed it at exactly that polar) is the
	// authoritative position — it is NOT overwritten by the quantized reconstruction
	// below, so a dragged node reloads at exactly where it was dropped. The quantized
	// triple for such a node is just measured bookkeeping.
	exact := make(map[string]bool, len(b.spec.Nodes))
	for _, n := range b.spec.Nodes {
		if n.ScenePolarR != nil && n.ScenePolarTheta != nil && n.ScenePolarPhi != nil {
			exact[n.ID] = true
			if off, ok := measured[n.ID]; ok {
				offsets[n.ID] = off
			} else {
				offsets[n.ID] = prior[n.ID]
			}
			continue
		}
		if n.QuantITheta != nil && n.QuantIPhi != nil && n.QuantIR != nil {
			o := quantizedOffset{
				iTheta: *n.QuantITheta,
				iPhi:   *n.QuantIPhi,
				iR:     *n.QuantIR,
			}
			if n.StepTheta != nil {
				o.cTheta = *n.StepTheta
			}
			if n.StepPhi != nil {
				o.cPhi = *n.StepPhi
			}
			if n.StepR != nil {
				o.cR = *n.StepR
			}
			offsets[n.ID] = o
			continue
		}
		if off, ok := measured[n.ID]; ok {
			offsets[n.ID] = off
			continue
		}
		offsets[n.ID] = prior[n.ID] // centerless → default to the scene center, keep any stored constants
	}
	b.quantizedOffsets = offsets

	// Reconstruct world centers from the quantized triple ONLY for nodes without an exact
	// stored scenePolar (legacy / un-migrated). Nodes with an exact scenePolar keep the
	// verbatim loaded center — their drag position round-trips losslessly.
	derived := deriveCenters(offsets, b.sphere.Center)
	for id, pos := range derived {
		if exact[id] {
			continue
		}
		b.centers[id] = pos
		if g, ok := b.nodeGeoms[id]; ok {
			setNodeWorld(&g, pos)
			b.nodeGeoms[id] = g
		}
	}
}

// computeLocalPolars resolves each node's LocalPolars list (layout_holder.go
// LocalPolar) — additive, double-link local-polar DATA layered on top of the
// authoritative absolute quantized layout computed just above.
//
// A node's neighbors are every node it shares a domain edge with (either
// direction), deduplicated — 2To5 and 5To2 give node 2 a single neighbor 5, and
// node 5 a single neighbor 2. Each is resolved from the STORED spec value when
// present (a migrated node's meta.json localPolars entry for that neighbor),
// otherwise MEASURED fresh from the composed world centers (b.centers, already
// overwritten by computeQuantizedLayout) using this node's own effective step
// constants: iTheta=round(theta/stepTheta), iPhi=round(phi/stepPhi),
// iR=round(R/stepR) — the same snap contract as measureScalars, but with the
// NEIGHBOR (not the scene center) as the polar origin, and THIS node's steps
// (not the neighbor's).
func (b *buildCtx) computeLocalPolars() {
	neighbors := map[string]map[string]bool{}
	for _, e := range b.spec.Edges {
		if neighbors[e.Source] == nil {
			neighbors[e.Source] = map[string]bool{}
		}
		if neighbors[e.Target] == nil {
			neighbors[e.Target] = map[string]bool{}
		}
		neighbors[e.Source][e.Target] = true
		neighbors[e.Target][e.Source] = true
	}

	stored := map[string]map[string]specLocalPolar{}
	specByID := map[string]specNode{}
	for _, n := range b.spec.Nodes {
		specByID[n.ID] = n
		if len(n.LocalPolars) == 0 {
			continue
		}
		m := make(map[string]specLocalPolar, len(n.LocalPolars))
		for _, lp := range n.LocalPolars {
			m[lp.To] = lp
		}
		stored[n.ID] = m
	}

	result := map[string][]LocalPolar{}
	poles := map[string]dir{}
	for _, n := range b.spec.Nodes {
		nbrs := neighbors[n.ID]
		if len(nbrs) == 0 {
			continue
		}
		ids := make([]string, 0, len(nbrs))
		for id := range nbrs {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic order

		ownCenter, hasOwn := b.centers[n.ID]
		// Local-polar quantization uses its OWN small, uniform cells (layout_holder.go
		// localStepTheta/localStepPhi/localStepR) — NOT the scene-center triple's
		// coarser stepTheta/stepPhi/stepR (the point of the double-link model: every
		// distance lands on a whole tick of a small grid).
		t, p, r := LocalPolar{}.effectiveSteps()

		// Resolve this node's rotating local pole (rotating_pole.go) from EVERY
		// neighbor's CURRENT world offset (available for all ids regardless of whether
		// that neighbor's own bearing entry is stored or freshly measured below — the
		// pole tracks the live geometry, not the persisted quant cache), seeded from
		// the persisted pole when present.
		sn := specByID[n.ID]
		pole := dir{}
		hasPole := sn.LocalPoleTheta != nil && sn.LocalPolePhi != nil
		if hasPole {
			pole = dir{Theta: *sn.LocalPoleTheta, Phi: *sn.LocalPolePhi}
		}
		dirs := map[string]dir{}
		if hasOwn {
			for _, mid := range ids {
				if mCenter, ok := b.centers[mid]; ok {
					d, _ := dirFromOffset(mCenter.sub(ownCenter))
					dirs[mid] = d
				}
			}
		}
		finalPole, converged := resolveLocalPole(pole, hasPole, dirs)
		if !converged && b.tr != nil {
			b.tr.Breadcrumb("pole.kick.uncapped", n.ID, "", fmt.Sprintf("neighbors=%d", len(dirs)))
		}
		poles[n.ID] = finalPole

		list := make([]LocalPolar, 0, len(ids))
		for _, mid := range ids {
			if sm, ok := stored[n.ID]; ok {
				if lp, ok2 := sm[mid]; ok2 {
					entry := LocalPolar{
						To: mid, Role: lp.Role, QuantITheta: lp.QuantITheta, QuantIPhi: lp.QuantIPhi, QuantIR: lp.QuantIR,
						StepTheta: lp.StepTheta, StepPhi: lp.StepPhi, StepR: lp.StepR,
					}
					// The stored bearing may have been quantized about a DIFFERENT pole
					// (pre-feature data quantized about world +y, or a kick since moved
					// the pole) — re-quantize the bearing about finalPole from the live
					// offset when one is available, preserving Role/QuantIR/step
					// constants exactly (QuantIR carries the equal-radii shared-c
					// contract and must not be recomputed).
					if mCenter, ok3 := b.centers[mid]; hasOwn && ok3 {
						d, _ := dirFromOffset(mCenter.sub(ownCenter))
						et, ep, _ := entry.effectiveSteps()
						c, psi := azimuthFrom(finalPole, d)
						entry.QuantITheta = int(math.Round(c / et))
						entry.QuantIPhi = int(math.Round(psi / ep))
					}
					list = append(list, entry)
					continue
				}
			}
			mCenter, ok := b.centers[mid]
			if !hasOwn || !ok {
				list = append(list, LocalPolar{To: mid}) // centerless → zero offset, nothing to measure
				continue
			}
			d, radius := dirFromOffset(mCenter.sub(ownCenter))
			c, psi := azimuthFrom(finalPole, d)
			list = append(list, LocalPolar{
				To:          mid,
				QuantITheta: int(math.Round(c / t)),
				QuantIPhi:   int(math.Round(psi / p)),
				QuantIR:     int(math.Round(radius / r)),
			})
		}
		result[n.ID] = list
	}
	b.localPolars = result
	b.localPoles = poles
}

// deriveCascadeRoles builds the per-node cascadeRoleSpec map node_move.go's
// ApplyCascadeRoles wants, from each node's own LocalPolars entries (Role:
// "source"/"follower"/"anchor", authored in the spec) and its own Gate flag — the
// decentralization of what used to be three hardcoded package-level tables
// (ruleSource/ruleFollowers/gateNeighbors). A gate node's two fixed neighbors are
// its LocalPolars list IN ORDER (computeLocalPolars sorts ids alphabetically),
// reproducing the historical gateNeighbors table's (a,b) ordering for nodes 1/9/10
// exactly. A SECOND pass reads Role=="anchor" off a GATE node's own LocalPolars
// entries and appends that gate's id to the NAMED neighbor's AnchoredGates —
// EXPLICIT per gate-neighbor-pair authoring, deliberately NOT "every neighbor of
// every gate": a node can be one of a gate's two fixed neighbors without being its
// anchor (nodes 2/3 are gate 1's neighbors but neither is its anchor; only node 6 is
// marked anchor on gates 9 and 10 — the historical node-6-shaped relationship). Must
// run AFTER computeLocalPolars (b.localPolars populated).
func (b *buildCtx) deriveCascadeRoles() map[string]cascadeRoleSpec {
	gateSet := map[string]bool{}
	for _, n := range b.spec.Nodes {
		if n.Gate {
			gateSet[n.ID] = true
		}
	}
	roles := map[string]cascadeRoleSpec{}
	for id, lps := range b.localPolars {
		spec := cascadeRoleSpec{}
		for _, lp := range lps {
			switch lp.Role {
			case "source":
				spec.SourceID = lp.To
			case "follower":
				spec.Followers = append(spec.Followers, lp.To)
			}
		}
		if gateSet[id] {
			spec.Gate = true
			if len(lps) >= 2 {
				spec.GateA, spec.GateB = lps[0].To, lps[1].To
			}
		}
		roles[id] = spec
	}
	for gID := range gateSet {
		for _, lp := range b.localPolars[gID] {
			if lp.Role != "anchor" {
				continue
			}
			anchor := roles[lp.To]
			anchor.AnchoredGates = append(anchor.AnchoredGates, gID)
			roles[lp.To] = anchor
		}
	}
	return roles
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
	// SPEC order (b.spec.Nodes/Edges — the deterministic directory-sorted order parseSpec
	// read the topology in), NOT map iteration order, so the buffer's row seed
	// (md.NodeSeeds/EdgeSeeds) gives every node/edge a deterministic row.
	nodeOrder := make([]string, len(b.spec.Nodes))
	for i, n := range b.spec.Nodes {
		nodeOrder[i] = n.ID
	}
	edgeOrder := make([]string, len(b.spec.Edges))
	for i, e := range b.spec.Edges {
		edgeOrder[i] = e.Label
	}
	md := newMoveDispatch(b.nodeGeoms, b.edgeEndpoints, b.tr, nodeOrder, edgeOrder)
	md.ApplyCascadeRoles(b.deriveCascadeRoles())
	if b.hasScene {
		// Persisted scene sphere: install it now so md.sceneSphere is consistent straight out
		// of LoadTopology (a fresh/legacy scene has none — main.go's LoadSceneSphere then
		// content-fits it from the loaded node centers).
		md.sceneSphere = b.sphere
	}

	// The quantized layout is authoritative by default — b.quantizedOffsets was already
	// resolved (stored offset, or measured from the pre-quantized center) by
	// computeQuantizedLayout, which also overwrote b.nodeGeoms so the nodeMovers newMoveDispatch
	// just built above are already seeded from the composed centers. Seed each node's OWN
	// mover field (nodeMover.quantOffset) from it here — there is no shared md.quantizedOffsets
	// map anymore (that map, read/written by multiple mover goroutines for different keys,
	// was the "concurrent map read and map write" fatal fixed by node6-drag-decentralized.md's
	// per-node ownership). A node missing an entry in b.quantizedOffsets keeps its
	// nodeMover's zero-value quantOffset, matching the old map's zero-value-on-miss read.
	md.quantizedLayout = true
	for id, off := range b.quantizedOffsets {
		if nm, ok := md.nodeMovers[id]; ok {
			nm.quantOffset = off
		}
	}
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
		pb.clock = b.clk // shared clock for clock-paced interior animation (Input refill slide)

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
		// Attach this node's computed LocalPolars list (layout_holder.go) via the
		// promoted embedded Wiring.LayoutHolder every kind gets — so the node's
		// layout goroutine owns it without per-kind wiring. Locate the embedded
		// *LayoutHolder by reflection (same field-lookup builders.go/loader.go use
		// elsewhere for port/data injection), then load through its own locked
		// setter rather than reflecting on the unexported localPolars field
		// directly, so this initial load goes through the same mutex every other
		// LocalPolars access does.
		if v := reflect.ValueOf(nd).Elem(); v.Kind() == reflect.Struct {
			if lhField := v.FieldByName("LayoutHolder"); lhField.IsValid() && lhField.CanAddr() {
				if lh, ok := lhField.Addr().Interface().(*LayoutHolder); ok {
					if lps, ok := b.localPolars[n.ID]; ok {
						lh.LoadLocalPolars(lps)
					}
					// Attach this node's resolved rotating local pole (rotating_pole.go),
					// computed alongside LocalPolars above (persisted verbatim if present,
					// otherwise freshly seeded+kicked) — always set when the node has any
					// neighbors (computeLocalPolars only populates b.localPoles for those).
					if pole, ok := b.localPoles[n.ID]; ok {
						lh.SetLocalPole(pole)
					}
					// Register this node's embedded *Wiring.LayoutHolder with the move
					// dispatcher so a later drag (RootMove) can route a local-polar
					// re-quantize to the OWNING node's own holder — MoveDispatch never
					// copies or owns LocalPolars itself.
					b.md.layoutHolders[n.ID] = lh
				}
			}
		}
		nodes = append(nodes, nd)
	}
	b.outSink = outSink
	b.nodes = nodes
	return nil
}

// emitLayoutLinks streams each double-linked node pair once via tr.LayoutLink, sourced from
// the per-node LocalPolars list computed by computeLocalPolars (b.localPolars) — the LAYOUT
// model, NOT b.spec.Edges (the bead-edge graph). Each unordered pair emits exactly once, from
// its alphabetically-first id, so Go (not the renderer) owns de-duplication. This is the sole
// source of the buffer's LayoutLink block: the layout-link overlay must reflect
// LocalPolars even on a topology where its pairs diverge from the Edge block's.
func (b *buildCtx) emitLayoutLinks() {
	ids := make([]string, 0, len(b.localPolars))
	for id := range b.localPolars {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, lp := range b.localPolars[id] {
			if id < lp.To {
				b.tr.LayoutLink(id, lp.To)
			}
		}
	}
}

// bindDispatch binds per-edge source Outs and dest wires into each edgeMover so
// a node-move updates per-edge travel-time and the per-port window aggregate,
// and seeds each dest wire's per-edge latency for the MaxIncomingSimLatencyMs
// aggregate.
func (b *buildCtx) bindDispatch() {
	b.md.Bind(b.outSink, SlotRegistry(b.destWire))
}
