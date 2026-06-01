// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// Supported message shapes:
//   {"type":"delivered","target":"<node-id>","targetHandle":"<port-name>"}
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
	"maps"
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
	maps.Copy(pos, positions)
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
		arcLen := BezierArcLength(src.X, src.Y, tgt.X, tgt.Y, CurveParamBulgeFactor, CurveParamBezierSampleCount)
		result = append(result, struct {
			edgeId       string
			simLatencyMs float64
		}{edgeId: eid, simLatencyMs: arcLen / PulseSpeedWuPerMs})
	}
	return result
}


type stdinMsg struct {
	Type         string   `json:"type"`
	Target       string   `json:"target"`
	TargetHandle string   `json:"targetHandle"`
	Edges        []string `json:"edges"`
	NodeId       string   `json:"nodeId"`
	X            float64  `json:"x"`
	Y            float64  `json:"y"`
	Z            float64  `json:"z"`
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity used for delivery acks.
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads JSON lines from r, dispatching messages.
// Returns when ctx is done or r reaches EOF.
// Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" and used for delivery acks.
// reg is keyed by edge label and used for fade/node-move operations.
// nmr may be nil; if non-nil, node-move messages update wire geometry.
// tr is retained for future use but no longer used by the node-move handler.
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, reg WireRegistry, nmr *NodeMoveRegistry, tr *T.Trace) {
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
				if msg.Target == "" || msg.TargetHandle == "" {
					continue
				}
				destKey := msg.Target + "." + msg.TargetHandle
				pw, found := slotReg[destKey]
				if !found {
					continue
				}
				pw.NotifyDelivered(ctx) //nolint:errcheck // ErrCanceled is handled by loop exit
			case "deleteEdge":
				if msg.Target == "" || msg.TargetHandle == "" {
					continue
				}
				tr.Breadcrumb("deleteEdge-recv", msg.Target, msg.TargetHandle, "")
				destKey := msg.Target + "." + msg.TargetHandle
				pw, found := slotReg[destKey]
				if !found {
					tr.Breadcrumb("deleteEdge-notfound", msg.Target, msg.TargetHandle, destKey)
					continue
				}
				// "delete" breadcrumb emitted here (not from PacedWire.Delete, which has
				// no Trace reference) carrying the wire's authoritative slot identity.
				tr.Breadcrumb("delete", pw.Target, pw.TargetHandle, "")
				tr.Breadcrumb("deleteEdge-delete", msg.Target, msg.TargetHandle, destKey)
				pw.Delete()
			case "addEdge":
				if msg.Target == "" || msg.TargetHandle == "" {
					continue
				}
				tr.Breadcrumb("addEdge-recv", msg.Target, msg.TargetHandle, "")
				destKey := msg.Target + "." + msg.TargetHandle
				pw, found := slotReg[destKey]
				if !found {
					tr.Breadcrumb("addEdge-notfound", msg.Target, msg.TargetHandle, destKey)
					continue
				}
				tr.Breadcrumb("addEdge-restore", pw.Target, pw.TargetHandle, "")
				pw.Restore()
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
				}
			}
		}
	}
}
