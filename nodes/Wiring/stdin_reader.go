// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// Supported message shapes:
//   {"type":"delivered","edge":"<edge-label>"}
//   {"type":"fade","edges":["<edge-id>", ...]}
//   {"type":"node-move","nodeId":"<id>","x":<f64>,"y":<f64>,"z":<f64>}
//
// One goroutine; cancellable via context. On EOF or context cancel, exits
// cleanly. Unknown message types are silently ignored (forward-compat).

package Wiring

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"math"
	"sync"

	T "github.com/dtauraso/wirefold/Trace"
)

// NodePosition holds the 3-D position of a node (world units, same as specPosition).
type NodePosition struct {
	X, Y, Z float64
}

// EdgeEndpoints identifies the source and target node IDs for one edge.
type EdgeEndpoints struct {
	Source string
	Target string
}

// NodeMoveRegistry carries the extra state needed to handle node-move messages.
// It is built by the loader alongside the WireRegistry and passed to RunStdinReader.
type NodeMoveRegistry struct {
	mu        sync.Mutex
	positions map[string]NodePosition // nodeId → current position (mutable)
	// edgeNodes: edgeId → endpoints (immutable after construction)
	edgeNodes map[string]EdgeEndpoints
	// nodeEdges: nodeId → list of edgeIds that touch this node (immutable after construction)
	nodeEdges map[string][]string
}

// NewNodeMoveRegistry allocates and initialises a NodeMoveRegistry.
// positions is a snapshot of per-node positions at load time.
// edgeNodes maps each edge label to its source/target node IDs.
func NewNodeMoveRegistry(positions map[string]NodePosition, edgeNodes map[string]EdgeEndpoints) *NodeMoveRegistry {
	nodeEdges := map[string][]string{}
	for edgeId, ep := range edgeNodes {
		nodeEdges[ep.Source] = append(nodeEdges[ep.Source], edgeId)
		nodeEdges[ep.Target] = append(nodeEdges[ep.Target], edgeId)
	}
	// Defensively copy positions.
	pos := make(map[string]NodePosition, len(positions))
	for k, v := range positions {
		pos[k] = v
	}
	return &NodeMoveRegistry{
		positions: pos,
		edgeNodes: edgeNodes,
		nodeEdges: nodeEdges,
	}
}

// updateNodeAndGetAffected moves nodeId to (x, y, z) and returns a slice of
// (edgeId, newSimLatencyMs) pairs for all edges that touch this node.
// Returns nil if nodeId is unknown.
func (nmr *NodeMoveRegistry) updateNodeAndGetAffected(nodeId string, x, y, z float64) []struct {
	edgeId       string
	simLatencyMs float64
} {
	nmr.mu.Lock()
	defer nmr.mu.Unlock()
	if _, ok := nmr.positions[nodeId]; !ok {
		return nil
	}
	nmr.positions[nodeId] = NodePosition{X: x, Y: y, Z: z}

	edgeIds := nmr.nodeEdges[nodeId]
	if len(edgeIds) == 0 {
		return nil
	}
	result := make([]struct {
		edgeId       string
		simLatencyMs float64
	}, 0, len(edgeIds))
	for _, eid := range edgeIds {
		ep := nmr.edgeNodes[eid]
		src := nmr.positions[ep.Source]
		tgt := nmr.positions[ep.Target]
		arcLen := arcLengthBetween3(src, tgt)
		result = append(result, struct {
			edgeId       string
			simLatencyMs float64
		}{edgeId: eid, simLatencyMs: arcLen / PulseSpeedWuPerMs})
	}
	return result
}

// arcLengthBetween3 computes the straight-line distance between two NodePositions.
// Returns at least minArcLength so SimLatencyMs is never zero.
func arcLengthBetween3(a, b NodePosition) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y
	dz := b.Z - a.Z
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if d < minArcLength {
		return minArcLength
	}
	return d
}

type stdinMsg struct {
	Type   string   `json:"type"`
	Edge   string   `json:"edge"`
	Edges  []string `json:"edges"`
	NodeId string   `json:"nodeId"`
	X      float64  `json:"x"`
	Y      float64  `json:"y"`
	Z      float64  `json:"z"`
}

// RunStdinReader reads JSON lines from r, dispatching messages
// to the WireRegistry. Returns when ctx is done or r reaches EOF.
// Call in a goroutine alongside the node run loop.
//
// nmr may be nil; if non-nil, node-move messages update wire geometry
// and emit latency-changed trace events via tr.
// tr may be nil; if nil, latency-changed events are not emitted.
func RunStdinReader(ctx context.Context, r io.Reader, reg WireRegistry, nmr *NodeMoveRegistry, tr *T.Trace) {
	sc := bufio.NewScanner(r)
	done := ctx.Done()
	lineCh := make(chan string, 8)
	go func() {
		for sc.Scan() {
			lineCh <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			// Scan encountered an error reading from r; log and continue.
			// The channel close will unblock the main select loop.
		}
		close(lineCh)
	}()
	for {
		select {
		case <-done:
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			var msg stdinMsg
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "delivered":
				if msg.Edge == "" {
					continue
				}
				pw, found := reg[msg.Edge]
				if !found {
					continue
				}
				pw.NotifyDelivered()
			case "fade":
				// Build a set of faded edge ids for O(1) lookup.
				faded := make(map[string]bool, len(msg.Edges))
				for _, id := range msg.Edges {
					faded[id] = true
				}
				// Apply wholesale: set every wire's faded flag.
				reg.ForEach(func(id string, pw *PacedWire) {
					pw.SetFaded(faded[id])
				})
			case "node-move":
				if nmr == nil || msg.NodeId == "" {
					continue
				}
				affected := nmr.updateNodeAndGetAffected(msg.NodeId, msg.X, msg.Y, msg.Z)
				for _, a := range affected {
					// Update the wire geometry in the registry.
					if pw, ok := reg[a.edgeId]; ok {
						pw.mu.Lock()
						pw.SimLatencyMs = a.simLatencyMs
						pw.ArcLength = a.simLatencyMs * PulseSpeedWuPerMs
						pw.mu.Unlock()
					}
					// Emit latency-changed so TS can adjust any in-flight bead.
					if tr != nil {
						tr.LatencyChanged(a.edgeId, a.simLatencyMs)
					}
				}
			}
		}
	}
}
