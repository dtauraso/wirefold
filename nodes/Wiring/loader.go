// loader.go — runtime topology loader.
//
// LoadTopology reads topology.json, allocates one PacedWire per destination
// port (fan-in safe), and returns ([]Node, WireRegistry). WireRegistry is
// keyed by edge label and is consumed by the stdin-reader goroutine (see
// RunStdinReader in main.go) to dispatch "delivered" messages from the webview.
//
// Key behaviors:
//   - One *PacedWire per (destNode, destPort); multiple edges sharing a
//     destination port reuse the same wire (fan-in support).
//   - WireRegistry maps edge label → wire for stdin_reader NotifyDelivered.
//   - Input nodes: data.init values pre-seeded via pw.Send in a goroutine.
//   - ChainInhibitor: data.state["held"] → Held via wire:"data.state" tag.
//   - Slice output ports (ToEdge): all outbound wires appended in spec order.
//   - Output ports with no outbound edge: dead-end chan int (buf 1).

package Wiring

import (
	"context"
	"encoding/json"
	"fmt"
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
// Side/Slot drive port placement; they mirror Port.side / Port.slot in
// tools/topology-vscode/src/schema/types.ts. Slot is a pointer so an absent
// slot (auto-spacing) is distinguishable from slot 0.
type specPort struct {
	Name string `json:"name"`
	Side string `json:"side,omitempty"`
	Slot *int   `json:"slot,omitempty"`
}

// specNode mirrors the JSON node shape.
type specNode struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Index    *int       `json:"index,omitempty"`
	Data     *NodeData  `json:"data,omitempty"`
	Inputs   []specPort `json:"inputs,omitempty"`
	Outputs  []specPort `json:"outputs,omitempty"`
	Position specPosition // populated from view.nodes after JSON parse
}

