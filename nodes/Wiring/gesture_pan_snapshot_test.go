package Wiring

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
)

// TestWheelPanReachesSnapshotCameraColumns pins the full pan data path the new-system
// buffer camera depends on: a plain-wheel raw-input event → gestWheel → PanViewpoint →
// EmitViewpoint → Buffer.SnapshotState (the same sink main.go wires) → the snapshot's
// Camera PX/PY/PZ columns. If the FSM stops emitting the panned pivot, or the snapshot
// drops it, BufferCamera can never move, so this asserts the pivot lands in the columns.
func TestWheelPanReachesSnapshotCameraColumns(t *testing.T) {
	var snapOut bytes.Buffer
	snapState := B.NewSnapshotState(&snapOut)
	tr := T.NewWithSinkHook(256, io.Discard, snapState.Update)

	// Seed a valid (non-degenerate) viewpoint, then pan.
	md := newGestureMD(canonicalViewpoint())
	ev := rawEvent("wheel", 400, 300)
	ev.DeltaX = 40
	ev.DeltaY = 0
	md.HandleRawInput(ev, nil, tr)
	tr.Close()

	frames := splitBufferFrames(t, snapOut.Bytes())
	if len(frames) == 0 {
		t.Fatal("pan emitted no buffer snapshot frames")
	}
	last := frames[len(frames)-1]
	beadCount := binary.LittleEndian.Uint32(last[4:])
	nodeCount := binary.LittleEndian.Uint32(last[8:])
	edgeCount := binary.LittleEndian.Uint32(last[12:])
	off := B.BufHeaderSize +
		int(beadCount)*B.BufBeadStride +
		int(nodeCount)*B.BufNodeStride +
		int(edgeCount)*B.BufEdgeStride
	px := math.Float32frombits(binary.LittleEndian.Uint32(last[off+B.BufCameraColPX:]))

	// gestWheel seeds pivot to region-focus (0,0,90) then slides +X by the wheel delta,
	// so the snapshot pivot.X must be non-zero and equal the FSM's pivot.X.
	if math.Abs(float64(px)-md.vp.pivot.X) > 1e-4 {
		t.Fatalf("snapshot pivot.X=%v does not match FSM pivot.X=%v", px, md.vp.pivot.X)
	}
	if px == 0 {
		t.Fatalf("pan pivot did not reach the snapshot camera columns (px=0)")
	}
}

// TestUnseededViewpointPanIsDegenerate documents the root cause of the "pan does nothing"
// bug on the new-system path: from the ZERO viewpoint (pos and up both at θ0,φ0, which the
// FSM starts in when no camera seed ever arrives), gestWheel's pan keeps pos parallel to up.
// basisFromViewpoint then collapses (refX = up × pole = 0), so the pan slides the pivot in a
// degenerate frame and BufferCamera renders a degenerate camera. This is why the new path
// must seed a real pose on load (see shouldAutoFitCamera / BufferCameraAutoFit). Contrast
// with a seeded viewpoint, where pos and up stay perpendicular.
func TestUnseededViewpointPanIsDegenerate(t *testing.T) {
	// Zero viewpoint: pos = up = {Theta:0, Phi:0} → both map to +Y → parallel.
	md := newGestureMD(viewpoint{})
	ev := rawEvent("wheel", 400, 300)
	ev.DeltaX = 40
	md.HandleRawInput(ev, nil, nil)

	posW := anglesToWorldOffset(1, md.vp.pos.Theta, md.vp.pos.Phi)
	upW := anglesToWorldOffset(1, md.vp.up.Theta, md.vp.up.Phi)
	// Degenerate: |pos × up| ≈ 0 (parallel), the collapsed-basis condition.
	if cross := posW.cross(upW).length(); cross > 1e-9 {
		t.Fatalf("expected degenerate (parallel pos/up) from zero viewpoint, |pos×up|=%v", cross)
	}

	// A seeded viewpoint keeps a valid (non-degenerate) basis after the same pan.
	md2 := newGestureMD(canonicalViewpoint())
	md2.HandleRawInput(ev, nil, nil)
	posW2 := anglesToWorldOffset(1, md2.vp.pos.Theta, md2.vp.pos.Phi)
	upW2 := anglesToWorldOffset(1, md2.vp.up.Theta, md2.vp.up.Phi)
	if cross := posW2.cross(upW2).length(); cross < 1e-6 {
		t.Fatalf("seeded viewpoint should keep a valid basis, but |pos×up|=%v", cross)
	}
}
