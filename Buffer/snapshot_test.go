// Buffer/snapshot_test.go — ingest-side tests for SnapshotState: Update() event
// handling, on* handlers (hover, layout-link, selection, interior beads, node-kind
// id), and transient-flag lifecycle. Byte-layout / block-writing / framing tests
// live in pack_test.go (readF32/readU32/readI32 helpers are defined there and
// used from both files — same package). Split mirrors the production snapshot.go
// (ingest) / pack.go (pack) split.

package Buffer

import (
	"bytes"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// The port-row hit-test resolution table (formerly SnapshotState.LookupPortRow) moved off
// SnapshotState onto MoveDispatch — built once at load from the seed order, not discovered
// from geometry events. See nodes/Wiring/node_move_row_table_test.go for its test.

// TestHoverColumn verifies that KindHover marks the Hovered column on the hovered node OR
// port (exclusive, cleared on others), and that an empty hover clears all hover flags.
func TestHoverColumn(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}}})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	nodeHovered := func(row int) byte {
		nodeFrame := s.BuildNodeFrame()
		nodeOff := BufNodeFrameHeaderSize
		return nodeFrame[nodeOff+row*BufNodeStride+BufNodeColHovered]
	}
	portHovered := func() byte {
		nodeFrame := s.BuildNodeFrame()
		nodeCount := readU32(nodeFrame, 4)
		portOff := BufNodeFrameHeaderSize +
			int(nodeCount)*BufNodeStride +
			int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride
		return nodeFrame[portOff+BufPortColHovered] // port row 0 = n0/in
	}

	// Hover node n0 → node row 0 Hovered=1, node row 1 = 0.
	s.Update(T.Event{Kind: T.KindHover, Node: "n0"})
	if nodeHovered(0) != 1 || nodeHovered(1) != 0 {
		t.Fatalf("node hover: row0=%d row1=%d, want 1,0", nodeHovered(0), nodeHovered(1))
	}
	// Hover the port n0/in → port Hovered=1, node hover cleared.
	s.Update(T.Event{Kind: T.KindHover, Node: "n0", Port: "in", Value: 1})
	if portHovered() != 1 {
		t.Fatalf("port hover: got %d, want 1", portHovered())
	}
	if nodeHovered(0) != 0 {
		t.Fatalf("port hover should clear node hover: node0=%d, want 0", nodeHovered(0))
	}
	// Empty hover clears everything.
	s.Update(T.Event{Kind: T.KindHover, Node: ""})
	if nodeHovered(0) != 0 || portHovered() != 0 {
		t.Fatalf("cleared hover: node0=%d port=%d, want 0,0", nodeHovered(0), portHovered())
	}
}

// TestLayoutLinkUnresolvableEndpointFiltered verifies that a layout-link pair whose endpoint
// node id never resolves to a live buffer node row (nodeRowIndex == -1) is dropped from the
// LayoutLink block entirely — it must not be packed with SrcNodeRow/DstNodeRow == -1, which
// would crash the webview reader. A control pair whose both endpoints resolve is kept.
func TestLayoutLinkUnresolvableEndpointFiltered(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	// Control: both endpoints resolve.
	s.Update(T.Event{Kind: T.KindLayoutLink, Node: "n0", Target: "n1"})
	// Broken: "ghost" was never registered as a node, so nodeRowIndex("ghost") == -1.
	s.Update(T.Event{Kind: T.KindLayoutLink, Node: "n0", Target: "ghost"})

	snap := s.BuildSnapshot()

	layoutLinkCount := int(readU32(snap, 4))

	if layoutLinkCount != 1 {
		t.Fatalf("layoutLinkCount: got %d, want 1 (unresolvable pair must be dropped)", layoutLinkCount)
	}

	llOff := BufHeaderSize

	for i := 0; i < layoutLinkCount; i++ {
		base := llOff + i*BufLayoutLinkStride
		src := readI32(snap, base+BufLayoutLinkColSrcNodeRow)
		dst := readI32(snap, base+BufLayoutLinkColDstNodeRow)
		if src < 0 || dst < 0 {
			t.Errorf("layout link row %d: SrcNodeRow=%d DstNodeRow=%d, want both >= 0", i, src, dst)
		}
	}
}

