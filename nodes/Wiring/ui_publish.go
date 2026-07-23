// ui_publish.go — publishes MoveDispatch's OWN selection/hover/abc-drag UI state as the
// same lock-free atomic maps Buffer.SnapshotState used to be the sole publisher of
// (NodeUIStateFor/PortHoveredFor/IsEdgeSelected — see nodeMover/edgeMover's uiStateFor/
// portHoveredFor/edgeSelected fields, wired via SetNodeStreams/SetEdgeStreams). This is
// step 2 of retiring the accumulator (step 1, ea6167f9, moved the row-identity tables):
// the goroutine that already SETS the intent (gesture.go's applySelect/setHover,
// quantized_move.go's neighborSetCRequantize AbcDrag path — all on the single
// gesture/MoveDispatch stdin-reader goroutine) now publishes directly here instead of
// round-tripping the STATE through Trace → the drain goroutine → SnapshotState.
//
// tr.Select/tr.Hover/tr.AbcDrag/tr.AbcDragReset still fire alongside every mutation here —
// they now serve ONLY the .probe EVENT LOG (T.Event recording), not state. Buffer.
// SnapshotState keeps its OWN copy of this state (setSelected/setHovered/KindAbcDrag),
// unchanged, to feed the fd-3 FALLBACK packer when no dedicated node/edge stream is active.
package Wiring

import "sync/atomic"

// uiNodeStatePub is one node's published UI-state snapshot — the MoveDispatch analogue
// of Buffer.SnapshotState's nodeUIStateSnap (same fields, same meaning).
type uiNodeStatePub struct {
	selected, hovered, latchedSel, gotDragMsg, kindID uint8
	dragDeltaA, dragDeltaB, dragDeltaC                int32
}

// uiPublishState groups the three atomically-published maps + the abc-drag count.
// Zero value is safe to read (Load returns nil, every accessor below treats that as
// "nothing published yet").
type uiPublishState struct {
	nodeStates   atomic.Pointer[map[string]uiNodeStatePub]
	portHover    atomic.Pointer[map[string]uint8]
	selectedEdge atomic.Pointer[map[string]bool]
	abcCount     atomic.Uint32
}

// uiPortHoverKey builds the portHover map key for (node, port, isInput) — same shape as
// Buffer.SnapshotState's portHoverKey (kept as a private duplicate so this package stays
// Buffer-independent).
func uiPortHoverKey(node, port string, isInput bool) string {
	iv := "0"
	if isInput {
		iv = "1"
	}
	return node + "\x00" + port + "\x00" + iv
}

// republishUIState rebuilds and atomically republishes md.uiPub's three maps from
// MoveDispatch's own selection/hover/abc-drag bookkeeping (md.sel.selected/selectedEdge/
// hoverNode/hoverPort/hoverInput, md.latchedNode, md.abcRecipients/abcDeltas). Called
// after every mutation of that state (setSelectionUI, setHoverUI, recordAbcDrag,
// resetAbcDrag). Takes md.uiMu for the read: selection/hover are otherwise mutated only
// by the single gesture/MoveDispatch goroutine, but abcRecipients/abcDeltas are mutated
// from every recipient node's OWN nodeMover goroutine (see uiMu's doc comment,
// node_move.go), so this read must synchronize with those writers too.
func (md *MoveDispatch) republishUIState() {
	md.uiMu.Lock()
	selected, selectedEdge := md.sel.selected, md.sel.selectedEdge
	hoverNode, hoverPort, hoverInput := md.sel.hoverNode, md.sel.hoverPort, md.sel.hoverInput
	latched := md.latchedNode
	recipients := make(map[string]bool, len(md.abcRecipients))
	for id := range md.abcRecipients {
		recipients[id] = true
	}
	deltas := make(map[string][3]int32, len(md.abcDeltas))
	for id, d := range md.abcDeltas {
		deltas[id] = d
	}
	md.uiMu.Unlock()

	nodes := make(map[string]uiNodeStatePub, len(md.nodeRowTable))
	ph := map[string]uint8{}
	for _, id := range md.nodeRowTable {
		st := uiNodeStatePub{kindID: md.kindIDByNode[id]}
		if id != "" && id == selected {
			st.selected = 1
		}
		if id != "" && id == latched {
			st.latchedSel = 1
		}
		if hoverPort == "" && id != "" && id == hoverNode {
			st.hovered = 1
		}
		if recipients[id] {
			st.gotDragMsg = 1
			d := deltas[id]
			st.dragDeltaA, st.dragDeltaB, st.dragDeltaC = d[0], d[1], d[2]
		}
		nodes[id] = st
	}
	if hoverPort != "" && hoverNode != "" {
		ph[uiPortHoverKey(hoverNode, hoverPort, hoverInput)] = 1
	}
	edges := map[string]bool{}
	if selectedEdge != "" {
		edges[selectedEdge] = true
	}
	md.uiPub.nodeStates.Store(&nodes)
	md.uiPub.portHover.Store(&ph)
	md.uiPub.selectedEdge.Store(&edges)
}

