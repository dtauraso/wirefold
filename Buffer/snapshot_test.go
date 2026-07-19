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

// TestLookupPortRow verifies the port-row resolution table the gesture FSM uses: a numeric
// buffer PORT-ROW index maps back to its (node, port, isInput) in the SAME flattened order
// the Port block is written (node-row order × each node's Ports order). No port name crosses
// the buffer — the table IS the row→(node,port) authority.
func TestLookupPortRow(t *testing.T) {
	s := NewSnapshotState(nil)

	// Out-of-range before any node registers.
	if _, _, _, ok := s.LookupPortRow(0); ok {
		t.Fatalf("LookupPortRow(0) before any port: want ok=false")
	}

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DX: -1},
			{Name: "out", IsInput: false, DX: 1},
		},
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1", Radius: 1,
		Ports: []T.PortGeom{
			{Name: "in", IsInput: true, DY: 1},
		},
	})

	cases := []struct {
		row     int
		node    string
		port    string
		isInput bool
	}{
		{0, "n0", "in", true},
		{1, "n0", "out", false},
		{2, "n1", "in", true},
	}
	for _, c := range cases {
		node, port, isInput, ok := s.LookupPortRow(c.row)
		if !ok || node != c.node || port != c.port || isInput != c.isInput {
			t.Errorf("LookupPortRow(%d): got (%q,%q,%v,%v), want (%q,%q,%v,true)",
				c.row, node, port, isInput, ok, c.node, c.port, c.isInput)
		}
	}

	// Out-of-range row → ok=false.
	if _, _, _, ok := s.LookupPortRow(3); ok {
		t.Errorf("LookupPortRow(3) out of range: want ok=false")
	}
	if _, _, _, ok := s.LookupPortRow(-1); ok {
		t.Errorf("LookupPortRow(-1): want ok=false")
	}
}

