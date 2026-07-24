// Buffer/stream_events.go — the per-owner-frame trailing EVENTS section (memory/
// feedback_no_single_writer_bridge.md): every per-owner stream frame (NODE/EDGE/INTERIOR/
// VIEW) appends [count:u32] + count × BufEventStride bytes AFTER its own self-describing
// payload, using the SAME row layout (SetEventRow) the fd3 SCENE frame's EVENT block used
// before this migration — so the ext host decodes an event identically regardless of which
// fd it rode in on. No frame header needs an eventCount field of its own: the decoder reads
// this section as "whatever bytes remain" once each frame's own known counts are exhausted.
package Buffer

import (
	"encoding/binary"

	T "github.com/dtauraso/wirefold/Trace"
)

// StreamEvent is one packed EVENT-block row. Kind is already resolved to its numeric
// TRACE_EVENT_KINDS index (via KindID) by the caller.
type StreamEvent struct {
	Kind                                                             uint8
	NodeRow, PortRow, TargetRow, TargetPortRow, EdgeRow, Slot, Value int32
	Bead                                                             uint32
	ArcLength, SimLatencyMs, X, Y, Z, F                              float32
	// Label/Debug/Text mirror Wiring.RowEvent's breadcrumb fields (Kind ==
	// KindBreadcrumb only). Text is packed by BuildEventsSection into this frame's
	// own trailing event-text-bytes section, immediately after the fixed-stride
	// event rows — the single sanctioned free-form string escape hatch on this row
	// (tools/check-event-string-section-singular.sh).
	Label uint8
	Debug uint8
	Text  string
}

// kindIDByName is built once at package init from the closed T.TraceEventKinds
// vocabulary and never mutated again, so concurrent reads from many owner goroutines
// need no synchronization — a pure package-level lookup so any per-owner goroutine can
// resolve its own event kind with no shared accumulator instance.
var kindIDByName = buildKindIDMap()

// buildKindIDMap indexes T.TraceEventKinds so the EVENT block Kind column matches the
// TS TRACE_EVENT_KINDS array (both generated from Trace.go's Kind* constants).
func buildKindIDMap() map[string]uint8 {
	m := make(map[string]uint8, len(T.TraceEventKinds))
	for i, k := range T.TraceEventKinds {
		m[k] = uint8(i)
	}
	return m
}

// KindID resolves a raw Trace kind string (T.Kind*) to its EVENT-block numeric id.
func KindID(kind string) uint8 {
	return kindIDByName[kind]
}

// BuildEventsSection packs events into one trailing EVENTS section: [count:u32] +
// count × BufEventStride bytes, followed by the event-text-bytes section (every
// event's Text, concatenated in event order) — the single free-form string section
// this row type carries (TextOff/TextLen on bufLayoutEvent). This section is always
// the LAST thing in a per-owner stream frame (every BuildXStreamFrame appends it
// last), so appending the text bytes here needs no frame-level size bookkeeping.
func BuildEventsSection(events []StreamEvent) []byte {
	textBytes := make([][]byte, len(events))
	textLen := 0
	for i, e := range events {
		textBytes[i] = []byte(e.Text)
		textLen += len(textBytes[i])
	}
	buf := make([]byte, 4+len(events)*BufEventStride+textLen)
	binary.LittleEndian.PutUint32(buf[0:], uint32(len(events)))
	rows := buf[4 : 4+len(events)*BufEventStride]
	textOff := uint32(0)
	off := 4 + len(events)*BufEventStride
	for i, e := range events {
		tb := textBytes[i]
		SetEventRow(rows, i,
			e.Kind, e.NodeRow, e.PortRow, e.TargetRow, e.TargetPortRow, e.EdgeRow,
			e.Slot, e.Value, e.Bead, e.ArcLength, e.SimLatencyMs, e.X, e.Y, e.Z, e.F,
			e.Label, e.Debug, textOff, uint32(len(tb)))
		copy(buf[off:], tb)
		off += len(tb)
		textOff += uint32(len(tb))
	}
	return buf
}