// setSelectionUI sets the Go-owned selection (node XOR edge, exclusive — mirrors
// Buffer.SnapshotState.setSelected/setSelectedEdge's exclusivity) under uiMu, moving
// latchedNode to a newly-selected node (left untouched on a deselect), then republishes.
// Called only from the gesture/MoveDispatch goroutine (applySelect), but still takes
// uiMu since republishUIState (called from any node's own goroutine, via an in-flight
// abc-drag) reads the same fields.
func (md *MoveDispatch) setSelectionUI(node, edge string) {
	md.uiMu.Lock()
	md.sel.selected = node
	md.sel.selectedEdge = edge
	if node != "" {
		md.latchedNode = node
	}
	md.uiMu.Unlock()
	md.republishUIState()
}

// setHoverUI sets the Go-owned hover state under uiMu, then republishes. Called only
// from the gesture goroutine (setHover's dedupe check reads md.sel.hoverNode/Port/Input
// directly — safe without uiMu since only this same goroutine ever writes them — see
// gesture.go's setHover).
func (md *MoveDispatch) setHoverUI(node, port string, isInput bool) {
	md.uiMu.Lock()
	md.sel.hoverNode, md.sel.hoverPort, md.sel.hoverInput = node, port, isInput
	md.uiMu.Unlock()
	md.republishUIState()
}

// recordAbcDrag marks nodeID as a recipient of this drag's time.abc-drag message with
// its delta, increments the cumulative abc-drag count, and republishes. Called from
// nodeID's OWN nodeMover goroutine (quantized_move.go's neighborSetCRequantize) —
// concurrently with every OTHER recipient's own call, hence uiMu.
func (md *MoveDispatch) recordAbcDrag(nodeID string, deltaA, deltaB, deltaC int) {
	md.uiMu.Lock()
	if md.abcRecipients == nil {
		md.abcRecipients = map[string]bool{}
	}
	if md.abcDeltas == nil {
		md.abcDeltas = map[string][3]int32{}
	}
	md.abcRecipients[nodeID] = true
	md.abcDeltas[nodeID] = [3]int32{int32(deltaA), int32(deltaB), int32(deltaC)}
	md.uiMu.Unlock()
	md.uiPub.abcCount.Add(1)
	md.republishUIState()
}

// resetAbcDrag re-scopes the recipient SET (and per-node deltas) to the drag about to
// start, leaving the cumulative count alone (mirrors Buffer.SnapshotState's
// KindAbcDragReset handling). Called from the gesture goroutine at the pending→dragging
// transition (gesture.go).
func (md *MoveDispatch) resetAbcDrag() {
	md.uiMu.Lock()
	md.abcRecipients = map[string]bool{}
	md.abcDeltas = map[string][3]int32{}
	md.uiMu.Unlock()
	md.republishUIState()
}

// NodeUIStateFor resolves id's current published UI-state snapshot — MoveDispatch's own
// copy, same signature/shape as Buffer.SnapshotState.NodeUIStateFor (which nodeMover was
// wired to before this migration). Safe to call from a goroutine other than the one that
// writes it (reads an immutable atomically-published map). ok=false when id is not a
// registered node (nodeRowTable is built once, before any mover launches — see its doc
// comment — so this is only false for an unknown id).
func (md *MoveDispatch) NodeUIStateFor(id string) (selected, hovered, latchedSel, gotDragMsg, kindID uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, ok bool) {
	m := md.uiPub.nodeStates.Load()
	if m == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, false
	}
	st, found := (*m)[id]
	if !found {
		return 0, 0, 0, 0, 0, 0, 0, 0, false
	}
	return st.selected, st.hovered, st.latchedSel, st.gotDragMsg, st.kindID,
		st.dragDeltaA, st.dragDeltaB, st.dragDeltaC, true
}

// PortHoveredFor reports whether (node, port, isInput) is CURRENTLY hovered, via
// MoveDispatch's own published portHover map — same signature/shape as Buffer.
// SnapshotState.PortHoveredFor.
func (md *MoveDispatch) PortHoveredFor(node, port string, isInput bool) uint8 {
	m := md.uiPub.portHover.Load()
	if m == nil {
		return 0
	}
	return (*m)[uiPortHoverKey(node, port, isInput)]
}

// IsEdgeSelected reports whether label is the CURRENT click-selected edge, via
// MoveDispatch's own published selectedEdge map — same signature/shape as Buffer.
// SnapshotState.IsEdgeSelected.
func (md *MoveDispatch) IsEdgeSelected(label string) bool {
	m := md.uiPub.selectedEdge.Load()
	if m == nil {
		return false
	}
	return (*m)[label]
}

// AbcDragCount returns the current cumulative count of time.abc-drag events observed —
// MoveDispatch's own copy of the affirmation counter the VIEW frame's Overlay block
// carries (Buffer/layout.go's AbcDragCount column). Read by the view-frame writer
// (Buffer.SnapshotState.buildViewFrame, via the abcDragCountFor func injected by
// SetAbcDragCountSource) instead of the accumulator's own overlay.AbcDragCount field.
func (md *MoveDispatch) AbcDragCount() uint32 {
	return md.uiPub.abcCount.Load()
}
