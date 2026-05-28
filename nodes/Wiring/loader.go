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
	"math"
	"os"

	T "github.com/dtauraso/wirefold/Trace"
)

// arcLengthBetween returns the straight-line distance between two node positions.
// If either position is the zero value (node not positioned), a minimum of
// minArcLength is returned so SimLatencyMs is never zero.
const minArcLength = 1.0 // world units

func arcLengthBetween(a, b specPosition) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	dz := b.Z - a.Z
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if d < minArcLength {
		return minArcLength
	}
	return d
}

// specPosition is the 3-D canvas position of a node as stored in view.nodes.
type specPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"` // optional; defaults to 0 when absent
}

// specNode mirrors the JSON node shape.
type specNode struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Index    *int      `json:"index,omitempty"`
	Data     *NodeData `json:"data,omitempty"`
	Position specPosition // populated from view.nodes after JSON parse
}

// NodeData mirrors the JSON data block on a node.
type NodeData struct {
	Init      []int          `json:"init,omitempty"`
	Repeat    bool           `json:"repeat,omitempty"`
	State map[string]int `json:"state,omitempty"` // field-seeding: struct fields via wire:"data.state"
}

// specEdge mirrors the JSON edge shape.
// Fields tagged wire:"prop,..." are wire props emitted to wire-defs.ts by gen-node-defs.
type specEdge struct {
	Label          string  `json:"label"          wire:"prop,optional,tsType:string"`
	MidpointOffset float64 `json:"midpointOffset" wire:"prop,optional,tsType:number"`
	ArrowStyle     string  `json:"arrowStyle"     wire:"prop,optional,tsType:ArrowStyle"`
	Concurrent     *bool   `json:"concurrent"     wire:"prop,optional,tsType:boolean"`
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

// WireRegistry maps edge label → *PacedWire. The stdin-reader goroutine uses
// this to call NotifyDelivered when the webview reports animation complete.
// Each entry points to the wire owned by the destination port; multiple edges
// sharing a destination port map to the same *PacedWire.
type WireRegistry map[string]*PacedWire

// ForEach calls fn for every (edgeID, wire) pair in the registry.
func (reg WireRegistry) ForEach(fn func(id string, pw *PacedWire)) {
	for id, pw := range reg {
		fn(id, pw)
	}
}

// LoadTopology reads the JSON file at jsonPath and constructs []Node plus a
// WireRegistry keyed by edge label, and a NodeMoveRegistry for live position
// updates (used by the stdin reader to handle node-move messages).
func LoadTopology(ctx context.Context, jsonPath string, tr *T.Trace) ([]Node, WireRegistry, *NodeMoveRegistry, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("LoadTopology: read %s: %w", jsonPath, err)
	}
	var spec topoSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, nil, fmt.Errorf("LoadTopology: parse %s: %w", jsonPath, err)
	}
	if err := validateSpec(&spec); err != nil {
		return nil, nil, nil, err
	}

	// Populate Position on each specNode from view.nodes.
	for i := range spec.Nodes {
		if pos, ok := spec.View.Nodes[spec.Nodes[i].ID]; ok {
			spec.Nodes[i].Position = pos
		}
	}

	// Build id→position map for arc-length computation at wire construction.
	nodePos := map[string]specPosition{}
	for _, n := range spec.Nodes {
		nodePos[n.ID] = n.Position
	}

	// Allocate one *PacedWire per destination port (fan-in safe).
	// destWire: "destNode.destPort" → *PacedWire (owned by the destination).
	// edgeWire: edge label → *PacedWire (same pointer; for stdin_reader lookup).
	// edgeEndpoints: edge label → source/target node IDs (for NodeMoveRegistry).
	destWire := map[string]*PacedWire{}
	edgeWire := WireRegistry{}
	edgeEndpoints := map[string]EdgeEndpoints{}
	for _, e := range spec.Edges {
		destKey := e.Target + "." + e.TargetHandle
		pw, exists := destWire[destKey]
		if !exists {
			arcLength := arcLengthBetween(nodePos[e.Source], nodePos[e.Target])
			pw = NewPacedWire(arcLength, PulseSpeedWuPerMs)
			destWire[destKey] = pw
		}
		edgeWire[e.Label] = pw
		edgeEndpoints[e.Label] = EdgeEndpoints{Source: e.Source, Target: e.Target}
	}

	// Build NodeMoveRegistry from initial positions and edge endpoints.
	nmrPositions := make(map[string]NodePosition, len(nodePos))
	for id, p := range nodePos {
		nmrPositions[id] = NodePosition{X: p.X, Y: p.Y, Z: p.Z}
	}
	nmr := NewNodeMoveRegistry(nmrPositions, edgeEndpoints)

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

	// Build each node.
	nodes := make([]Node, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		bind := Registry[n.Type]
		pb := newPortBindings()

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
					pb.SetSinglePaced(port.Name, edgeWire[labels[0]])
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
					pb.AppendMultiPacedWithHandle(port.Name, handle, edgeWire[lbl])
				}
				// If no outbound edges, builder falls back to a dead-end slice.
			}
		}

		nd, err := bind.Build(ctx, n.ID, n.Data, pb, tr)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("LoadTopology: build node %q: %w", n.ID, err)
		}
		nodes = append(nodes, nd)
	}

	return nodes, edgeWire, nmr, nil
}
