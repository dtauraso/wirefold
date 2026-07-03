package Trace

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Verifies the drain loop's encoder path is byte-identical to the previous
// json.Marshal(marshalEvent)+'\n' output, across every event kind.
func TestEncoderByteIdenticalToMarshal(t *testing.T) {
	events := []Event{
		{Kind: KindRecv, Node: "n1", Port: "in", Value: 0},
		{Kind: KindFire, Node: "n1"},
		{Kind: KindSend, Node: "n1", Port: "out", Value: 7},
		{Kind: KindSend, Node: "n<1>", Port: "o&t", Value: 0, ArcLength: 1.5, SimLatencyMs: 2.25, Target: "t>", TargetHandle: "h&"},
		{Kind: KindDone, Node: "n1", Port: "out"},
		{Kind: KindPosition, Node: "n1", Port: "out", Value: 3, X: 1.1, Y: -2.2, Z: 0, F: 0.5, Bead: 42},
		{Kind: KindGeometry, Edge: "e1", SX: 1, SY: 2, SZ: 3, EX: 4, EY: 5, EZ: 6},
		{Kind: KindPulseCancelled, Node: "n1", Port: "out", Value: 1, Bead: 9},
		{Kind: KindArrive, Node: "n1", Port: "out", Value: 1, Bead: 9},
		{Kind: KindNodeGeometry, Node: "n1", NX: 1, NY: 2, NZ: 3, Radius: 4, SphereR: 5, VRX: 1, Ports: []PortGeom{{Name: "p<", IsInput: true, PX: 1, DY: 2}}},
		{Kind: KindNodeBead, Node: "n1", Row: 1, Col: 0, Present: true, Value: 1, X: 1, Y: 2, Z: 3},
		{Kind: KindCamera, PX: 1, PY: 2, PZ: 3, R: 4, PosTheta: 5, PosPhi: 6, UpTheta: 7, UpPhi: 8},
		{Kind: KindSceneTori, Visible: true},
		{Kind: KindDoubleLinks, Visible: false},
		{Kind: "mystery-kind", Node: "n1", Port: "out", Value: 5},
	}
	for _, e := range events {
		// old path
		b, err := marshalEvent(e)
		if err != nil {
			t.Fatalf("marshalEvent(%s): %v", e.Kind, err)
		}
		old := append(append([]byte{}, b...), '\n')
		// new path
		v, err := eventValue(e)
		if err != nil {
			t.Fatalf("eventValue(%s): %v", e.Kind, err)
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		if err := enc.Encode(v); err != nil {
			t.Fatalf("encode(%s): %v", e.Kind, err)
		}
		if !bytes.Equal(old, buf.Bytes()) {
			t.Errorf("kind %s NOT byte-identical:\n old: %q\n new: %q", e.Kind, old, buf.Bytes())
		}
	}
}
