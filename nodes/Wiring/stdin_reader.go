// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// The editor→Go bridge carries two top-level message kinds:
//
//  1. Geometry-CRUD edits (type=="edit") — op discriminates the operation:
//     {"type":"edit","op":"create","target":"<node-id>","targetHandle":"<port>"}
//     {"type":"edit","op":"delete","target":"<node-id>","targetHandle":"<port>"}
//     {"type":"edit","op":"update","nodeId":"<id>","x":<f64>,"y":<f64>,"z":<f64>}
//     {"type":"edit","op":"fade","edges":["<edge-id>", ...]}
//
//  2. Play/pause control (type=="play" / type=="pause") — routes directly to the
//     clock's global gate (Halt/Resume). The process starts halted; the first
//     "play" message resumes bead delivery. "pause" re-halts.
//
// Go owns the clock and delivery; nothing on this seam triggers delivery or
// carries animation internals.
//
// One goroutine; cancellable via context. On EOF or context cancel, exits
// cleanly. Unknown message types and ops are silently ignored (forward-compat).

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


// stdinMsg is the single editor→Go bridge shape. type is always "edit"; op
// discriminates the CRUD/animation operation. The remaining fields are the union
// of every op's payload (only the fields for the active op are populated).
type stdinMsg struct {
	Type         string   `json:"type"`
	Op           string   `json:"op"`
	Target       string   `json:"target"`
	TargetHandle string   `json:"targetHandle"`
	Edges        []string `json:"edges"`
	NodeId       string   `json:"nodeId"`
	X            float64  `json:"x"`
	Y            float64  `json:"y"`
	Z            float64  `json:"z"`
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity used to resolve an edit's create/delete op
// to the wire owned by that destination port.
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads JSON lines from r, dispatching geometry-CRUD "edit"
// messages and play/pause clock-gate control messages. Returns when ctx is done
// or r reaches EOF. Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" and resolves create/delete ops to the
// destination port's wire. reg is keyed by edge label and drives the fade op.
// nmr may be nil; if non-nil, update (node-move) ops re-derive wire geometry.
// tr emits control breadcrumbs for the edit ops.
// clk may be nil; if non-nil, "play" calls clk.Resume() and "pause" calls clk.Halt().
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, reg WireRegistry, nmr *NodeMoveRegistry, tr *T.Trace, clk Clock) {
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
			// Two top-level bridge kinds:
			//   "edit"  — geometry-CRUD; op discriminates the operation (internal axis).
			//   "play"  — resume the clock's global gate (bead delivery starts).
			//   "pause" — halt the clock's global gate (bead delivery freezes).
			switch msg.Type {
			case "edit":
				applyEdit(msg, slotReg, reg, nmr, tr)
			case "play":
				if clk != nil {
					clk.Resume()
				}
			case "pause":
				if clk != nil {
					clk.Halt()
				}
			}
		}
	}
}

// applyEdit dispatches one geometry-CRUD/animation edit by its op. It is the
// internal op-axis of the single "edit" bridge shape; the op values are matched by
// value (==) rather than a case-literal switch so they stay invisible to the
// message-kind-parity guard, which fences only top-level msg.Type kinds.
//
// Ops:
//   - create: un-silence the destination port's wire (edge re-added) — Restore.
//   - delete: silence the wire AND cancel any in-flight bead's clock-delivery,
//     echoing pulse-cancelled (PacedWire.Delete owns both, atomically).
//   - update: re-derive the moved node's edge geometry (node-move) on the registry.
//   - fade:   set the per-wire faded-flag set wholesale across all wires.
//
// Unknown ops are ignored (forward-compat).
func applyEdit(msg stdinMsg, slotReg SlotRegistry, reg WireRegistry, nmr *NodeMoveRegistry, tr *T.Trace) {
	switch {
	case msg.Op == "create":
		if msg.Target == "" || msg.TargetHandle == "" {
			return
		}
		tr.Breadcrumb("edit-create-recv", msg.Target, msg.TargetHandle, "")
		destKey := msg.Target + "." + msg.TargetHandle
		pw, found := slotReg[destKey]
		if !found {
			tr.Breadcrumb("edit-create-notfound", msg.Target, msg.TargetHandle, destKey)
			return
		}
		tr.Breadcrumb("edit-create-restore", pw.Target, pw.TargetHandle, "")
		pw.Restore()
	case msg.Op == "delete":
		if msg.Target == "" || msg.TargetHandle == "" {
			return
		}
		tr.Breadcrumb("edit-delete-recv", msg.Target, msg.TargetHandle, "")
		destKey := msg.Target + "." + msg.TargetHandle
		pw, found := slotReg[destKey]
		if !found {
			tr.Breadcrumb("edit-delete-notfound", msg.Target, msg.TargetHandle, destKey)
			return
		}
		// "delete" breadcrumb emitted here (PacedWire.Delete has no Trace reference)
		// carrying the wire's authoritative slot identity. Delete cancels any
		// in-flight bead's clock-delivery and echoes pulse-cancelled atomically.
		tr.Breadcrumb("delete", pw.Target, pw.TargetHandle, "")
		tr.Breadcrumb("edit-delete-delete", msg.Target, msg.TargetHandle, destKey)
		pw.Delete()
	case msg.Op == "update":
		if nmr == nil || msg.NodeId == "" {
			return
		}
		nmr.applyNodeMove(msg.NodeId, msg.X, msg.Y, msg.Z)
	case msg.Op == "fade":
		// Build a set of faded edge ids for O(1) lookup, then apply wholesale:
		// set every wire's faded flag to its membership in the set.
		faded := make(map[string]bool, len(msg.Edges))
		for _, id := range msg.Edges {
			faded[id] = true
		}
		reg.ForEach(func(id string, pw *PacedWire) {
			pw.SetFaded(faded[id])
		})
	}
}