// TestTransientFlagsCleared verifies that the per-tick causal events accumulated
// in pendingEvents are reset after each snapshot emit (they must not bleed into
// subsequent ticks' VIEW frame EVENTS section — the fd-3 scene frame no longer carries
// an EVENT block at all; memory/feedback_no_single_writer_bridge.md).
func TestTransientFlagsCleared(t *testing.T) {
	s := NewSnapshotState(nil)

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1",
		NX: 0, NY: 0, NZ: 0, Radius: 1, SphereR: 0.5,
	})

	// Fire on n1 and emit (via position).
	s.Update(T.Event{Kind: T.KindFire, Node: "n1"})
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "n1", Port: "out",
		X: 0, Y: 0, Z: 0, Value: 0, F: 0.5, Bead: 1,
	})

	// After the position-triggered snapshot, transients should be cleared.
	if len(s.pendingEvents) != 0 {
		t.Errorf("pendingEvents not cleared after snapshot: got %d, want 0", len(s.pendingEvents))
	}
	view := s.buildViewFrame()
	eventsOff := BufViewFrameHeaderSize + BufCameraStride + BufOverlayStride + BufSceneStride
	if eventCount := readU32(view, eventsOff); eventCount != 0 {
		t.Errorf("view frame eventCount not cleared: got %d, want 0", eventCount)
	}
}

// TestInteriorBeadReachesSnapshot verifies that KindNodeBead events land in the
// Interior block: slot = row*2 + col, present/value/offset are packed at the right
// columns, empty slots stay present=0, and a present=false event clears a popped slot.
func TestInteriorBeadReachesSnapshot(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	// (row 0, col 0) → slot 0: present value 1 at local offset (2,-3,4).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: true, Value: 1, X: 2, Y: -3, Z: 4})
	// (row 1, col 0) → slot 2: present value 0 (a valid black bead) at (1,1,1).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 1, Col: 0, Present: true, Value: 0, X: 1, Y: 1, Z: 1})

	nodeFrame := s.BuildNodeFrame()
	// Interior block starts right after the single node row (no beads). The
	// Node/Interior blocks now live in the node-owner-group frame (see BufBlockTagNode).
	interiorOff := BufNodeFrameHeaderSize + 1*BufNodeStride
	slot := func(i int) int { return interiorOff + i*BufInteriorStride }

	if nodeFrame[slot(0)+BufInteriorColPresent] != 1 {
		t.Errorf("slot0.Present: got %d, want 1", nodeFrame[slot(0)+BufInteriorColPresent])
	}
	if v := readI32(nodeFrame, slot(0)+BufInteriorColValue); v != 1 {
		t.Errorf("slot0.Value: got %d, want 1", v)
	}
	if x := readF32(nodeFrame, slot(0)+BufInteriorColOX); x != 2 {
		t.Errorf("slot0.OX: got %v, want 2", x)
	}
	if y := readF32(nodeFrame, slot(0)+BufInteriorColOY); y != -3 {
		t.Errorf("slot0.OY: got %v, want -3", y)
	}
	if z := readF32(nodeFrame, slot(0)+BufInteriorColOZ); z != 4 {
		t.Errorf("slot0.OZ: got %v, want 4", z)
	}
	// Slot 1 (row 0, col 1) was never emitted → absent.
	if nodeFrame[slot(1)+BufInteriorColPresent] != 0 {
		t.Errorf("slot1.Present: got %d, want 0 (never emitted)", nodeFrame[slot(1)+BufInteriorColPresent])
	}
	// Slot 2 (row 1, col 0): present, value 0.
	if nodeFrame[slot(2)+BufInteriorColPresent] != 1 {
		t.Errorf("slot2.Present: got %d, want 1", nodeFrame[slot(2)+BufInteriorColPresent])
	}
	if v := readI32(nodeFrame, slot(2)+BufInteriorColValue); v != 0 {
		t.Errorf("slot2.Value: got %d, want 0", v)
	}

	// A present=false event on slot 0 clears it (popped bead disappears).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: false, Value: 0, X: 2, Y: -3, Z: 4})
	nodeFrame = s.BuildNodeFrame()
	if nodeFrame[slot(0)+BufInteriorColPresent] != 0 {
		t.Errorf("slot0.Present after pop: got %d, want 0", nodeFrame[slot(0)+BufInteriorColPresent])
	}
}

// TestSelectionPersistsAndIsExclusive verifies KindSelect marks exactly one node's
// Selected column, that it PERSISTS across snapshots (unlike transient event flags),
// and that a later select on another node moves the flag (exclusive), while node=""
// clears it entirely.
func TestSelectionPersistsAndIsExclusive(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})

	// The Node block now lives in the node-owner-group frame (see BufBlockTagNode).
	n1 := BufNodeFrameHeaderSize
	n2 := n1 + BufNodeStride

	// Select n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 1 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after select n1: n1.Selected=%d n2.Selected=%d want 1,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Persists: a fresh snapshot with no further select keeps n1 selected.
	snap = s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 1 {
		t.Fatalf("selection did not persist: n1.Selected=%d want 1", snap[n1+BufNodeColSelected])
	}

	// Exclusive: selecting n2 clears n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 1 {
		t.Fatalf("after select n2: n1.Selected=%d n2.Selected=%d want 0,1", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Clear: node="" deselects all.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after clear: n1.Selected=%d n2.Selected=%d want 0,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}
}

