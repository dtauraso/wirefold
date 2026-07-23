// Buffer/row_order_test.go — pins the row-order mechanism main.go relies on: calling
// Update() with a sequence of KindNodeGeometry/KindGeometry events in a given order (the
// same thing main.go's spec-order pre-emit loop does via tr.NodeGeometry/tr.Geometry,
// synchronously and single-goroutine, before any node goroutine can race in) produces
// exactly that row order, and a LATER geometry event for an already-registered id writes
// into its PRE-ASSIGNED row rather than appending a new one. This is the unit-level proof
// that determinism does not depend on scheduler luck (the headless test in the main
// package proves the same thing end-to-end against the real binary).

package Buffer

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

func nodeGeomEvent(id, label string, cx, cy, cz float64) T.Event {
	return T.Event{Kind: T.KindNodeGeometry, Node: id, Label: label, NX: cx, NY: cy, NZ: cz, Radius: 10, SphereR: 10}
}

func TestSequentialEmitProducesRowsInGivenOrder(t *testing.T) {
	s := NewSnapshotState(nil)
	// The order these are Update()'d in — NOT alphabetical, NOT insertion-into-a-map
	// order — is what determines row order; this mirrors main.go's spec-order pre-emit
	// loop running before any node goroutine exists to race it.
	s.Update(nodeGeomEvent("z-node", "z-node", 1, 2, 3))
	s.Update(nodeGeomEvent("a-node", "a-node", 4, 5, 6))
	s.Update(nodeGeomEvent("m-node", "m-node", 7, 8, 9))

	// LookupNodeRow moved off SnapshotState onto MoveDispatch (built once at load from the
	// seed order — see node_move_row_table_test.go for the MoveDispatch-side row-identity
	// test). SnapshotState's own row bookkeeping (nodeIDs, its ingest-order index into
	// s.nodes) is asserted directly here.
	want := []string{"z-node", "a-node", "m-node"}
	for row, id := range want {
		if row >= len(s.nodeIDs) || s.nodeIDs[row] != id {
			t.Fatalf("row %d: got %q, want %q", row, s.nodeIDs[row], id)
		}
	}
	if len(s.nodes) != 3 {
		t.Fatalf("nodes: got %d rows, want 3", len(s.nodes))
	}
	// Prefilled with the emitting event's own geometry, not zeros.
	if s.nodes[1].cx != 4 || s.nodes[1].cy != 5 || s.nodes[1].cz != 6 {
		t.Fatalf("row 1 (a-node) geometry: got (%v,%v,%v), want (4,5,6)", s.nodes[1].cx, s.nodes[1].cy, s.nodes[1].cz)
	}
}

// TestLaterGeometryWritesPreAssignedRow proves the emit-window claim: a node-geometry
// event for an id ALREADY REGISTERED (main.go's pre-emit loop, or an earlier real emit)
// must overwrite that node's pre-assigned row, not append a new row at the end.
func TestLaterGeometryWritesPreAssignedRow(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(nodeGeomEvent("z-node", "z-node", 0, 0, 0))
	s.Update(nodeGeomEvent("a-node", "a-node", 0, 0, 0))

	// "a-node" is row 1. A LATER emit for the same id (e.g. its own goroutine's real
	// startup EmitGeometry, arriving after main.go's pre-emit loop already created the
	// row) must land back in row 1, with the row COUNT unchanged (2), not 3.
	s.Update(nodeGeomEvent("a-node", "a-node", 99, 98, 97))

	if len(s.nodes) != 2 {
		t.Fatalf("nodes: got %d rows after re-emit, want 2 (no new row appended)", len(s.nodes))
	}
	if s.nodeIDs[1] != "a-node" {
		t.Fatalf("row 1: got %q, want a-node (row identity must not move)", s.nodeIDs[1])
	}
	if s.nodes[1].cx != 99 || s.nodes[1].cy != 98 || s.nodes[1].cz != 97 {
		t.Fatalf("row 1 geometry: got (%v,%v,%v), want (99,98,97) (the re-emit's own values)", s.nodes[1].cx, s.nodes[1].cy, s.nodes[1].cz)
	}
	// Row 0 (z-node) must be untouched by a-node's re-emit.
	if s.nodeIDs[0] != "z-node" {
		t.Fatalf("row 0: got %q, want z-node", s.nodeIDs[0])
	}
}

func TestSequentialEdgeEmitProducesRowsInGivenOrder(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e2", Node: "n1", Target: "n2"})
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "n2", Target: "n3"})

	want := []string{"e2", "e1"}
	for row, label := range want {
		if row >= len(s.edgeLabels) || s.edgeLabels[row] != label {
			t.Fatalf("row %d: got %q, want %q", row, s.edgeLabels[row], label)
		}
	}
	if s.edges[0].srcNode != "n1" || s.edges[0].dstNode != "n2" {
		t.Fatalf("row 0 endpoints: got (%q,%q), want (n1,n2)", s.edges[0].srcNode, s.edges[0].dstNode)
	}
}
