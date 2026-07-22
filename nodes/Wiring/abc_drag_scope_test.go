package Wiring

// abc_drag_scope_test.go — proves the abc-drag drag-log (Buffer.SnapshotState's
// current-drag-scoped recipient set + per-node gotDragMsg buffer bit) is SCOPED TO THE
// CURRENT DRAG, not accumulated across the whole session. Regression test for the bug
// where dragging node A logged its real recipients, then dragging a DIFFERENT node B
// kept A's stale recipients in the set alongside B's — the fix is a KindAbcDragReset
// (Trace/Trace.go) emitted once at the real drag-start edge (the gesture FSM's
// pending→dragging transition, nodes/Wiring/gesture.go) before the neighborSetC fan,
// which Buffer.SnapshotState.Update clears the recipient set and every node's
// gotDragMsg bit on. This test drives md.RootMove directly (not the gesture FSM), so it
// calls tr.AbcDragReset() itself before each RootMove to stand in for that drag-start
// edge.
//
// Two disjoint node pairs (x→t,x→n and y→z) let us drag x (recipients {t,n}) then drag y
// (recipient {z}) and assert the FINAL buffer state's gotDragMsg set is exactly {z} — if
// the reset were missing (old accumulating behavior), {t,n} would still show gotDragMsg=1.
//
// SnapshotState methods (BuildSnapshot, LookupNodeRow, ...) must all be called from the
// single Trace-drain goroutine (see Buffer/snapshot.go's doc comment) — calling them from
// this test's goroutine while the drain goroutine is concurrently calling Update would be
// a data race. So this test never touches the SnapshotState directly: it captures the
// FRAMED binary frames the drain goroutine writes (via a mutex-guarded io.Writer, exactly
// like production's fd3 stream) and decodes the last complete frame's bytes on this
// goroutine instead — those bytes are an immutable snapshot copy once written.

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	B "github.com/dtauraso/wirefold/Buffer"
	T "github.com/dtauraso/wirefold/Trace"
)

// writeXTNY extends writeXTN's x/t/n topology with a second, DISJOINT pair y→z (no edge
// connecting it to x/t/n), so dragging y fans neighborSetC to z only.
func writeXTNY(t *testing.T) string {
	t.Helper()
	root := writeXTN(t)
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	mk("nodes/y/meta.json", `{"id":"y","type":"SrcNode","r":100,"scenePolarR":60,"scenePolarTheta":0.5,"scenePolarPhi":2.0}`)
	mk("nodes/y/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/z/meta.json", `{"id":"z","type":"SinkNode","r":100,"scenePolarR":70,"scenePolarTheta":1.4,"scenePolarPhi":-0.6}`)
	mk("nodes/z/inputs/In.json", `{"name":"In"}`)
	mk("edges/eYZ.json", `{"label":"eYZ","kind":"data","source":"y","sourceHandle":"Out","target":"z","targetHandle":"In"}`)
	mk("nodes/iso/meta.json", `{"id":"iso","type":"SrcNode","r":100,"scenePolarR":50,"scenePolarTheta":2.5,"scenePolarPhi":1.0}`)
	mk("nodes/iso/outputs/Out.json", `{"name":"Out"}`)
	return root
}

func readU32(buf []byte, off int) uint32 { return binary.LittleEndian.Uint32(buf[off:]) }

// lastFrame extracts the payload of the LAST complete [u32 len][payload] frame in raw.
// Returns nil if no complete frame is present yet.
// lastFrame returns the most recent complete fd-3 frame's SNAPSHOT bytes, with the
// leading blockTag byte stripped (frames are [len:u32-LE][blockTag:u8][block bytes],
// len counting the tag byte plus block bytes; today the sole tag is
// B.BufBlockTagScene, so stripping it here is equivalent to validating it).
func lastFrame(raw []byte) []byte {
	var last []byte
	off := 0
	for off+4 <= len(raw) {
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			break
		}
		if n >= 1 {
			last = raw[off+1 : off+n]
		}
		off += n
	}
	return last
}

