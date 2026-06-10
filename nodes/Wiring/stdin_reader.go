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

// EdgeEndpoints identifies the source and target node IDs (and the port handles)
// for one edge. Handles are needed to recompute the port-to-port arc length.
type EdgeEndpoints struct {
	Source       string
	Target       string
	SourceHandle string
	TargetHandle string
}

// NodeMoveRegistry carries the extra state needed to handle node-move messages.
// It is built by the loader alongside the WireRegistry and passed to RunStdinReader.
type NodeMoveRegistry struct {
	mu    sync.Mutex
	geoms map[string]nodeGeom // nodeId → current geometry (position is mutable)
	// edgeNodes: edgeId → endpoints (immutable after construction)
	edgeNodes map[string]EdgeEndpoints
	// nodeEdges: nodeId → list of edgeIds that touch this node (immutable after construction)
	nodeEdges map[string][]string
	// edgeOut: edgeId → source *Out for that edge, indexed by "source.sourceHandle".
	// Populated by SetEdgeOuts after node construction. Used by node-move to write
	// the affected edge's own per-edge travel-time onto its source Out.
	edgeOut map[string]*Out
	// destWire: destKey ("target.targetHandle") → dest *PacedWire, for recomputing
	// MaxIncomingSimLatencyMs across all edges feeding a port on node-move, and for
	// re-deriving an in-flight bead's remaining travel (ReviseInFlightGeometry).
	destWire map[string]*PacedWire
	// destEdges: destKey → edgeIds feeding that destination port.
	destEdges map[string][]string
	// tr streams the re-derived edge curve (KindGeometry) on node-move so the
	// renderer redraws the wire tube from Go's control points. Set by the loader.
	tr *T.Trace
}

// NewNodeMoveRegistry allocates and initialises a NodeMoveRegistry.
// geoms is a snapshot of per-node geometry (kind/dims/ports/position) at load time.
// edgeNodes maps each edge label to its source/target node IDs and handles.
func NewNodeMoveRegistry(geoms map[string]nodeGeom, edgeNodes map[string]EdgeEndpoints) *NodeMoveRegistry {
	nodeEdges := map[string][]string{}
	destEdges := map[string][]string{}
	for edgeId, ep := range edgeNodes {
		nodeEdges[ep.Source] = append(nodeEdges[ep.Source], edgeId)
		nodeEdges[ep.Target] = append(nodeEdges[ep.Target], edgeId)
		destKey := ep.Target + "." + ep.TargetHandle
		destEdges[destKey] = append(destEdges[destKey], edgeId)
	}
	// Defensively copy geoms.
	g := make(map[string]nodeGeom, len(geoms))
	maps.Copy(g, geoms)
	return &NodeMoveRegistry{
		geoms:     g,
		edgeNodes: edgeNodes,
		nodeEdges: nodeEdges,
		edgeOut:   map[string]*Out{},
		destWire:  map[string]*PacedWire{},
		destEdges: destEdges,
	}
}

// SetEdgeOuts wires the per-edge source Outs (indexed by "source.sourceHandle"
// in outSink) and the destination wires (slotReg, keyed by "target.targetHandle")
// into the registry so node-move can update per-edge travel-time and recompute
// each affected port's MaxIncomingSimLatencyMs. Call once after node construction.
func (nmr *NodeMoveRegistry) SetEdgeOuts(outSink map[string]*Out, slotReg SlotRegistry) {
	nmr.mu.Lock()
	defer nmr.mu.Unlock()
	for edgeId, ep := range nmr.edgeNodes {
		if o, ok := outSink[ep.Source+"."+ep.SourceHandle]; ok {
			nmr.edgeOut[edgeId] = o
		}
		destKey := ep.Target + "." + ep.TargetHandle
		if pw, ok := slotReg[destKey]; ok {
			nmr.destWire[destKey] = pw
		}
	}
}

// EdgeOut returns the source *Out bound to the given edge label, or nil if the
// edge is unknown. Exported so out-of-package callers (e.g. the headless cascade
// verifier in package main) can read an edge's per-edge in-flight time
// (Out.SimLatencyMs) from the loaded geometry.
func (nmr *NodeMoveRegistry) EdgeOut(edgeID string) *Out {
	nmr.mu.Lock()
	defer nmr.mu.Unlock()
	return nmr.edgeOut[edgeID]
}

// SetTrace injects the trace stream used to emit re-derived edge curves on
// node-move (KindGeometry). Call once after construction (loader).
func (nmr *NodeMoveRegistry) SetTrace(tr *T.Trace) {
	nmr.mu.Lock()
	nmr.tr = tr
	nmr.mu.Unlock()
}

