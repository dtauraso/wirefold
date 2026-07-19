package Wiring

// abc_drag_scope_test.go — proves the abc-drag drag-log (Buffer.SnapshotState's sticky
// recipient set + per-node gotDragMsg buffer bit) is SCOPED TO THE CURRENT DRAG, not
// accumulated across the whole session. Regression test for the bug where dragging node
// A logged its real recipients, then dragging a DIFFERENT node B kept A's stale
// recipients in the set alongside B's — the fix is RootMove emitting KindAbcDragReset
// (Trace/Trace.go) before the neighborSetC fan, which Buffer.SnapshotState.Update clears
// the sticky set and every node's gotDragMsg bit on.
//
// Two disjoint node pairs (x→t,x→n and y→z) let us drag x (recipients {t,n}) then drag y
// (recipient {z}) and assert the FINAL buffer state's gotDragMsg set is exactly {z} — if
// the reset were missing (old sticky behavior), {t,n} would still show gotDragMsg=1.
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
	"os"
	"path/filepath"
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
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mk("nodes/y/meta.json", `{"id":"y","type":"FanInSrc","r":100,"scenePolarR":60,"scenePolarTheta":0.5,"scenePolarPhi":2.0}`)
	mk("nodes/y/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/z/meta.json", `{"id":"z","type":"FanInSink","r":100,"scenePolarR":70,"scenePolarTheta":1.4,"scenePolarPhi":-0.6}`)
	mk("nodes/z/inputs/In.json", `{"name":"In"}`)
	mk("edges/eYZ.json", `{"label":"eYZ","kind":"data","source":"y","sourceHandle":"Out","target":"z","targetHandle":"In"}`)
	return root
}

func readU32(buf []byte, off int) uint32 { return binary.LittleEndian.Uint32(buf[off:]) }

// lastFrame extracts the payload of the LAST complete [u32 len][payload] frame in raw.
// Returns nil if no complete frame is present yet.
func lastFrame(raw []byte) []byte {
	var last []byte
	off := 0
	for off+4 <= len(raw) {
		n := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+n > len(raw) {
			break
		}
		last = raw[off : off+n]
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
// sticky behavior (no KindAbcDragReset), the set after dragging y would still contain
// t and n alongside z, and this assertion would fail.
func TestAbcDragLogIsScopedToCurrentDrag(t *testing.T) {
	root := writeXTNY(t)

	var frames syncBuffer
	snap := B.NewSnapshotState(&frames)
	tr := T.NewWithSinkHook(0, nil, snap.Update)

	_, _, md, err := LoadTopology(context.Background(), root, tr, NewRealClock())
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
