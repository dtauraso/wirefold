// Buffer/snapshot_events.go — per-tick EVENT block: recording + resolution + packing.
//
// The buffer carries the full causal trace-event detail (value/port/target/arc/latency/
// geometry + the identity strings) so the ext-host `.probe/go.jsonl` log decodes from the
// SAME binary content buffer — the spec's "one representation including logs". Node/port/
// edge identities are NOT duplicated per event: the EVENT block carries numeric ROW indices
// that resolve through the existing row tables + the label / port-name / edge-label string
// sections. Kind is the event's index into TRACE_EVENT_KINDS (shared Go/TS vocabulary).
//
// Consumed ONLY by the ext-host buffer-decoded logger; the render path ignores this block.

package Buffer

import (
	T "github.com/dtauraso/wirefold/Trace"
)

// recordEvent maps one trace event to a buffered eventRec (string identities kept; rows
// resolved at buildSnapshot time). Every trace kind Update handles is recorded so the
// buffer-decoded log carries the same lines the removed JSON-trace stdout path did.
func (s *SnapshotState) recordEvent(ev T.Event) {
	r := eventRec{kind: ev.Kind, node: ev.Node, port: ev.Port, slot: -1, value: ev.Value, bead: ev.Bead}
	switch ev.Kind {
	case T.KindRecv:
		r.portIsInput = true
	case T.KindSend:
		r.portIsInput = false
		r.arc = ev.ArcLength
		r.lat = ev.SimLatencyMs
		r.target = ev.Target
		r.targetHandle = ev.TargetHandle
	case T.KindPosition:
		r.portIsInput = false
		r.x, r.y, r.z, r.f = ev.X, ev.Y, ev.Z, ev.F
	case T.KindArrive:
		r.portIsInput = false
	case T.KindGeometry:
		r.edge = ev.Edge
	case T.KindLayoutLink:
		r.target = ev.Target
	case T.KindNodeBead:
		r.slot = ev.Row*2 + ev.Col
	case T.KindCamera:
		// All fields read from the Camera block at decode time.
	case T.KindSceneSphere:
		// All fields read from the Scene block at decode time (same pattern as Camera).
	case T.KindSceneTori, T.KindScenePoles, T.KindNodePoles,
		T.KindSelSpherePoles, T.KindHandholds, T.KindLabelsGlobal,
		T.KindOverlaysVis, T.KindDoubleLinks:
		r.flag = ev.Visible
	case T.KindSelect:
		r.edge = ev.Edge // edge!="" → edge select; else node select (value=mode)
	case T.KindHover:
		r.portIsInput = ev.Value == 1
	}
	s.pendingEvents = append(s.pendingEvents, r)
}

// portRowLookup builds a (node,port,isInput) → port-row map in the SAME flattened order
// buildSnapshot writes the Port block, so an event's port resolves to its buffer row.
func (s *SnapshotState) portRowLookup() map[portLookupKey]int {
	m := make(map[portLookupKey]int)
	row := 0
	for i := range s.nodes {
		node := s.nodeIDs[i]
		for _, p := range s.nodes[i].ports {
			m[portLookupKey{node, p.name, p.isInput}] = row
			row++
		}
	}
	return m
}

type portLookupKey struct {
	node    string
	port    string
	isInput bool
}

// FinalFlush emits one last snapshot if events accumulated since the last emit (e.g. trailing
// recv/fire/arrive that were not followed by a position emit), OR if a KindPosition update
// was coalesced away (tickSource set, still on the same tick as the last emit) and so never
// published — otherwise the run's very last bead positions could be dropped. Call AFTER
// Trace.Close has drained every event into Update, so nothing trailing is lost from the buffer.
func (s *SnapshotState) FinalFlush() {
	if len(s.pendingEvents) > 0 || s.positionDirty {
		s.emitSnapshot()
	}
}