// applyNodeMove moves nodeId to (x, y, z) and re-derives geometry for every edge
// that touches it (MODEL.md: Go is the authoritative holder of node positions +
// per-edge curve control points). For each affected edge it:
//   - recomputes the port-to-port curve (P0/P1/P2) and arc length from the moved
//     geometry, writing arc/latency AND control points onto the source Out, so the
//     next placement and the geometry stream use the new curve;
//   - re-derives any in-flight bead's remaining travel from the NEW arc and the
//     distance already covered (ReviseInFlightGeometry on the dest wire);
//   - streams the new curve (KindGeometry) so the renderer redraws the wire tube.
//
// Each destination port reached by an affected edge then has its
// MaxIncomingSimLatencyMs recomputed as max over ALL edges feeding it (not just the
// moved ones). No-op if nodeId is unknown.
func (nmr *NodeMoveRegistry) applyNodeMove(nodeId string, x, y, z float64) {
	nmr.mu.Lock()
	defer nmr.mu.Unlock()
	g, ok := nmr.geoms[nodeId]
	if !ok {
		return
	}
	g.Pos = vec3{X: x, Y: y, Z: z}
	nmr.geoms[nodeId] = g

	edgeIds := nmr.nodeEdges[nodeId]
	if len(edgeIds) == 0 {
		return
	}

	// curveOf computes the current port-to-port curve (control points) for an edge
	// from the live geometry; arc length is integrated from the same curve.
	curveOf := func(eid string) (edgeCurve, float64) {
		ep := nmr.edgeNodes[eid]
		curve := curveBetweenPorts(
			nmr.geoms[ep.Source], ep.SourceHandle,
			nmr.geoms[ep.Target], ep.TargetHandle,
		)
		arc := arcLengthBetweenPorts(
			nmr.geoms[ep.Source], ep.SourceHandle,
			nmr.geoms[ep.Target], ep.TargetHandle,
		)
		return curve, arc
	}

	// 1. Re-derive each affected edge: source Out curve + travel-time, in-flight
	//    bead remaining travel, and the streamed geometry. Track the distinct
	//    destination ports they feed for the aggregate recompute below.
	touchedDest := map[string]bool{}
	for _, eid := range edgeIds {
		curve, arc := curveOf(eid)
		lat := arc / PulseSpeedWuPerMs
		if o := nmr.edgeOut[eid]; o != nil {
			o.ArcLength = arc
			o.SimLatencyMs = lat
			o.P0, o.P1, o.P2 = curve.P0, curve.P1, curve.P2
		}
		ep := nmr.edgeNodes[eid]
		// Re-derive an in-flight bead on this edge from the new arc + curve. The
		// dest wire owns the bead; ReviseInFlightGeometry is a no-op if none in flight.
		if pw := nmr.destWire[ep.Target+"."+ep.TargetHandle]; pw != nil {
			pw.ReviseInFlightGeometry(arc, curve)
		}
		// Stream the new curve so the renderer redraws the tube from Go's points.
		nmr.tr.Geometry(eid,
			curve.P0.X, curve.P0.Y, curve.P0.Z,
			curve.P1.X, curve.P1.Y, curve.P1.Z,
			curve.P2.X, curve.P2.Y, curve.P2.Z)
		touchedDest[ep.Target+"."+ep.TargetHandle] = true
	}

	// 2. Recompute each touched dest port's MaxIncomingSimLatencyMs over ALL its
	//    feeding edges (some may be unaffected by this move).
	for destKey := range touchedDest {
		pw := nmr.destWire[destKey]
		if pw == nil {
			continue
		}
		var maxLat float64
		for _, eid := range nmr.destEdges[destKey] {
			if _, arc := curveOf(eid); arc/PulseSpeedWuPerMs > maxLat {
				maxLat = arc / PulseSpeedWuPerMs
			}
		}
		pw.mu.Lock()
		pw.MaxIncomingSimLatencyMs = maxLat
		pw.mu.Unlock()
	}
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
				// Phase 1: Go's clock times delivery now (see PacedWire
				// startDeliveryLocked); the TS "delivered" message no longer
				// triggers it. We still parse and discard the message so the seam
				// stays message-kind-parity clean (full removal is Phase 5).
				continue
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
				nmr.applyNodeMove(msg.NodeId, msg.X, msg.Y, msg.Z)
			}
		}
	}
}
