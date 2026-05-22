// loader.go — runtime topology loader.
//
// LoadTopology reads topology.json, allocates channels, and returns []S.Node
// in spec order. Behaviorally equivalent to the generated Wire() in Wiring.go.
//
// Key behaviors (previously codegen, now runtime):
//   - One chan int (buf 1) per edge; label is its identity.
//   - Input nodes: input channel sized max(len(init),1), pre-loaded with init values.
//   - ChainInhibitor: data.initialSlots["held"] → HeldValue.
//   - Slice output ports (ToEdge): all outbound channels appended in spec order.
//   - Output ports with no outbound edge: dead-end chan int (buf 1).
//   - initialSlots entries mapped to input ports (not struct fields) prime the
//     inbound channel before the node goroutine starts.

package Wiring

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
)

// specNode mirrors the JSON node shape.
type specNode struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Index        *int           `json:"index,omitempty"`
	Data         *NodeData      `json:"data,omitempty"`
	InitialSlots map[string]int `json:"initialSlots,omitempty"`
}

// NodeData mirrors the JSON data block on a node.
type NodeData struct {
	Init         []int          `json:"init,omitempty"`
	InitialSlots map[string]int `json:"initialSlots,omitempty"`
}

// specEdge mirrors the JSON edge shape.
// Fields tagged wire:"prop,..." are wire props emitted to wire-defs.ts by gen-node-defs.
type specEdge struct {
	Label        string `json:"label"        wire:"prop,optional,tsType:string"`
	ValueLabel   string `json:"valueLabel"   wire:"prop,optional,tsType:string"`
	MidpointOffset float64 `json:"midpointOffset" wire:"prop,optional,tsType:number"`
	ArrowStyle   string `json:"arrowStyle"   wire:"prop,optional,tsType:ArrowStyle"`
	Concurrent   *bool  `json:"concurrent"   wire:"prop,optional,tsType:boolean"`
	Kind         string `json:"kind"         wire:"prop,required,tsType:EdgeKind"`
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

// nodeInitSlots merges top-level and data.initialSlots; top-level wins.
func nodeInitSlots(n specNode) map[string]int {
	m := map[string]int{}
	if n.Data != nil {
		maps.Copy(m, n.Data.InitialSlots)
	}
	maps.Copy(m, n.InitialSlots)
	return m
}

// LoadTopology reads the JSON file at jsonPath and constructs a []S.Node
// equivalent to the generated Wire() function.
func LoadTopology(jsonPath string) ([]S.Node, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("LoadTopology: read %s: %w", jsonPath, err)
	}
	var spec topoSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("LoadTopology: parse %s: %w", jsonPath, err)
	}

	// Validate all kinds up front.
	for _, n := range spec.Nodes {
		if _, ok := Registry[n.Type]; !ok {
			return nil, fmt.Errorf("LoadTopology: node %q: unknown type %q", n.ID, n.Type)
		}
	}

	// Allocate one chan int (buf 1) per edge; keyed by label.
	edgeChan := map[string]chan int{}
	for _, e := range spec.Edges {
		if e.Label == "" {
			return nil, fmt.Errorf("LoadTopology: edge %q→%q has empty label", e.Source, e.Target)
		}
		edgeChan[e.Label] = make(chan int, 1)
	}

	// Build inbound and outbound edge maps.
	// inbound:  target node id → port name → edge label
	// outbound: source node id → port name → []edge label
	inbound := map[string]map[string]string{}
	outbound := map[string]map[string][]string{}
	for _, e := range spec.Edges {
		if inbound[e.Target] == nil {
			inbound[e.Target] = map[string]string{}
		}
		if outbound[e.Source] == nil {
			outbound[e.Source] = map[string][]string{}
		}
		inbound[e.Target][e.TargetHandle] = e.Label
		outbound[e.Source][e.SourceHandle] = append(outbound[e.Source][e.SourceHandle], e.Label)
	}

	// Prime channels for initialSlots entries that map to input ports.
	for _, n := range spec.Nodes {
		slots := nodeInitSlots(n)
		if len(slots) == 0 {
			continue
		}
		bind := Registry[n.Type]
		for _, port := range bind.Ports {
			if port.Dir != PortIn {
				continue
			}
			slotVal, hasSlot := slots[port.Name]
			if !hasSlot {
				continue
			}
			label, ok := inbound[n.ID][port.Name]
			if !ok {
				return nil, fmt.Errorf("LoadTopology: node %q initialSlots: port %q has no incoming edge", n.ID, port.Name)
			}
			edgeChan[label] <- slotVal
		}
	}

	// Build each node.
	nodes := make([]S.Node, 0, len(spec.Nodes))
	for _, n := range spec.Nodes {
		bind := Registry[n.Type]
		pb := newPortBindings()

		for _, port := range bind.Ports {
			switch port.Dir {
			case PortIn:
				label, ok := inbound[n.ID][port.Name]
				if ok {
					pb.SetSingle(port.Name, edgeChan[label])
				}
				// If no inbound edge, pb.In() will allocate a dead-end channel.

			case PortOut:
				labels := outbound[n.ID][port.Name]
				if len(labels) > 0 {
					pb.SetSingle(port.Name, edgeChan[labels[0]])
				}
				// If no outbound edge, pb.Out() allocates a dead-end channel.

			case PortOutMulti:
				labels := outbound[n.ID][port.Name]
				for _, lbl := range labels {
					pb.AppendMulti(port.Name, edgeChan[lbl])
				}
				// If no outbound edges, builder falls back to a dead-end slice.
			}
		}

		idx := 0
		if n.Index != nil {
			idx = *n.Index
		}

		nd, err := bind.Build(idx, n.ID, n.Data, pb)
		if err != nil {
			return nil, fmt.Errorf("LoadTopology: build node %q: %w", n.ID, err)
		}
		nodes = append(nodes, nd)
	}

	return nodes, nil
}