// toNodeGeom builds the geometry descriptor for arc-length computation,
// resolving the port lists from the spec node (falling back to the kind's
// registry ports with default sides when the spec omits inputs/outputs).
func (n specNode) toNodeGeom() nodeGeom {
	g := nodeGeom{Kind: n.Type, Pos: vec3{X: n.Position.X, Y: n.Position.Y, Z: n.Position.Z}}
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

func specPortsToGeom(ports []specPort) []portGeom {
	out := make([]portGeom, 0, len(ports))
	for _, p := range ports {
		out = append(out, portGeom{Name: p.Name, Side: p.Side, Slot: p.Slot})
	}
	return out
}

// NodeData mirrors the JSON data block on a node.
type NodeData struct {
	Init      []int          `json:"init,omitempty"`
	Repeat    bool           `json:"repeat,omitempty"`
	State map[string]int `json:"state,omitempty"` // field-seeding: struct fields via wire:"data.state"
	// SendRules is the node-owned per-output-port send policy, keyed by output
	// port name (the sourceHandle, e.g. "ToNext0"). Absent ports default to
	// consumeGated. The send rule belongs to the SOURCE NODE, not the edge.
	SendRules map[string]string `json:"sendRules,omitempty"`
}

// specEdge mirrors the JSON edge shape.
// Fields tagged wire:"prop,..." are wire props emitted to wire-defs.ts by gen-node-defs.
type specEdge struct {
	Label          string  `json:"label"          wire:"prop,optional,tsType:string"`
	ArrowStyle     string  `json:"arrowStyle,omitempty"`
	Concurrent     *bool   `json:"concurrent,omitempty"`
	Kind           string  `json:"kind"           wire:"prop,required,tsType:EdgeKind"`
	Source         string  `json:"source"`
	SourceHandle   string  `json:"sourceHandle"`
	Target         string  `json:"target"`
	TargetHandle   string  `json:"targetHandle"`
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

// WireRegistry maps edge label → *PacedWire. Used for fade/node-move operations
// where lookup by edge label is needed. Each entry points to the wire owned by
// the destination port; multiple edges sharing a destination port map to the same *PacedWire.
type WireRegistry map[string]*PacedWire

// ForEach calls fn for every (edgeID, wire) pair in the registry.
func (reg WireRegistry) ForEach(fn func(id string, pw *PacedWire)) {
	for id, pw := range reg {
		fn(id, pw)
	}
}

// LoadTopology reads the JSON file at jsonPath and constructs []Node plus a
// SlotRegistry (keyed by "target.targetHandle" for delivery acks), a WireRegistry
// (keyed by edge label for fade/node-move), and a NodeMoveRegistry for live position
// updates (used by the stdin reader to handle node-move messages).
func LoadTopology(ctx context.Context, jsonPath string, tr *T.Trace) ([]Node, SlotRegistry, WireRegistry, *NodeMoveRegistry, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("LoadTopology: read %s: %w", jsonPath, err)
	}
	var spec topoSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("LoadTopology: parse %s: %w", jsonPath, err)
	}
	if err := validateSpec(&spec); err != nil {
		return nil, nil, nil, nil, err
	}

	// Populate Position on each specNode from view.nodes.
	for i := range spec.Nodes {
		if pos, ok := spec.View.Nodes[spec.Nodes[i].ID]; ok {
			spec.Nodes[i].Position = pos
		}
	}

	// Build id→position and id→geometry maps for arc-length computation at wire
	// construction. nodeGeom carries kind/dims/port side+slot so the Go arc
	// length mirrors buildPortCurve (3-D port-to-port) exactly.
	nodePos := map[string]specPosition{}
	nodeGeoms := map[string]nodeGeom{}
	for _, n := range spec.Nodes {
		nodePos[n.ID] = n.Position
		nodeGeoms[n.ID] = n.toNodeGeom()
	}

	// Allocate one *PacedWire per destination port (fan-in safe).
	// destWire: "destNode.destPort" → *PacedWire (owned by the destination).
	// edgeWire: edge label → *PacedWire (same pointer; for stdin_reader lookup).
	// edgeEndpoints: edge label → source/target node IDs + handles (for NodeMoveRegistry).
	destWire := map[string]*PacedWire{}
	edgeWire := WireRegistry{}
	edgeEndpoints := map[string]EdgeEndpoints{}
	// edgeArc / edgeLatency carry each edge's OWN travel-time (per-edge geometry),
	// distinct from the dest wire's MaxIncomingSimLatencyMs aggregate. Keyed by
	// edge label; consumed below when binding the source Out.
	edgeArc := map[string]float64{}
	edgeLatency := map[string]float64{}
	for _, e := range spec.Edges {
		destKey := e.Target + "." + e.TargetHandle
		// Per-edge arc length / latency from this edge's own port-to-port geometry.
		arcLength := arcLengthBetweenPorts(
			nodeGeoms[e.Source], e.SourceHandle,
			nodeGeoms[e.Target], e.TargetHandle,
		)
		simLatencyMs := arcLength / PulseSpeedWuPerMs
		edgeArc[e.Label] = arcLength
		edgeLatency[e.Label] = simLatencyMs
		pw, exists := destWire[destKey]
		if !exists {
			pw = NewPacedWire(arcLength, PulseSpeedWuPerMs)
			pw.Target = e.Target
			pw.TargetHandle = e.TargetHandle
			pw.Trace = tr
			destWire[destKey] = pw
		} else if simLatencyMs > pw.MaxIncomingSimLatencyMs {
			// Fan-in: raise the per-port window aggregate to the max over all
			// edges feeding this destination port.
			pw.MaxIncomingSimLatencyMs = simLatencyMs
		}
		edgeWire[e.Label] = pw
		edgeEndpoints[e.Label] = EdgeEndpoints{
			Source: e.Source, Target: e.Target,
			SourceHandle: e.SourceHandle, TargetHandle: e.TargetHandle,
		}
	}

	// Build NodeMoveRegistry from initial geometry and edge endpoints. The
	// registry recomputes port-to-port arc length on node-move, so it needs the
	// full per-node geometry (kind/dims/ports), not just position.
	nmr := NewNodeMoveRegistry(nodeGeoms, edgeEndpoints)

	// Build id→type map and per-kind OutMulti port set (needed for sourceHandle normalization).
	nodeType := map[string]string{}
	for _, n := range spec.Nodes {
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

	// outMultiBaseName strips a trailing digit suffix from a sourceHandle when
	// the base name is an OutMulti port on the given kind.
	// e.g. "ToNext0" → "ToNext" for a kind that has OutMulti port "ToNext".
	// Returns the canonical port name and whether it resolved.
	outMultiBaseName := func(handle, kind string) (string, bool) {
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

	// Build inbound and outbound edge maps.
	// inbound:  target node id → port name → destKey ("destNode.destPort")
	// outbound: source node id → port name → []edge label
	// outboundHandle: source node id → port name → []sourceHandle (indexed, same order as outbound)
	// For OutMulti ports, sourceHandle may be "<portName><index>" — normalize to portName.
	inbound := map[string]map[string]string{}
	outbound := map[string]map[string][]string{}
	outboundHandle := map[string]map[string][]string{}
	for _, e := range spec.Edges {
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
		if base, isMulti := outMultiBaseName(e.SourceHandle, nodeType[e.Source]); isMulti {
			srcKey = base
		}
		outbound[e.Source][srcKey] = append(outbound[e.Source][srcKey], e.Label)
		outboundHandle[e.Source][srcKey] = append(outboundHandle[e.Source][srcKey], e.SourceHandle)
	}

	// nodeSendRule looks up the node-owned per-output-port send rule for the
	// given node id and output port name (sourceHandle). The rule lives on the
	// SOURCE NODE's data.sendRules map, keyed by output port name. Ports not
	// listed default to consumeGated.
	nodeSendRule := func(n specNode, port string) SendRule {
		if n.Data == nil || n.Data.SendRules == nil {
			return RuleConsumeGated
		}
		// ParseSendRule returns RuleConsumeGated for "" and errors for
		// unrecognised values. validate.go rejects bad values before we
		// reach here, so the error branch is defence-in-depth only.
		rule, err := ParseSendRule(n.Data.SendRules[port])
		if err != nil {
			return RuleConsumeGated
		}
		return rule
	}

	// Build each node. outSink collects every paced source Out keyed by
	// "node.handle" so node-move can update per-edge travel-time on the Out.
	outSink := map[string]*Out{}
	nodes := make([]Node, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		bind := Registry[n.Type]
		pb := newPortBindings()
		pb.outSink = outSink

		for _, port := range bind.Ports {
			switch port.Dir {
			case PortIn:
				dk, ok := inbound[n.ID][port.Name]
				if ok {
					pb.SetSinglePaced(port.Name, destWire[dk])
				}
				// If no inbound edge, reflectBuild falls back to dead-end chan.

			case PortOut:
				labels := outbound[n.ID][port.Name]
				if len(labels) > 0 {
					// Look up wire by destination of the first outbound edge.
					// For fan-in, the destination port owns the wire.
					// Send rule is node-owned, keyed by this output port name.
					rule := nodeSendRule(n, port.Name)
					lbl := labels[0]
					pb.SetSinglePacedRule(port.Name, edgeWire[lbl], rule, edgeArc[lbl], edgeLatency[lbl])
				}
				// If no outbound edge, reflectBuild falls back to dead-end chan.

			case PortOutMulti:
				labels := outbound[n.ID][port.Name]
				handles := outboundHandle[n.ID][port.Name]
				for i, lbl := range labels {
					handle := port.Name
					if i < len(handles) {
						handle = handles[i]
					}
					// Per-port (per fan-out element): the rule is keyed by the
					// concrete output port name (sourceHandle, e.g. "ToNext0").
					rule := nodeSendRule(n, handle)
					pb.AppendMultiPacedWithHandle(port.Name, handle, edgeWire[lbl], rule, edgeArc[lbl], edgeLatency[lbl])
				}
				// If no outbound edges, builder falls back to a dead-end slice.
			}
		}

		nd, err := bind.Build(ctx, n.ID, n.Data, pb, tr)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("LoadTopology: build node %q: %w", n.ID, err)
		}
		nodes = append(nodes, nd)
	}

	// Index per-edge source Outs and dest wires into the move registry so
	// node-move can update per-edge travel-time and the per-port window aggregate.
	nmr.SetEdgeOuts(outSink, SlotRegistry(destWire))

	return nodes, SlotRegistry(destWire), edgeWire, nmr, nil
}
