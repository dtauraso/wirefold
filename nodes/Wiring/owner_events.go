// owner_events.go — the per-owner EVENT-block payload (memory/feedback_no_single_writer_bridge.md):
// each emitting goroutine (nodeMover, edgeMover, a node's own Update loop) resolves its OWN
// events to numeric buffer rows AT THE CALL SITE, using resolvers it already holds
// (nodeRowFor/portRowFor/edgeRowForPair — the same closures its geometry columns already
// resolve through), and hands the resolved slice straight to its OWN buildFrame closure. No
// event is ever routed through a shared/central buffer or a second goroutine: this is a plain
// value type, not a channel or a lock-guarded structure — resolution and packing both happen on
// the SAME goroutine that owns the fd.
//
// Buffer.KindID resolves Kind to its numeric EVENT-block id at PACK time (in main.go's injected
// closures, which import Buffer); Kind stays a string here so this package keeps its existing
// Buffer-independence (see PortRowResolver/EdgeRowResolver's doc comments).
package Wiring

// RowEvent is one fully row-resolved event, ready for this goroutine's own frame's trailing
// EVENTS section. -1 sentinels an unresolved/absent row reference, matching the buffer's
// existing EVENT-block convention (bufLayoutEvent in Buffer/layout.go).
type RowEvent struct {
	Kind                                                             string
	NodeRow, PortRow, TargetRow, TargetPortRow, EdgeRow, Slot, Value int32
	Bead                                                             uint64
	ArcLength, SimLatencyMs, X, Y, Z, F                              float64
}
