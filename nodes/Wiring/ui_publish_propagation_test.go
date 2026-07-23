// ui_publish_propagation_test.go — proves the message-passing UI-state path (no shared
// map, no mutex, no atomic): driving a selection/hover/abc-drag change through the REAL
// gesture path (applySelect/setHover, quantized_move.go's neighborSetCRequantize AbcDrag
// path) updates the AFFECTED mover's OWN fields via a message on its own dedicated
// channel, and the affected mover re-emits its dedicated stream frame with the new state
// on its OWN periodic every-cycle emit — no central trigger, no nudge mechanism needed
// (nodeMover.run's writeStreamFrame call already runs every cycle regardless of geometry
// change, same as edgeMover.run — see node_mover.go). The abc-drag COUNT is proven via
// Buffer.SnapshotState's own single-goroutine counter (s.overlay.AbcDragCount, written on
// the Trace-drain goroutine only) reflected in the VIEW frame.

package Wiring

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
)

// uiPubLockedBuf is a mutex-guarded io.Writer capturing framed stream bytes from a
// nodeMover/edgeMover/SnapshotState goroutine, mirroring abc_drag_scope_test.go's fd-3
// capture pattern but for a per-owner dedicated stream (no leading block-tag byte — the
// fd position identifies it).
type uiPubLockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *uiPubLockedBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *uiPubLockedBuf) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, w.buf.Len())
	copy(out, w.buf.Bytes())
	return out
}

// lastNodeStreamSelectedHovered decodes the LAST complete framed node-stream payload in
// raw ([len:u32][payload], no tag byte) and returns its Node row's Selected/Hovered bytes.
// ok=false if no complete frame has arrived yet.
func lastNodeStreamSelectedHovered(raw []byte) (selected, hovered uint8, ok bool) {
	off := 0
	var last []byte
	for off+4 <= len(raw) {
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			break
		}
		last = raw[off : off+n]
		off += n
	}
	// Header: [tick,portCount,labelLen,portNameBytesCount,layoutLinkCount] = 5×u32 = 20
	// bytes (BuildNodeStreamFrame's doc comment), then the Node block.
	const nodeOff = 20
	if last == nil || len(last) < nodeOff+B.BufNodeStride {
		return 0, 0, false
	}
	return last[nodeOff+B.BufNodeColSelected], last[nodeOff+B.BufNodeColHovered], true
}

// lastNodeStreamDragMsg decodes the LAST complete node-stream frame's GotDragMsg/
// DragDeltaA/B/C fields, mirroring lastNodeStreamSelectedHovered.
func lastNodeStreamDragMsg(raw []byte) (gotDragMsg uint8, deltaA, deltaB, deltaC int32, ok bool) {
	off := 0
	var last []byte
	for off+4 <= len(raw) {
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			break
		}
		last = raw[off : off+n]
		off += n
	}
	const nodeOff = 20
	if last == nil || len(last) < nodeOff+B.BufNodeStride {
		return 0, 0, 0, 0, false
	}
	node := last[nodeOff : nodeOff+B.BufNodeStride]
	g := node[B.BufNodeColGotDragMsg]
	dA := int32(binary.LittleEndian.Uint32(node[B.BufNodeColDragDeltaA:]))
	dB := int32(binary.LittleEndian.Uint32(node[B.BufNodeColDragDeltaB:]))
	dC := int32(binary.LittleEndian.Uint32(node[B.BufNodeColDragDeltaC:]))
	return g, dA, dB, dC, true
}

// lastViewFrameAbcDragCount decodes the LAST complete framed VIEW-stream payload and
// returns its Overlay block's AbcDragCount. ok=false if no complete frame has arrived.
func lastViewFrameAbcDragCount(raw []byte) (count uint32, ok bool) {
	off := 0
	var last []byte
	for off+4 <= len(raw) {
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			break
		}
		last = raw[off : off+n]
		off += n
	}
	countOff := B.BufViewFrameHeaderSize + B.BufCameraStride + B.BufOverlayColAbcDragCount
	if last == nil || len(last) < countOff+4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(last[countOff:]), true
}