// gotDragMsgSet decodes one snapshot payload's Node block (+ Label section for ids) and
// returns the set of node ids whose GotDragMsg bit is 1.
func gotDragMsgSet(snap []byte) map[string]bool {
	beadCount := int(readU32(snap, 4))
	nodeCount := int(readU32(snap, 8))
	edgeCount := int(readU32(snap, 12))
	portCount := int(readU32(snap, 16))
	layoutLinkCount := int(readU32(snap, 36))

	nodeOff := B.BufHeaderSize + beadCount*B.BufBeadStride
	interiorOff := nodeOff + nodeCount*B.BufNodeStride
	edgeOff := interiorOff + nodeCount*B.BufInteriorSlotsPerNode*B.BufInteriorStride
	layoutLinkOff := edgeOff + edgeCount*B.BufEdgeStride
	portOff := layoutLinkOff + layoutLinkCount*B.BufLayoutLinkStride
	cameraOff := portOff + portCount*B.BufPortStride
	overlayOff := cameraOff + B.BufCameraStride
	sceneOff := overlayOff + B.BufOverlayStride
	labelOff := sceneOff + B.BufSceneStride

	out := map[string]bool{}
	for row := 0; row < nodeCount; row++ {
		off := nodeOff + row*B.BufNodeStride
		if snap[off+B.BufNodeColGotDragMsg] != 1 {
			continue
		}
		lOff := int(readU32(snap, off+B.BufNodeColLabelOff))
		lLen := int(readU32(snap, off+B.BufNodeColLabelLen))
		id := string(snap[labelOff+lOff : labelOff+lOff+lLen])
		out[id] = true
	}
	return out
}

func readI32(buf []byte, off int) int32 { return int32(binary.LittleEndian.Uint32(buf[off:])) }

// nodeDragDeltas decodes one snapshot payload's Node block (+ Label section for ids) and
// returns each node id's (DragDeltaA, DragDeltaB, DragDeltaC) triple, keyed by id, for
// every node whose GotDragMsg bit is 1 (the drag-scoped recipient set).
func nodeDragDeltas(snap []byte) map[string][3]int32 {
	beadCount := int(readU32(snap, 4))
	nodeCount := int(readU32(snap, 8))
	edgeCount := int(readU32(snap, 12))
	portCount := int(readU32(snap, 16))
	layoutLinkCount := int(readU32(snap, 36))

	nodeOff := B.BufHeaderSize + beadCount*B.BufBeadStride
	interiorOff := nodeOff + nodeCount*B.BufNodeStride
	edgeOff := interiorOff + nodeCount*B.BufInteriorSlotsPerNode*B.BufInteriorStride
	layoutLinkOff := edgeOff + edgeCount*B.BufEdgeStride
	portOff := layoutLinkOff + layoutLinkCount*B.BufLayoutLinkStride
	cameraOff := portOff + portCount*B.BufPortStride
	overlayOff := cameraOff + B.BufCameraStride
	sceneOff := overlayOff + B.BufOverlayStride
	labelOff := sceneOff + B.BufSceneStride

	out := map[string][3]int32{}
	for row := 0; row < nodeCount; row++ {
		off := nodeOff + row*B.BufNodeStride
		if snap[off+B.BufNodeColGotDragMsg] != 1 {
			continue
		}
		lOff := int(readU32(snap, off+B.BufNodeColLabelOff))
		lLen := int(readU32(snap, off+B.BufNodeColLabelLen))
		id := string(snap[labelOff+lOff : labelOff+lOff+lLen])
		out[id] = [3]int32{
			readI32(snap, off+B.BufNodeColDragDeltaA),
			readI32(snap, off+B.BufNodeColDragDeltaB),
			readI32(snap, off+B.BufNodeColDragDeltaC),
		}
	}
	return out
}

func setEq(a map[string]bool, want ...string) bool {
	if len(a) != len(want) {
		return false
	}
	for _, w := range want {
		if !a[w] {
			return false
		}
	}
	return true
}

