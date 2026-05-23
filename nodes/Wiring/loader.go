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

// specNode mirrors the JSON node shape.
type specNode struct {
	ID    string    `json:"id"`
	Type  string    `json:"type"`
	Index *int      `json:"index,omitempty"`
	Data  *NodeData `json:"data,omitempty"`
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
	ValueLabel     string  `json:"valueLabel"     wire:"prop,optional,tsType:string"`
	MidpointOffset float64 `json:"midpointOffset" wire:"prop,optional,tsType:number"`
	ArrowStyle     string  `json:"arrowStyle"     wire:"prop,optional,tsType:ArrowStyle"`
	Concurrent     *bool   `json:"concurrent"     wire:"prop,optional,tsType:boolean"`
	Kind           string  `json:"kind"           wire:"prop,required,tsType:EdgeKind"`
	Source         string  `json:"source"`
	SourceHandle   string  `json:"sourceHandle"`
	Target         string  `json:"target"`
	TargetHandle   string  `json:"targetHandle"`
}

// topoSpec is the top-level JSON shape.
type topoSpec struct {
	Nodes []specNode `json:"nodes"`
	Edges []specEdge `json:"edges"`
}

// WireRegistry maps edge label → *PacedWire. The stdin-reader goroutine uses
// this to call NotifyDelivered when the webview reports animation complete.
// Each entry points to the wire owned by the destination port; multiple edges
// sharing a destination port map to the same *PacedWire.
type WireRegistry map[string]*PacedWire

// LoadTopology reads the JSON file at jsonPath and constructs []Node plus a
// WireRegistry keyed by edge label.
func LoadTopology(ctx context.Context, jsonPath string, tr *T.Trace) ([]Node, WireRegistry, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, nil, fmt.Errorf("LoadTopology: read %s: %w", jsonPath, err)
	}
	var spec topoSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, fmt.Errorf("LoadTopology: parse %s: %w", jsonPath, err)
	}

	// Validate all kinds up front.
	// also caught by TS parser; defense-in-depth
	for _, n := range spec.Nodes {
		if _, ok := Registry[n.Type]; !ok {
			return nil, nil, fmt.Errorf("LoadTopology: node %q: unknown type %q", n.ID, n.Type)
		}
	}

	// Allocate one *PacedWire per destination port (fan-in safe).
	// destWire: "destNode.destPort" → *PacedWire (owned by the destination).
	// edgeWire: edge label → *PacedWire (same pointer; for stdin_reader lookup).
	destWire := map[string]*PacedWire{}
	edgeWire := WireRegistry{}
	for _, e := range spec.Edges {
		// also caught by TS parser; defense-in-depth
		if e.Label == "" {
			return nil, nil, fmt.Errorf("LoadTopology: edge %q→%q has empty label", e.Source, e.Target)
		}
		destKey := e.Target + "." + e.TargetHandle
		pw, exists := destWire[destKey]
		if !exists {
			pw = NewPacedWire()
			destWire[destKey] = pw
		}
		edgeWire[e.Label] = pw
	}

	// Build id→type map and per-kind port lookup sets (needed for normalization and validation).
	nodeType := map[string]string{}
	for _, n := range spec.Nodes {
		nodeType[n.ID] = n.Type
	}
	kindInPorts := map[string]map[string]bool{}
	kindOutPorts := map[string]map[string]bool{}
	kindOutMultiPorts := map[string]map[string]bool{}
	for kind, bind := range Registry {
		ins := map[string]bool{}
		outs := map[string]bool{}
		outMultis := map[string]bool{}
		for _, p := range bind.Ports {
			switch p.Dir {
			case PortIn:
				ins[p.Name] = true
			case PortOut:
				outs[p.Name] = true
			case PortOutMulti:
				outMultis[p.Name] = true
				outs[p.Name] = true
			}
		}
		kindInPorts[kind] = ins
		kindOutPorts[kind] = outs
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

	// Validation 1: port handle names must match declared ports on the node kind.
	for _, e := range spec.Edges {
		srcKind := nodeType[e.Source]
		srcHandle := e.SourceHandle
		if base, isMulti := outMultiBaseName(srcHandle, srcKind); isMulti {
			srcHandle = base
		}
		if !kindOutPorts[srcKind][srcHandle] {
			return nil, nil, fmt.Errorf("LoadTopology: edge %q: sourceHandle %q is not an output port on kind %q", e.Label, e.SourceHandle, srcKind)
		}
		tgtKind := nodeType[e.Target]
		if !kindInPorts[tgtKind][e.TargetHandle] {
			return nil, nil, fmt.Errorf("LoadTopology: edge %q: targetHandle %q is not an input port on kind %q", e.Label, e.TargetHandle, tgtKind)
		}
	}

	// Validation 2: required input ports must have an inbound edge.
	for _, n := range spec.Nodes {
		bind := Registry[n.Type]
		for _, port := range bind.Ports {
			if !port.Required {
				continue
			}
			if _, ok := inbound[n.ID][port.Name]; !ok {
				return nil, nil, fmt.Errorf("LoadTopology: node %q: required input port %q has no inbound edge", n.ID, port.Name)
			}
		}
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
			return nil, nil, fmt.Errorf("LoadTopology: build node %q: %w", n.ID, err)
		}
		nodes = append(nodes, nd)
	}

	return nodes, edgeWire, nil
}