// TestLatchedSelPersistsThroughDeselect verifies the Go-owned LatchedSel column: it moves
// with Selected when a DIFFERENT node is selected, but — unlike Selected — does NOT clear
// when the node is deselected (Node=""). This is the replacement for the old TS-owned
// `latchedSel` React state in NavGuides.tsx (see Buffer/layout.go LatchedSel).
func TestLatchedSelPersistsThroughDeselect(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})

	// The Node block now lives in the node-owner-group frame (see BufBlockTagNode).
	n1 := BufNodeFrameHeaderSize
	n2 := n1 + BufNodeStride

	// Select n1: both Selected and LatchedSel move to n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 1 || snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after select n1: Selected=%d LatchedSel=%d want 1,1", snap[n1+BufNodeColSelected], snap[n1+BufNodeColLatchedSel])
	}

	// Deselect (Node=""): Selected clears, but LatchedSel STAYS on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildNodeFrame()
	if snap[n1+BufNodeColSelected] != 0 {
		t.Fatalf("after deselect: n1.Selected=%d want 0", snap[n1+BufNodeColSelected])
	}
	if snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after deselect: n1.LatchedSel=%d want 1 (latch persists through deselect)", snap[n1+BufNodeColLatchedSel])
	}

	// Selecting a DIFFERENT node (n2) moves the latch to n2 and clears it on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildNodeFrame()
	if snap[n2+BufNodeColSelected] != 1 || snap[n2+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after select n2: n2.Selected=%d n2.LatchedSel=%d want 1,1", snap[n2+BufNodeColSelected], snap[n2+BufNodeColLatchedSel])
	}
	if snap[n1+BufNodeColLatchedSel] != 0 {
		t.Fatalf("after select n2: n1.LatchedSel=%d want 0 (latch moved off n1)", snap[n1+BufNodeColLatchedSel])
	}
}

// TestEdgeSelectionExclusiveWithNode verifies KindSelect with the Edge field marks exactly
// one edge's Selected column, persists it, moves it exclusively to another edge, and that
// node/edge selection is MUTUALLY exclusive (selecting an edge clears the node, and vice
// versa). Also asserts the edge rows registered in stable order (edgeLabels; the row→label
// hit-test table itself now lives on MoveDispatch, not here — see
// nodes/Wiring/node_move_row_table_test.go).
func TestEdgeSelectionExclusiveWithNode(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})
	// Two edges (KindGeometry registers them in first-seen order → rows 0,1).
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e0", Node: "n1", Target: "n2"})
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "n2", Target: "n1"})

	// Edge rows registered in stable order.
	if len(s.edgeLabels) != 2 || s.edgeLabels[0] != "e0" || s.edgeLabels[1] != "e1" {
		t.Fatalf("edgeLabels=%v want [e0 e1]", s.edgeLabels)
	}

	// The Edge block now lives in its own tagged frame (see BufBlockTagEdge); the Node
	// block lives in the separate node-owner-group frame (see BufBlockTagNode).
	edgeBase := BufEdgeFrameHeaderSize
	e0Sel := edgeBase + 0*BufEdgeStride + BufEdgeColSelected
	e1Sel := edgeBase + 1*BufEdgeStride + BufEdgeColSelected
	n1Sel := BufNodeFrameHeaderSize + 0*BufNodeStride + BufNodeColSelected

	// Select node n1 first.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	nodeFrame := s.BuildNodeFrame()
	if nodeFrame[n1Sel] != 1 {
		t.Fatalf("after select n1: n1.Selected=%d want 1", nodeFrame[n1Sel])
	}

	// Select edge e0: sets e0, clears the node selection (exclusive).
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e0"})
	edgeFrame := s.BuildEdgeFrame()
	nodeFrame = s.BuildNodeFrame()
	if edgeFrame[e0Sel] != 1 || edgeFrame[e1Sel] != 0 {
		t.Fatalf("after select e0: e0.Selected=%d e1.Selected=%d want 1,0", edgeFrame[e0Sel], edgeFrame[e1Sel])
	}
	if nodeFrame[n1Sel] != 0 {
		t.Fatalf("after select e0: n1.Selected=%d want 0 (edge select clears node)", nodeFrame[n1Sel])
	}

	// Persists across snapshots.
	edgeFrame = s.BuildEdgeFrame()
	if edgeFrame[e0Sel] != 1 {
		t.Fatalf("edge selection did not persist: e0.Selected=%d want 1", edgeFrame[e0Sel])
	}

	// Exclusive among edges: selecting e1 clears e0.
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e1"})
	edgeFrame = s.BuildEdgeFrame()
	if edgeFrame[e0Sel] != 0 || edgeFrame[e1Sel] != 1 {
		t.Fatalf("after select e1: e0.Selected=%d e1.Selected=%d want 0,1", edgeFrame[e0Sel], edgeFrame[e1Sel])
	}

	// Selecting a node again clears the edge selection (exclusive both ways).
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	edgeFrame = s.BuildEdgeFrame()
	if edgeFrame[e1Sel] != 0 {
		t.Fatalf("after select node n2: e1.Selected=%d want 0 (node select clears edge)", edgeFrame[e1Sel])
	}
}