// TestHoverColumn verifies that KindHover marks the Hovered column on the hovered node OR
// port (exclusive, cleared on others), and that an empty hover clears all hover flags.
func TestHoverColumn(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n0", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, DX: -1}}})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	nodeHovered := func(row int) byte {
		snap := s.BuildSnapshot()
		nodeOff := BufHeaderSize + int(readU32(snap, 4))*BufBeadStride
		return snap[nodeOff+row*BufNodeStride+BufNodeColHovered]
	}
	portHovered := func() byte {
		snap := s.BuildSnapshot()
		nodeCount := readU32(snap, 8)
		edgeCount := readU32(snap, 12)
		portOff := BufHeaderSize + int(readU32(snap, 4))*BufBeadStride +
			int(nodeCount)*BufNodeStride +
			int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
			int(edgeCount)*BufEdgeStride
		return snap[portOff+BufPortColHovered] // port row 0 = n0/in
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

	beadCount := int(readU32(snap, 4))
	nodeCount := int(readU32(snap, 8))
	edgeCount := int(readU32(snap, 12))
	layoutLinkCount := int(readU32(snap, 36))

	if layoutLinkCount != 1 {
		t.Fatalf("layoutLinkCount: got %d, want 1 (unresolvable pair must be dropped)", layoutLinkCount)
	}

	llOff := BufHeaderSize +
		beadCount*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride +
		edgeCount*BufEdgeStride

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
// subsequent ticks' EVENT block).
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
	// Build a second snapshot immediately (without producing any new events).
	snap := s.BuildSnapshot()
	eventCount := readU32(snap, 24)
	if eventCount != 0 {
		t.Errorf("eventCount not cleared after snapshot: got %d, want 0", eventCount)
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

	snap := s.BuildSnapshot()
	// Interior block starts right after the single node row (no beads).
	interiorOff := BufHeaderSize + 1*BufNodeStride
	slot := func(i int) int { return interiorOff + i*BufInteriorStride }

	if snap[slot(0)+BufInteriorColPresent] != 1 {
		t.Errorf("slot0.Present: got %d, want 1", snap[slot(0)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(0)+BufInteriorColValue); v != 1 {
		t.Errorf("slot0.Value: got %d, want 1", v)
	}
	if x := readF32(snap, slot(0)+BufInteriorColOX); x != 2 {
		t.Errorf("slot0.OX: got %v, want 2", x)
	}
	if y := readF32(snap, slot(0)+BufInteriorColOY); y != -3 {
		t.Errorf("slot0.OY: got %v, want -3", y)
	}
	if z := readF32(snap, slot(0)+BufInteriorColOZ); z != 4 {
		t.Errorf("slot0.OZ: got %v, want 4", z)
	}
	// Slot 1 (row 0, col 1) was never emitted → absent.
	if snap[slot(1)+BufInteriorColPresent] != 0 {
		t.Errorf("slot1.Present: got %d, want 0 (never emitted)", snap[slot(1)+BufInteriorColPresent])
	}
	// Slot 2 (row 1, col 0): present, value 0.
	if snap[slot(2)+BufInteriorColPresent] != 1 {
		t.Errorf("slot2.Present: got %d, want 1", snap[slot(2)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(2)+BufInteriorColValue); v != 0 {
		t.Errorf("slot2.Value: got %d, want 0", v)
	}

	// A present=false event on slot 0 clears it (popped bead disappears).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: false, Value: 0, X: 2, Y: -3, Z: 4})
	snap = s.BuildSnapshot()
	if snap[slot(0)+BufInteriorColPresent] != 0 {
		t.Errorf("slot0.Present after pop: got %d, want 0", snap[slot(0)+BufInteriorColPresent])
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

	n1 := BufHeaderSize // no beads, no need to offset
	n2 := n1 + BufNodeStride

	// Select n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after select n1: n1.Selected=%d n2.Selected=%d want 1,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Persists: a fresh snapshot with no further select keeps n1 selected.
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 {
		t.Fatalf("selection did not persist: n1.Selected=%d want 1", snap[n1+BufNodeColSelected])
	}

	// Exclusive: selecting n2 clears n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 1 {
		t.Fatalf("after select n2: n1.Selected=%d n2.Selected=%d want 0,1", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Clear: node="" deselects all.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildSnapshot()
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

	n1 := BufHeaderSize
	n2 := n1 + BufNodeStride

	// Select n1: both Selected and LatchedSel move to n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 || snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after select n1: Selected=%d LatchedSel=%d want 1,1", snap[n1+BufNodeColSelected], snap[n1+BufNodeColLatchedSel])
	}

	// Deselect (Node=""): Selected clears, but LatchedSel STAYS on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 {
		t.Fatalf("after deselect: n1.Selected=%d want 0", snap[n1+BufNodeColSelected])
	}
	if snap[n1+BufNodeColLatchedSel] != 1 {
		t.Fatalf("after deselect: n1.LatchedSel=%d want 1 (latch persists through deselect)", snap[n1+BufNodeColLatchedSel])
	}

	// Selecting a DIFFERENT node (n2) moves the latch to n2 and clears it on n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
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
// versa). Also exercises LookupEdgeRow: an edge row resolves to its label.
func TestEdgeSelectionExclusiveWithNode(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})
	// Two edges (KindGeometry registers them in first-seen order → rows 0,1).
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e0", Node: "n1", Target: "n2"})
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "n2", Target: "n1"})

	// Edge-row table resolves rows → labels in stable order.
	if l, ok := s.LookupEdgeRow(0); !ok || l != "e0" {
		t.Fatalf("LookupEdgeRow(0)=%q,%v want e0,true", l, ok)
	}
	if l, ok := s.LookupEdgeRow(1); !ok || l != "e1" {
		t.Fatalf("LookupEdgeRow(1)=%q,%v want e1,true", l, ok)
	}
	if _, ok := s.LookupEdgeRow(2); ok {
		t.Fatalf("LookupEdgeRow(2) ok=true want false (out of range)")
	}

	nodeCount := 2
	edgeBase := BufHeaderSize +
		0*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride
	e0Sel := edgeBase + 0*BufEdgeStride + BufEdgeColSelected
	e1Sel := edgeBase + 1*BufEdgeStride + BufEdgeColSelected
	n1Sel := BufHeaderSize + 0*BufNodeStride + BufNodeColSelected

	// Select node n1 first.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1Sel] != 1 {
		t.Fatalf("after select n1: n1.Selected=%d want 1", snap[n1Sel])
	}

	// Select edge e0: sets e0, clears the node selection (exclusive).
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e0"})
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 1 || snap[e1Sel] != 0 {
		t.Fatalf("after select e0: e0.Selected=%d e1.Selected=%d want 1,0", snap[e0Sel], snap[e1Sel])
	}
	if snap[n1Sel] != 0 {
		t.Fatalf("after select e0: n1.Selected=%d want 0 (edge select clears node)", snap[n1Sel])
	}

	// Persists across snapshots.
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 1 {
		t.Fatalf("edge selection did not persist: e0.Selected=%d want 1", snap[e0Sel])
	}

	// Exclusive among edges: selecting e1 clears e0.
	s.Update(T.Event{Kind: T.KindSelect, Edge: "e1"})
	snap = s.BuildSnapshot()
	if snap[e0Sel] != 0 || snap[e1Sel] != 1 {
		t.Fatalf("after select e1: e0.Selected=%d e1.Selected=%d want 0,1", snap[e0Sel], snap[e1Sel])
	}

	// Selecting a node again clears the edge selection (exclusive both ways).
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[e1Sel] != 0 {
		t.Fatalf("after select node n2: e1.Selected=%d want 0 (node select clears edge)", snap[e1Sel])
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
	snap := s.BuildSnapshot()

	// Skip the 20-byte header. Node block starts there.
	nodeOff := BufHeaderSize
	n0KindId := snap[nodeOff+0*BufNodeStride+BufNodeColKindId]
	n1KindId := snap[nodeOff+1*BufNodeStride+BufNodeColKindId]
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
