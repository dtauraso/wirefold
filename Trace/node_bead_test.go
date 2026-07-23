// node_bead_test.go — verifies NodeBead (the one surviving Trace event; see Trace.go's
// header doc comment): it delivers synchronously to the optional in-process onEvent
// hook and sink, carrying node id + (row,col), the bead value, and its world position.
package Trace

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNodeBeadCallsOnEvent(t *testing.T) {
	var got []Event
	tr := NewWithSinkHook(0, nil, func(e Event) { got = append(got, e) })
	tr.NodeBead("N1", 1, 0, true, 1, 4.5, -6.5, 0)

	if len(got) != 1 {
		t.Fatalf("got %d onEvent calls, want 1", len(got))
	}
	e := got[0]
	if e.Kind != KindNodeBead {
		t.Fatalf("kind = %q, want %q", e.Kind, KindNodeBead)
	}
	if e.Node != "N1" || e.Row != 1 || e.Col != 0 || !e.Present || e.Value != 1 {
		t.Fatalf("identity mismatch: node=%q row=%d col=%d present=%v value=%d", e.Node, e.Row, e.Col, e.Present, e.Value)
	}
	if e.X != 4.5 || e.Y != -6.5 || e.Z != 0 {
		t.Fatalf("position mismatch: (%v,%v,%v)", e.X, e.Y, e.Z)
	}
}

func TestNodeBeadWritesSinkJSON(t *testing.T) {
	var buf bytes.Buffer
	tr := NewWithSink(0, &buf)
	tr.NodeBead("N1", 1, 0, true, 1, 4.5, -6.5, 0)

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
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.Bytes())
	}
	if got.Kind != "node-bead" || got.Node != "N1" || got.Row != 1 || got.Col != 0 || !got.Present || got.Value != 1 {
		t.Fatalf("json header mismatch: %s", buf.Bytes())
	}
	if got.X != 4.5 || got.Y != -6.5 || got.Z != 0 {
		t.Fatalf("json position mismatch: %s", buf.Bytes())
	}
}

func TestNodeBeadNilReceiverIsNoOp(t *testing.T) {
	var tr *Trace
	tr.NodeBead("N1", 0, 0, true, 1, 0, 0, 0) // must not panic
}
