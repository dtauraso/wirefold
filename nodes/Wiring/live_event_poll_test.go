// live_event_poll_test.go — shared helper for external-package tests (Wiring_test) that
// need to poll a live MoveDispatch's per-owner RowEvents while node goroutines are still
// running. Decentralized (Step C, per-owner-buffer-rows.md): each node/edge goroutine now
// writes its own dedicated stream frame directly (no central Trace event channel to hook
// into), so a live-observing test wires md.SetNodeStreams with a capturing buildFrame/
// buildInteriorFrame, exactly as main.go wires the real Buffer builders — just capturing
// the RowEvent slice instead of encoding it to bytes. streamOut is a real fd (os.Pipe)
// so os.NewFile(uintptr(fd), ...) inside SetNodeStreams resolves to a valid, if unread,
// write end — the write itself is fire-and-forget (errors ignored) so an unread pipe
// never blocks or fails the capture.
package Wiring_test

import (
	"os"
	"sync"

	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// rowEventLog is a thread-safe sink for RowEvents captured from a live MoveDispatch's
// per-node/per-edge dedicated stream frames.
type rowEventLog struct {
	mu     sync.Mutex
	events []W.RowEvent
}

func (l *rowEventLog) record(events []W.RowEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, events...)
}

// snapshot returns a copy of every RowEvent recorded so far. Safe to call concurrently
// with record (the owning node/edge goroutines) at any time.
func (l *rowEventLog) snapshot() []W.RowEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]W.RowEvent, len(l.events))
	copy(out, l.events)
	return out
}

// wireLiveRowEvents installs a capturing SetNodeStreams on md so every node's own
// Fire/Recv/Send/NodeBead RowEvents (and node-geometry ones) land in the returned
// rowEventLog as they're emitted, live, while node goroutines run. Uses real pipe fds
// (os.Pipe) so os.NewFile inside SetNodeStreams gets a valid write end; the pipes are
// intentionally never read (fire-and-forget capture happens in buildFrame itself, not
// by decoding the written bytes).
func wireLiveRowEvents(md *W.MoveDispatch) *rowEventLog {
	log := &rowEventLog{}
	_, nodeW, _ := os.Pipe()
	_, interiorW, _ := os.Pipe()
	nodeBase := int(nodeW.Fd())
	interiorBase := int(interiorW.Fd())
	md.SetNodeStreams(nodeBase, interiorBase,
		md.NodeRowFor, md.EdgeRowForPair,
		func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []W.RowEvent) []byte {
			log.record(events)
			return nil
		},
		func(tick uint32, present []uint8, value []int32, ox, oy, oz []float32, events []W.RowEvent) []byte {
			log.record(events)
			return nil
		},
		func(kind string) uint8 { return 0 },
	)
	return log
}
