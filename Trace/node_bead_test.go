// node_bead_test.go — verifies the node-bead trace event (node 1's interior
// 2x2 buffer): NodeBead emits a KindNodeBead event carrying node id + (row,col),
// the bead value, and its world position, and marshalEvent serializes the
// expected JSON shape.
package Trace

import (
	"encoding/json"
	"testing"
)

func TestNodeBeadEmitsEvent(t *testing.T) {
	tr := New(8)
	tr.NodeBead("N1", 1, 0, true, 1, 4.5, -6.5, 0)
	tr.Close()

	events := tr.Events()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != KindNodeBead {
		t.Fatalf("kind = %q, want %q", e.Kind, KindNodeBead)
	}
	if e.Node != "N1" || e.Row != 1 || e.Col != 0 || !e.Present || e.Value != 1 {
		t.Fatalf("identity mismatch: node=%q row=%d col=%d present=%v value=%d", e.Node, e.Row, e.Col, e.Present, e.Value)
	}
	if e.X != 4.5 || e.Y != -6.5 || e.Z != 0 {
		t.Fatalf("position mismatch: (%v,%v,%v)", e.X, e.Y, e.Z)
	}

	b, err := marshalEvent(e)
	if err != nil {
		t.Fatalf("marshalEvent: %v", err)
	}
	var got struct {
		Kind    string  `json:"kind"`
		Node    string  `json:"node"`
		Row     int     `json:"row"`
		Col     int     `json:"col"`
		Present bool    `json:"present"`
		Value   int     `json:"value"`
		X       float64 `json:"x"`
		Y       float64 `json:"y"`
		Z       float64 `json:"z"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	if got.Kind != "node-bead" || got.Node != "N1" || got.Row != 1 || got.Col != 0 || !got.Present || got.Value != 1 {
		t.Fatalf("json header mismatch: %s", b)
	}
	if got.X != 4.5 || got.Y != -6.5 || got.Z != 0 {
		t.Fatalf("json position mismatch: %s", b)
	}
}
