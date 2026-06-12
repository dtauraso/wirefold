// node_geometry_test.go — verifies the node-geometry trace event (item 1):
// NodeGeometry emits a KindNodeGeometry event carrying the node center + per-port
// world positions/dirs, and marshalEvent serializes it with the expected JSON shape.
package Trace

import (
	"encoding/json"
	"testing"
)

func TestNodeGeometryEmitsEvent(t *testing.T) {
	tr := New(8)
	tr.NodeGeometry("N1", 1, 2, 3, 15, []PortGeom{
		{Name: "in", IsInput: true, PX: 0.5, PY: 2, PZ: 3, DX: -1, DY: 0, DZ: 0},
		{Name: "out", IsInput: false, PX: 1.5, PY: 2, PZ: 3, DX: 1, DY: 0, DZ: 0},
	})
	tr.Close()

	events := tr.Events()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	e := events[0]
	if e.Kind != KindNodeGeometry {
		t.Fatalf("kind = %q, want %q", e.Kind, KindNodeGeometry)
	}
	if e.Node != "N1" || e.NX != 1 || e.NY != 2 || e.NZ != 3 || e.Radius != 15 {
		t.Fatalf("node/center/radius mismatch: %q (%v,%v,%v) r=%v", e.Node, e.NX, e.NY, e.NZ, e.Radius)
	}
	if len(e.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(e.Ports))
	}

	b, err := marshalEvent(e)
	if err != nil {
		t.Fatalf("marshalEvent: %v", err)
	}
	var got struct {
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		NX    float64 `json:"nx"`
		NY    float64 `json:"ny"`
		NZ     float64 `json:"nz"`
		Radius float64 `json:"radius"`
		Ports  []struct {
			Name    string  `json:"name"`
			IsInput bool    `json:"isInput"`
			PX, PY, PZ float64 `json:"-"`
			DX, DY, DZ float64 `json:"-"`
		} `json:"ports"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	if got.Kind != "node-geometry" || got.Node != "N1" || got.NX != 1 || got.NY != 2 || got.NZ != 3 || got.Radius != 15 {
		t.Fatalf("json header mismatch: %s", b)
	}
	if len(got.Ports) != 2 || got.Ports[0].Name != "in" || !got.Ports[0].IsInput || got.Ports[1].Name != "out" || got.Ports[1].IsInput {
		t.Fatalf("json ports mismatch: %s", b)
	}
}