// TestNodeKindIDRoundTrip verifies that NodeKindID maps each known runtime kind to a
// stable index, and that the index is written into the buffer's KindId column and
// read back correctly via buildSnapshot. The expected indices match NODE_DEFS_ARRAY
// order (alphabetical by Go kind name) in node-defs.ts.
func TestNodeKindIDRoundTrip(t *testing.T) {
	// Verify the index produced by NodeKindID matches the known alphabetical order.
	want := map[string]uint8{
		"Hold":                      0,
		"HoldFlip":                  1,
		"HoldNewSendOld":            2,
		"Input":                     3,
		"Pacer":                     4,
		"Pulse":                     5,
		"WindowAndInhibitLeftGate":  6,
		"WindowAndInhibitRightGate": 7,
	}
	for kind, wantID := range want {
		if got := NodeKindID(kind); got != wantID {
			t.Errorf("NodeKindID(%q) = %d, want %d", kind, got, wantID)
		}
	}
	if got := NodeKindID("UnknownKind"); got != KindIDUnknown {
		t.Errorf("NodeKindID(%q) = %d, want KindIDUnknown (%d)", "UnknownKind", got, KindIDUnknown)
	}

	// Verify KindId is written into the buffer snapshot correctly.
	s := NewSnapshotState(nil)
	nodeGeom := func(id, kind string) T.Event {
		return T.Event{
			Kind: T.KindNodeGeometry, Node: id, NodeKind: kind,
			NX: 0, NY: 0, NZ: 0, Radius: 10, SphereR: 10,
			VRX: 0, VRY: 1, VRZ: 0, FRX: 1, FRY: 0, FRZ: 0,
		}
	}
	s.Update(nodeGeom("n0", "Input")) // KindId = 3
	s.Update(nodeGeom("n1", "Pulse")) // KindId = 5
	nodeFrame := s.BuildNodeFrame()

	// Skip the node frame's own header. Node block starts there (see BufBlockTagNode).
	nodeOff := BufNodeFrameHeaderSize
	n0KindId := nodeFrame[nodeOff+0*BufNodeStride+BufNodeColKindId]
	n1KindId := nodeFrame[nodeOff+1*BufNodeStride+BufNodeColKindId]
	if n0KindId != 3 {
		t.Errorf("n0 KindId = %d, want 3 (Input)", n0KindId)
	}
	if n1KindId != 5 {
		t.Errorf("n1 KindId = %d, want 5 (Pulse)", n1KindId)
	}
}

// TestStateMutatingEventEmitsFrame guards the bug class named in the emit-obligation
// refactor: a state-mutating event kind (e.g. KindSelect) must produce a NEW frame on
// s.out, not silently mutate state that only shows up whenever some UNRELATED later
// event happens to emit. If a future event kind mutates SnapshotState but forgets to
// set `emit = true` in its Update arm, this test catches it by asserting the frame count
// strictly increases across that Update call.
func TestStateMutatingEventEmitsFrame(t *testing.T) {
	var buf bytes.Buffer
	s := NewSnapshotState(&buf)

	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1})
	before := buf.Len()

	// KindSelect mutates s.nodes[idx].selected/latchedSel — a state-mutating arm that
	// carries no EVENT-block-only exemption (unlike Recv/Fire/Send) and is not
	// tick-coalesced (unlike Position). It must emit a new frame every call.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n0"})
	after := buf.Len()

	if after <= before {
		t.Fatalf("KindSelect did not emit a new frame: buf.Len() before=%d after=%d (want after > before)", before, after)
	}
}
