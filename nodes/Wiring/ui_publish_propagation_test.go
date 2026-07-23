// ui_publish_propagation_test.go — proves step 2 of retiring the accumulator (step 1,
// ea6167f9, moved the row-identity tables): driving a selection/hover/abc-drag change
// through the REAL gesture path (applySelect/setHover, quantized_move.go's
// neighborSetCRequantize AbcDrag path) updates MoveDispatch's OWN published UI-state maps
// (ui_publish.go) directly, and the affected nodeMover re-emits its dedicated stream frame
// with the new state on its OWN periodic every-cycle emit — no central trigger, no nudge
// mechanism needed (nodeMover.run's writeStreamFrame call already runs every cycle
// regardless of geometry change, same as edgeMover.run — see node_mover.go).

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
// nodeMover goroutine, mirroring abc_drag_scope_test.go's fd-3 capture pattern but for a
// per-node dedicated stream (no leading block-tag byte — the fd position identifies it).
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

// TestGesturePathPropagatesUIStateToMoverStream drives selection, hover, and abc-drag
// through the real gesture/quantized-move call sites and asserts (a) MoveDispatch's own
// published maps (NodeUIStateFor/AbcDragCount) reflect the change immediately, and (b) the
// affected node's OWN dedicated stream frame shows the new state via its periodic emit.
func TestGesturePathPropagatesUIStateToMoverStream(t *testing.T) {
	root := writeXTN(t) // x --Out--> t (chain), x --Out--> n (data)
	md := loadTreeMD(t, root)

	nm, ok := md.nodeMovers["x"]
	if !ok {
		t.Fatal("no nodeMover for x")
	}
	xRow, ok := md.NodeRowFor("x")
	if !ok {
		t.Fatal("no NODE-ROW for x")
	}
	// Wire x's mover directly to a captured stream + MoveDispatch's OWN published-state
	// accessors — the same wiring main.go now does via SetNodeStreams in production
	// (test-only direct field assignment: same package, bypasses SetNodeStreams' real-fd
	// plumbing, which requires actual OS file descriptors at fixed numbers).
	buf := &uiPubLockedBuf{}
	nm.streamOut = buf
	nm.nodeRow = xRow
	nm.uiStateFor = md.NodeUIStateFor
	nm.portHoveredFor = md.PortHoveredFor
	nm.buildFrame = B.BuildNodeStreamFrame
	// Production's SetNodeStreams publishes an initial (empty) UI-state snapshot as part
	// of wiring (node_move.go) — reproduce that here since this test bypasses SetNodeStreams
	// itself (no real fds).
	md.republishUIState()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	if sel, _, _, _, _, _, _, _, ok := md.NodeUIStateFor("x"); !ok || sel != 0 {
		t.Fatalf("NodeUIStateFor(x) before select = (selected=%v,ok=%v), want (0,true)", sel, ok)
	}

	tr := T.New(0)

	// --- Selection: applySelect (gesture.go) is the real click-outcome path. ---
	md.applySelect(rawInputMsg{Hit: rawHit{Kind: "node", NodeRow: int(xRow)}}, tr)
	if sel, _, _, _, _, _, _, _, ok := md.NodeUIStateFor("x"); !ok || sel != 1 {
		t.Fatalf("NodeUIStateFor(x) after applySelect = (selected=%v,ok=%v), want (1,true)", sel, ok)
	}
	waitForNodeStream(t, buf, func(selected, hovered uint8) bool { return selected == 1 })

	// --- Hover: setHover (gesture.go) is the shared dedupe+write hover path. ---
	md.setHover("x", "", false, tr)
	if _, hov, _, _, _, _, _, _, ok := md.NodeUIStateFor("x"); !ok || hov != 1 {
		t.Fatalf("NodeUIStateFor(x) after setHover = (hovered=%v,ok=%v), want (1,true)", hov, ok)
	}
	waitForNodeStream(t, buf, func(selected, hovered uint8) bool { return hovered == 1 })

	// --- Abc-drag: recordAbcDrag (quantized_move.go's neighborSetCRequantize call site)
	// runs on the RECIPIENT's own nodeMover goroutine, concurrently with every other
	// recipient — call it directly here (same call this package's own production code
	// makes) on neighbor "t", and check the published map + cumulative count.
	before := md.AbcDragCount()
	md.recordAbcDrag("t", 1, 2, 3)
	if got, want := md.AbcDragCount(), before+1; got != want {
		t.Fatalf("AbcDragCount after recordAbcDrag = %d, want %d", got, want)
	}
	if _, _, _, got, _, dA, dB, dC, ok := md.NodeUIStateFor("t"); !ok || got != 1 || dA != 1 || dB != 2 || dC != 3 {
		t.Fatalf("NodeUIStateFor(t) after recordAbcDrag = (gotDragMsg=%v,dA=%v,dB=%v,dC=%v,ok=%v), want (1,1,2,3,true)",
			got, dA, dB, dC, ok)
	}

	// AbcDragReset (via resetAbcDrag) re-scopes the recipient SET, leaving the count alone.
	md.resetAbcDrag()
	if got, want := md.AbcDragCount(), before+1; got != want {
		t.Fatalf("AbcDragCount after resetAbcDrag = %d, want %d (count is not drag-scoped)", got, want)
	}
	if _, _, _, got, _, _, _, _, ok := md.NodeUIStateFor("t"); !ok || got != 0 {
		t.Fatalf("NodeUIStateFor(t) after resetAbcDrag = gotDragMsg=%v, want 0", got)
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
			t.Fatalf("node x's dedicated stream frame never reflected the expected UI state within deadline")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