// TestAbcDragLogIsScopedToCurrentDrag drags x (recipients t,n), then drags the disjoint
// y (recipient z), and asserts the buffer's FINAL gotDragMsg set is {z} only — proving
// the previous drag's recipients (t,n) were cleared, not accumulated. Under the old
// accumulating behavior (no KindAbcDragReset), the set after dragging y would still
// contain t and n alongside z, and this assertion would fail.
func TestAbcDragLogIsScopedToCurrentDrag(t *testing.T) {
	root := writeXTNY(t)

	var frames syncBuffer
	snap := B.NewSnapshotState(&frames)
	tr := T.NewWithSinkHook(0, nil, snap.Update)

	_, _, md, _, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.EnableEditPersist(root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	xBefore, ok := md.centerOfNode("x")
	if !ok {
		t.Fatal("no center for x")
	}
	xTarget := xBefore.add(vec3{X: 55, Y: -20, Z: 30})
	// Stand in for the gesture FSM's pending→dragging transition, which is the real
	// drag-start edge that emits this reset (RootMove itself no longer does).
	tr.AbcDragReset()
	if !md.RootMove("x", xTarget) {
		t.Fatal("RootMove(x) returned false")
	}
	pollDragConverged(t, md, "x", xTarget)

	// Give x's neighborSetC fan (t, n) time to land and emit their gotDragMsg=1 frames
	// before dragging y, so the stale-recipient regression has something to leak.
	deadlineX := time.Now().Add(2 * time.Second)
	for {
		if snap := lastFrame([]byte(frames.String())); snap != nil {
			got := gotDragMsgSet(snap)
			if setEq(got, "t", "n") {
				break
			}
		}
		if time.Now().After(deadlineX) {
			t.Fatalf("t and n never both showed gotDragMsg=1 after dragging x")
		}
		time.Sleep(time.Millisecond)
	}

	// Drag y — a node fully disjoint from x/t/n — after x's drag has settled.
	yBefore, ok := md.centerOfNode("y")
	if !ok {
		t.Fatal("no center for y")
	}
	yTarget := yBefore.add(vec3{X: -25, Y: 40, Z: -10})
	tr.AbcDragReset()
	if !md.RootMove("y", yTarget) {
		t.Fatal("RootMove(y) returned false")
	}
	pollDragConverged(t, md, "y", yTarget)

	// Poll the frame stream until z (y's only neighbor) shows gotDragMsg=1 — the reset+fan
	// for y's drag has fully landed — then assert the set is EXACTLY {z}, not {t,n,z}.
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := lastFrame([]byte(frames.String()))
		if snap != nil {
			got := gotDragMsgSet(snap)
			if got["z"] {
				if !setEq(got, "z") {
					t.Fatalf("gotDragMsg set after dragging y must be exactly {z} (drag-scoped), got %v — stale recipients from x's earlier drag leaked across", got)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("z never showed gotDragMsg=1 after dragging y; final set: %v", got)
			}
		} else if time.Now().After(deadline) {
			t.Fatal("no snapshot frame captured after dragging y")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAbcDragResetAloneClearsGotDragMsgAndEmits drives B.SnapshotState.Update DIRECTLY,
// from this test's own goroutine, in isolation — no LoadTopology, no gesture FSM, no
// drain goroutine, no node whose OWN geometry re-emit could incidentally publish the
// cleared state. This is legitimate single-goroutine use of SnapshotState (its doc
// requires all methods run from one goroutine; this test's goroutine IS that goroutine
// here, since nothing else ever touches snap).
//
// It registers two node rows via synthetic KindNodeGeometry events, marks one with
// KindAbcDrag (so gotDragMsg=1 lands in a frame — the PRIOR non-empty state to prove
// gets cleared), records the frame count at that point, then sends ONE bare
// KindAbcDragReset event and NOTHING ELSE. Because no other event follows the reset,
// the ONLY thing that can produce a new frame or change the gotDragMsg set is the
// reset's own emit (Buffer/snapshot.go's KindAbcDragReset case) — unlike the previous
// version of this test, which dragged a real isolated node whose incidental geometry
// re-emit republished the already-cleared state regardless of whether the reset itself
// emitted anything (proven by commenting out that emit and observing the old test still
// passed).
func TestAbcDragResetAloneClearsGotDragMsgAndEmits(t *testing.T) {
	var frames syncBuffer
	snap := B.NewSnapshotState(&frames)

	snap.Update(T.Event{Kind: T.KindNodeGeometry, Node: "a", NodeKind: "SrcNode", Label: "a"})
	snap.Update(T.Event{Kind: T.KindNodeGeometry, Node: "b", NodeKind: "SinkNode", Label: "b"})
	snap.Update(T.Event{Kind: T.KindAbcDrag, Node: "a"})

	before := lastFrame([]byte(frames.String()))
	if before == nil {
		t.Fatal("no frame captured after registering nodes + one abc-drag mark")
	}
	gotBefore := gotDragMsgSet(before)
	if !setEq(gotBefore, "a") {
		t.Fatalf("gotDragMsg set before reset must be {a}, got %v", gotBefore)
	}
	framesBefore := frames.String()

	// The ONLY event from here on: the reset itself. Nothing else can produce a new
	// frame or clear the set.
	snap.Update(T.Event{Kind: T.KindAbcDragReset})

	framesAfter := frames.String()
	if framesAfter == framesBefore {
		t.Fatal("KindAbcDragReset produced no new frame — the reset's emit is missing")
	}
	after := lastFrame([]byte(framesAfter))
	if after == nil {
		t.Fatal("no frame captured after reset")
	}
	gotAfter := gotDragMsgSet(after)
	if len(gotAfter) != 0 {
		t.Fatalf("gotDragMsg set after a bare KindAbcDragReset must be empty, got %v", gotAfter)
	}
}

// TestAbcDragDeltaReachesBufferNodeColumns proves a KindAbcDrag event's DeltaA/B/C ride
// through to the recipient's DragDeltaA/B/C buffer Node columns exactly as sent —
// including the (0,0,0) case, which must still be a real recorded triple (not treated as
// absent — GotDragMsg is what distinguishes "no message" from "message with zero delta").
func TestAbcDragDeltaReachesBufferNodeColumns(t *testing.T) {
	var frames syncBuffer
	snap := B.NewSnapshotState(&frames)

	snap.Update(T.Event{Kind: T.KindNodeGeometry, Node: "a", NodeKind: "SrcNode", Label: "a"})
	snap.Update(T.Event{Kind: T.KindNodeGeometry, Node: "b", NodeKind: "SinkNode", Label: "b"})

	snap.Update(T.Event{Kind: T.KindAbcDrag, Node: "a", DeltaA: 3, DeltaB: -7, DeltaC: 2})
	snap.Update(T.Event{Kind: T.KindAbcDrag, Node: "b", DeltaA: 0, DeltaB: 0, DeltaC: 0})

	frame := lastFrame([]byte(frames.String()))
	if frame == nil {
		t.Fatal("no frame captured after two abc-drag marks")
	}
	got := nodeDragDeltas(frame)
	if got["a"] != [3]int32{3, -7, 2} {
		t.Fatalf("node a's DragDelta columns should be (3,-7,2), got %v", got["a"])
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("node b should be a recipient (GotDragMsg=1) even with a (0,0,0) delta; recipients=%v", got)
	}
	if got["b"] != [3]int32{0, 0, 0} {
		t.Fatalf("node b's DragDelta columns should be (0,0,0), got %v", got["b"])
	}

	// KindAbcDragReset must clear the DragDelta columns alongside GotDragMsg.
	snap.Update(T.Event{Kind: T.KindAbcDragReset})
	after := lastFrame([]byte(frames.String()))
	if after == nil {
		t.Fatal("no frame captured after reset")
	}
	gotAfter := nodeDragDeltas(after)
	if len(gotAfter) != 0 {
		t.Fatalf("DragDelta recipients after KindAbcDragReset must be empty, got %v", gotAfter)
	}
}
