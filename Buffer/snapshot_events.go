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
	case T.KindRecv, T.KindDone:
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
	case T.KindArrive, T.KindPulseCancelled:
		r.portIsInput = false
	case T.KindGeometry:
		r.edge = ev.Edge
	case T.KindNodeBead:
		r.slot = ev.Row*2 + ev.Col
	case T.KindCamera:
		// All fields read from the Camera block at decode time.
	case T.KindSceneTori, T.KindScenePoles, T.KindNodePoles, T.KindAngleLabels,
		T.KindSelSpherePoles, T.KindHandholds, T.KindLabelsGlobal, T.KindBadgesGlobal,
		T.KindOverlaysVis, T.KindDoubleLinks:
		r.flag = ev.Visible
	case T.KindSelect:
		r.edge = ev.Edge // edge!="" → edge select; else node select (value=mode)
	case T.KindHover:
		r.portIsInput = ev.Value == 1
	case T.KindFade:
		r.fadedNodes = append([]string(nil), ev.FadedNodes...)
		r.fadedEdges = append([]string(nil), ev.FadedEdges...)
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

// writeEventBlock resolves every buffered event to numeric rows and packs the EVENT block
// into buf (eventCount rows). portRows is the (node,port,isInput)→row map. Returns nothing;
// pendingEvents is cleared by clearTransients after the emit.
func (s *SnapshotState) writeEventBlock(buf []byte, portRows map[portLookupKey]int) {
	for row, e := range s.pendingEvents {
		nodeRow := int32(s.nodeRowIndex(e.node))
		portRow := int32(-1)
		if e.port != "" {
			if pr, ok := portRows[portLookupKey{e.node, e.port, e.portIsInput}]; ok {
				portRow = int32(pr)
			}
		}
		targetRow := int32(-1)
		if e.target != "" {
			targetRow = int32(s.nodeRowIndex(e.target))
		}
		targetPortRow := int32(-1)
		if e.targetHandle != "" {
			if pr, ok := portRows[portLookupKey{e.target, e.targetHandle, true}]; ok {
				targetPortRow = int32(pr)
			}
		}
		edgeRow := int32(-1)
		if e.edge != "" {
			if idx, ok := s.edgeIndex[e.edge]; ok {
				edgeRow = int32(idx)
			}
		}
		SetEventRow(buf, row,
			s.kindID[e.kind], nodeRow, portRow, targetRow, targetPortRow, edgeRow,
			int32(e.slot), int32(e.value), uint32(e.bead),
			float32(e.arc), float32(e.lat), float32(e.x), float32(e.y), float32(e.z), float32(e.f))
	}
}

// FinalFlush emits one last snapshot if events accumulated since the last emit (e.g. trailing
// recv/fire/done/arrive that were not followed by a position emit). Call AFTER Trace.Close has
// drained every event into Update, so no trailing causal event is lost from the buffer log.
func (s *SnapshotState) FinalFlush() {
	if len(s.pendingEvents) > 0 {
		s.emitSnapshot()
	}
}