// TestGesturePathPropagatesUIStateToMoverStream drives selection, hover, and abc-drag
// through the real gesture/quantized-move call sites and asserts (a) the AFFECTED
// mover's OWN dedicated stream frame shows the new state via its periodic emit (no
// shared/republished map to poll instead), and (b) the VIEW frame's AbcDragCount
// (Buffer.SnapshotState's own plain counter) reflects the abc-drag event.
func TestGesturePathPropagatesUIStateToMoverStream(t *testing.T) {
	root := writeXTN(t) // x --Out--> t (chain), x --Out--> n (data)

	viewBuf := &uiPubLockedBuf{}
	snapState := B.NewSnapshotState(nil)
	snapState.SetViewOut(viewBuf)
	tr := T.NewWithSinkHook(0, nil, snapState.Update)

	_, _, md, _, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	nm, ok := md.nodeMovers["x"]
	if !ok {
		t.Fatal("no nodeMover for x")
	}
	xRow, ok := md.NodeRowFor("x")
	if !ok {
		t.Fatal("no NODE-ROW for x")
	}
	nmT, ok := md.nodeMovers["t"]
	if !ok {
		t.Fatal("no nodeMover for t")
	}
	tRow, ok := md.NodeRowFor("t")
	if !ok {
		t.Fatal("no NODE-ROW for t")
	}
	// Wire x's and t's movers directly to captured streams — the same wiring main.go
	// now does via SetNodeStreams in production (test-only direct field assignment:
	// same package, bypasses SetNodeStreams' real-fd plumbing, which requires actual
	// OS file descriptors at fixed numbers).
	bufX := &uiPubLockedBuf{}
	nm.streamOut = bufX
	nm.nodeRow = xRow
	nm.buildFrame = func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []RowEvent) []byte {
		return B.BuildNodeStreamFrame(tick, nodeRow, cx, cy, cz, radius, sphereR, vrx, vry, vrz, frx, fry, frz, selected, kindID, hovered, latchedSel, gotDragMsg, dragDeltaA, dragDeltaB, dragDeltaC, label, portNames, portDX, portDY, portDZ, portPX, portPY, portPZ, portIsInput, portHovered, dstNodeRows, edgeRows, nil)
	}

	bufT := &uiPubLockedBuf{}
	nmT.streamOut = bufT
	nmT.nodeRow = tRow
	nmT.buildFrame = func(tick uint32, nodeRow int32, cx, cy, cz, radius, sphereR float32, vrx, vry, vrz, frx, fry, frz float32, selected, kindID, hovered, latchedSel, gotDragMsg uint8, dragDeltaA, dragDeltaB, dragDeltaC int32, label string, portNames []string, portDX, portDY, portDZ, portPX, portPY, portPZ []float32, portIsInput, portHovered []uint8, dstNodeRows, edgeRows []int32, events []RowEvent) []byte {
		return B.BuildNodeStreamFrame(tick, nodeRow, cx, cy, cz, radius, sphereR, vrx, vry, vrz, frx, fry, frz, selected, kindID, hovered, latchedSel, gotDragMsg, dragDeltaA, dragDeltaB, dragDeltaC, label, portNames, portDX, portDY, portDZ, portPX, portPY, portPZ, portIsInput, portHovered, dstNodeRows, edgeRows, nil)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	if selected, _, ok := lastNodeStreamSelectedHovered(bufX.Bytes()); ok && selected != 0 {
		t.Fatalf("x's stream selected before select = %v, want 0", selected)
	}

	// --- Selection: applySelect (gesture.go) is the real click-outcome path. This is a
	// MESSAGE to x's own mover (moveMsgKindSelect on x's extIn) — no shared map. ---
	md.applySelect(rawInputMsg{Hit: rawHit{Kind: "node", NodeRow: int(xRow)}}, tr)
	waitForNodeStream(t, bufX, func(selected, hovered uint8) bool { return selected == 1 })

	// --- Hover: setHover (gesture.go) is the shared dedupe+write hover path — also a
	// message (moveMsgKindHover) to x's own mover. ---
	md.setHover("x", "", false, tr)
	waitForNodeStream(t, bufX, func(selected, hovered uint8) bool { return hovered == 1 })

	// --- Abc-drag: the real recipient path is a moveMsgKindNeighborSetC message routed
	// to the RECIPIENT's own dedicated channel (mirrors requantizeLocalPolars' fan) —
	// t's own goroutine runs neighborSetCRequantize and sets its OWN gotDragMsg/
	// dragDelta* fields, no cross-goroutine write from this test goroutine (which would
	// race t's own writeStreamFrame reads under -race). ---
	before, _ := lastViewFrameAbcDragCount(viewBuf.Bytes())
	md.sendMove("t", moveMsg{
		Kind: moveMsgKindNeighborSetC, NodeID: "t", SenderID: "x",
		FromCenter: vec3{X: 1, Y: 2, Z: 3}, DeltaA: 1, DeltaB: 2, DeltaC: 3,
	})
	waitForNodeDragMsg(t, bufT, func(got uint8, dA, dB, dC int32) bool {
		return got == 1 && dA == 1 && dB == 2 && dC == 3
	})
	waitForViewAbcDragCount(t, viewBuf, func(count uint32) bool { return count == before+1 })

	// --- AbcDragReset (resetAbcDrag) broadcasts moveMsgKindAbcReset to every node
	// mover, clearing t's OWN recipient bit — the count (view frame) is left alone
	// (mirrors Buffer.SnapshotState's KindAbcDragReset handling: count is a cumulative
	// total-events affirmation, not drag-scoped). ---
	md.resetAbcDrag()
	waitForNodeDragMsg(t, bufT, func(got uint8, dA, dB, dC int32) bool { return got == 0 })
	if after, ok := lastViewFrameAbcDragCount(viewBuf.Bytes()); !ok || after != before+1 {
		t.Fatalf("AbcDragCount after resetAbcDrag = %v, want %v (count is not drag-scoped)", after, before+1)
	}
}

// waitForNodeStream polls buf's captured frames until check(selected, hovered) is true or
// a bounded deadline elapses — proves the affected mover's OWN periodic every-cycle emit
// (nodeMover.run's writeStreamFrame call, MsPerTick=16ms cycles) picks up the new UI state
// with no geometry change and no nudge mechanism.
func waitForNodeStream(t *testing.T, buf *uiPubLockedBuf, check func(selected, hovered uint8) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if selected, hovered, ok := lastNodeStreamSelectedHovered(buf.Bytes()); ok && check(selected, hovered) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node's dedicated stream frame never reflected the expected UI state within deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForNodeDragMsg is waitForNodeStream's abc-drag counterpart.
func waitForNodeDragMsg(t *testing.T, buf *uiPubLockedBuf, check func(gotDragMsg uint8, dA, dB, dC int32) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, dA, dB, dC, ok := lastNodeStreamDragMsg(buf.Bytes()); ok && check(got, dA, dB, dC) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node's dedicated stream frame never reflected the expected abc-drag state within deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// waitForViewAbcDragCount polls viewBuf's captured VIEW frames until check(count) is
// true or a bounded deadline elapses.
func waitForViewAbcDragCount(t *testing.T, buf *uiPubLockedBuf, check func(count uint32) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if count, ok := lastViewFrameAbcDragCount(buf.Bytes()); ok && check(count) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("view frame never reflected the expected AbcDragCount within deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
