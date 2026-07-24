package Trace

import "encoding/json"

// marshalBreadcrumb serializes one Breadcrumb() call as {"kind":"breadcrumb",...} with
// empty fields omitted. Used ONLY by the in-process test-observation sink now (see
// Trace.go's Breadcrumb/Trace-struct doc comments) — the production stdout path this
// shape used to also feed (routed by the ext host's now-removed tryParseBreadcrumb to
// .probe/go-debug.jsonl) was retired in favor of the structured buffer EVENT row.
func marshalBreadcrumb(label, node, port, value string) ([]byte, error) {
	type breadcrumb struct {
		Kind  string `json:"kind"`
		Label string `json:"label"`
		Node  string `json:"node,omitempty"`
		Port  string `json:"port,omitempty"`
		Value string `json:"value,omitempty"`
	}
	return json.Marshal(breadcrumb{Kind: "breadcrumb", Label: label, Node: node, Port: port, Value: value})
}

// marshalNodeBead serializes a NodeBead event for the optional in-process test sink
// (never wired in production — see NodeBead's doc comment). Used only by headless
// tests observing emitRefillSlide's animation frames via NewWithSink.
func marshalNodeBead(e Event) ([]byte, error) {
	type nodeBead struct {
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
	return json.Marshal(nodeBead{Kind: e.Kind, Node: e.Node, Row: e.Row, Col: e.Col, Present: e.Present, Value: e.Value, X: e.X, Y: e.Y, Z: e.Z})
}
