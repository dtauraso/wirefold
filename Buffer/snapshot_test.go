// Buffer/snapshot_test.go — round-trip parse test for SnapshotState.
//
// Feeds a SnapshotState a sequence of trace events, calls BuildSnapshot, then
// parses the raw bytes back and asserts counts and sampled values match.
// This is the Phase 2 acceptance check: if the snapshot builder, framing, and
// layout constants are consistent the test passes; any mismatch means a bug in
// the column offsets or the builder loop.

package Buffer

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// readF32 decodes one little-endian float32 from buf at off.
func readF32(buf []byte, off int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(buf[off:]))
}

// readU32 decodes one little-endian uint32 from buf at off.
func readU32(buf []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(buf[off:])
}

// readI32 decodes one little-endian int32 from buf at off.
func readI32(buf []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(buf[off:]))
}

// TestSnapshotRoundTrip feeds known events into a SnapshotState, builds a
// snapshot, then parses the bytes and asserts all counts and sampled field
// values are correct and match the layout constants in buffer_layout_gen.go.
func TestSnapshotRoundTrip(t *testing.T) {
	s := NewSnapshotState(nil) // no output; just test the builder

	// Register two nodes via KindNodeGeometry events.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-A",
		NX: 1.0, NY: 2.0, NZ: 3.0,
		Radius: 0.5, SphereR: 0.25,
	})
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-B",
		NX: 4.0, NY: 5.0, NZ: 6.0,
		Radius: 0.75, SphereR: 0.375,
	})

	// Register one edge via KindGeometry.
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "edge-1",
		SX: 1.1, SY: 2.2, SZ: 3.3,
		EX: 4.4, EY: 5.5, EZ: 6.6,
	})

	// Set camera.
	s.Update(T.Event{
		Kind: T.KindCamera,
		PX:   10.0, PY: 11.0, PZ: 12.0,
		R: 20.0, PosTheta: 0.5, PosPhi: 1.0,
		UpTheta: 0.25, UpPhi: 0.75,
	})

	// Toggle two overlay flags.
	s.Update(T.Event{Kind: T.KindSceneTori, Visible: true})
	s.Update(T.Event{Kind: T.KindDoubleLinks, Visible: true})

	// Inject a send event to build the srcToDest mapping (so arrive can route EvArrive).
	s.Update(T.Event{
		Kind: T.KindSend, Node: "node-A", Port: "out", Value: 42,
		Target: "node-B",
	})

	// Inject a live bead via KindPosition (also triggers a snapshot emit internally,
	// but we call BuildSnapshot separately below to inspect).
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "node-A", Port: "out",
		X: 2.5, Y: 3.5, Z: 4.5, Value: 42, F: 0.6, Bead: 1,
	})

	// Set a transient event on node-A (recv) and node-B (arrive via srcToDest lookup).
	s.Update(T.Event{Kind: T.KindRecv, Node: "node-A", Port: "in", Value: 42})
	s.Update(T.Event{Kind: T.KindArrive, Node: "node-A", Port: "out", Value: 42, Bead: 2})

	// Build snapshot WITHOUT triggering emit (BuildSnapshot is exported for tests).
	snap := s.BuildSnapshot()

	// ── Header ───────────────────────────────────────────────────────────────
	if len(snap) < BufHeaderSize {
		t.Fatalf("snapshot too short: got %d bytes, want >= %d", len(snap), BufHeaderSize)
	}

	beadCount := readU32(snap, 4)
	nodeCount := readU32(snap, 8)
	edgeCount := readU32(snap, 12)

	if beadCount != 1 {
		t.Errorf("beadCount: got %d, want 1", beadCount)
	}
	if nodeCount != 2 {
		t.Errorf("nodeCount: got %d, want 2", nodeCount)
	}
	if edgeCount != 1 {
		t.Errorf("edgeCount: got %d, want 1", edgeCount)
	}

	// ── Size check ───────────────────────────────────────────────────────────
	wantSize := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		BufCameraStride +
		BufOverlayStride
	if len(snap) != wantSize {
		t.Errorf("snapshot size: got %d, want %d", len(snap), wantSize)
	}

	// ── Bead block ───────────────────────────────────────────────────────────
	beadOff := BufHeaderSize
	// One bead was injected (Bead=1, node-A/out).
	gotX := readF32(snap, beadOff+BufBeadColX)
	gotY := readF32(snap, beadOff+BufBeadColY)
	gotZ := readF32(snap, beadOff+BufBeadColZ)
	gotVal := readI32(snap, beadOff+BufBeadColValue)
	gotFrac := readF32(snap, beadOff+BufBeadColFrac)
	gotID := readU32(snap, beadOff+BufBeadColBeadID)
	gotLive := snap[beadOff+BufBeadColLive]

	if gotX != 2.5 {
		t.Errorf("bead.X: got %v, want 2.5", gotX)
	}
	if gotY != 3.5 {
		t.Errorf("bead.Y: got %v, want 3.5", gotY)
	}
	if gotZ != 4.5 {
		t.Errorf("bead.Z: got %v, want 4.5", gotZ)
	}
	if gotVal != 42 {
		t.Errorf("bead.Value: got %v, want 42", gotVal)
	}
	if gotFrac != float32(0.6) {
		t.Errorf("bead.Frac: got %v, want 0.6", gotFrac)
	}
	if gotID != 1 {
		t.Errorf("bead.BeadID: got %v, want 1", gotID)
	}
	if gotLive != 1 {
		t.Errorf("bead.Live: got %v, want 1", gotLive)
	}

	// ── Node block ───────────────────────────────────────────────────────────
	nodeOff := BufHeaderSize + int(beadCount)*BufBeadStride

	// node-A (row 0): geometry + evRecv=1 (set above) + evArrive=0 (arrive was on dest node-B)
	nA := nodeOff
	if readF32(snap, nA+BufNodeColCX) != 1.0 {
		t.Errorf("nodeA.CX: got %v, want 1.0", readF32(snap, nA+BufNodeColCX))
	}
	if readF32(snap, nA+BufNodeColRadius) != 0.5 {
		t.Errorf("nodeA.Radius: got %v, want 0.5", readF32(snap, nA+BufNodeColRadius))
	}
	if snap[nA+BufNodeColEvRecv] != 1 {
		t.Errorf("nodeA.EvRecv: got %v, want 1", snap[nA+BufNodeColEvRecv])
	}
	if snap[nA+BufNodeColEvArrive] != 0 {
		t.Errorf("nodeA.EvArrive: got %v, want 0 (arrive routes to dest node-B)", snap[nA+BufNodeColEvArrive])
	}

	// node-B (row 1): geometry + evArrive=1 (arrive routed from node-A/out → node-B)
	nB := nodeOff + BufNodeStride
	if readF32(snap, nB+BufNodeColCX) != 4.0 {
		t.Errorf("nodeB.CX: got %v, want 4.0", readF32(snap, nB+BufNodeColCX))
	}
	if snap[nB+BufNodeColEvArrive] != 1 {
		t.Errorf("nodeB.EvArrive: got %v, want 1", snap[nB+BufNodeColEvArrive])
	}

	// ── Edge block ───────────────────────────────────────────────────────────
	// The Interior block (nodeCount×BufInteriorSlotsPerNode rows) sits between the
	// node and edge blocks, so the edge offset must skip it.
	edgeOff := nodeOff + int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride
	if readF32(snap, edgeOff+BufEdgeColSX) != float32(1.1) {
		t.Errorf("edge.SX: got %v, want ~1.1", readF32(snap, edgeOff+BufEdgeColSX))
	}
	if readF32(snap, edgeOff+BufEdgeColEZ) != float32(6.6) {
		t.Errorf("edge.EZ: got %v, want ~6.6", readF32(snap, edgeOff+BufEdgeColEZ))
	}

	// ── Camera block ─────────────────────────────────────────────────────────
	camOff := edgeOff + int(edgeCount)*BufEdgeStride
	if readF32(snap, camOff+BufCameraColPX) != 10.0 {
		t.Errorf("camera.PX: got %v, want 10.0", readF32(snap, camOff+BufCameraColPX))
	}
	if readF32(snap, camOff+BufCameraColR) != 20.0 {
		t.Errorf("camera.R: got %v, want 20.0", readF32(snap, camOff+BufCameraColR))
	}

	// ── Overlay block ────────────────────────────────────────────────────────
	ovOff := camOff + BufCameraStride
	if snap[ovOff+BufOverlayColSceneTori] != 1 {
		t.Errorf("overlay.SceneTori: got %v, want 1", snap[ovOff+BufOverlayColSceneTori])
	}
	if snap[ovOff+BufOverlayColDoubleLinks] != 1 {
		t.Errorf("overlay.DoubleLinks: got %v, want 1", snap[ovOff+BufOverlayColDoubleLinks])
	}
	if snap[ovOff+BufOverlayColScenePoles] != 0 {
		t.Errorf("overlay.ScenePoles: got %v, want 0", snap[ovOff+BufOverlayColScenePoles])
	}
}

// parseFrameStream reads all framed snapshots from raw and returns their payloads.
// Each frame is [len:u32-LE][payload]; returns an error string on malformed input.
func parseFrameStream(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	off := 0
	for off < len(raw) {
		if off+4 > len(raw) {
			t.Errorf("truncated frame header at offset %d", off)
			return frames
		}
		frameLen := int(readU32(raw, off))
		off += 4
		if off+frameLen > len(raw) {
			t.Errorf("truncated frame payload at offset %d (want %d bytes)", off, frameLen)
			return frames
		}
		frames = append(frames, raw[off:off+frameLen])
		off += frameLen
	}
	return frames
}

// TestSnapshotFraming verifies that the framed binary output produced by
// emitSnapshot has the correct [len:u32-LE][snapshot] structure and that the
// output stream may contain multiple frames (one per state-change event).
func TestSnapshotFraming(t *testing.T) {
	var buf bytes.Buffer
	s := NewSnapshotState(&buf)

	// KindNodeGeometry, KindGeometry, and KindPosition each trigger emitSnapshot,
	// so the stream will contain 3 frames when all three events are processed.
	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "node-X",
		NX: 0, NY: 0, NZ: 0, Radius: 1.0, SphereR: 0.5,
	})
	s.Update(T.Event{
		Kind: T.KindGeometry, Edge: "edge-X",
		SX: 0, SY: 0, SZ: 0, EX: 1, EY: 0, EZ: 0,
	})
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "node-X", Port: "out",
		X: 0.5, Y: 0, Z: 0, Value: 1, F: 0.5, Bead: 7,
	})

	frames := parseFrameStream(t, buf.Bytes())
	if len(frames) != 3 {
		t.Fatalf("expected 3 frames (node-geometry + edge-geometry + position), got %d", len(frames))
	}

	// The last frame (position trigger) must carry 1 bead, 1 node, 1 edge.
	payload := frames[len(frames)-1]
	beadCount := readU32(payload, 4)
	nodeCount := readU32(payload, 8)
	edgeCount := readU32(payload, 12)

	if beadCount != 1 {
		t.Errorf("last frame beadCount: got %d, want 1", beadCount)
	}
	if nodeCount != 1 {
		t.Errorf("last frame nodeCount: got %d, want 1", nodeCount)
	}
	if edgeCount != 1 {
		t.Errorf("last frame edgeCount: got %d, want 1", edgeCount)
	}

	wantSize := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		int(nodeCount)*BufInteriorSlotsPerNode*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		BufCameraStride + BufOverlayStride
	if len(payload) != wantSize {
		t.Errorf("last frame size: got %d, want %d", len(payload), wantSize)
	}
}

// TestTransientFlagsCleared verifies that transient event flags are reset to
// zero after each snapshot emit (they must not bleed into subsequent ticks).
func TestTransientFlagsCleared(t *testing.T) {
	s := NewSnapshotState(nil)

	s.Update(T.Event{
		Kind: T.KindNodeGeometry, Node: "n1",
		NX: 0, NY: 0, NZ: 0, Radius: 1, SphereR: 0.5,
	})

	// Set evFire on n1 and emit (via position).
	s.Update(T.Event{Kind: T.KindFire, Node: "n1"})
	s.Update(T.Event{
		Kind: T.KindPosition, Node: "n1", Port: "out",
		X: 0, Y: 0, Z: 0, Value: 0, F: 0.5, Bead: 1,
	})

	// After the position-triggered snapshot, transients should be cleared.
	// Build a second snapshot immediately (without setting any event flags).
	snap := s.BuildSnapshot()
	nodeOff := BufHeaderSize + BufBeadStride // 1 live bead
	if snap[nodeOff+BufNodeColEvFire] != 0 {
		t.Errorf("EvFire not cleared after snapshot: got %v, want 0", snap[nodeOff+BufNodeColEvFire])
	}
}

// TestInteriorBeadReachesSnapshot verifies that KindNodeBead events land in the
// Interior block: slot = row*2 + col, present/value/offset are packed at the right
// columns, empty slots stay present=0, and a present=false event clears a popped slot.
func TestInteriorBeadReachesSnapshot(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})

	// (row 0, col 0) → slot 0: present value 1 at local offset (2,-3,4).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: true, Value: 1, X: 2, Y: -3, Z: 4})
	// (row 1, col 0) → slot 2: present value 0 (a valid black bead) at (1,1,1).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 1, Col: 0, Present: true, Value: 0, X: 1, Y: 1, Z: 1})

	snap := s.BuildSnapshot()
	// Interior block starts right after the single node row (no beads).
	interiorOff := BufHeaderSize + 1*BufNodeStride
	slot := func(i int) int { return interiorOff + i*BufInteriorStride }

	if snap[slot(0)+BufInteriorColPresent] != 1 {
		t.Errorf("slot0.Present: got %d, want 1", snap[slot(0)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(0)+BufInteriorColValue); v != 1 {
		t.Errorf("slot0.Value: got %d, want 1", v)
	}
	if x := readF32(snap, slot(0)+BufInteriorColOX); x != 2 {
		t.Errorf("slot0.OX: got %v, want 2", x)
	}
	if y := readF32(snap, slot(0)+BufInteriorColOY); y != -3 {
		t.Errorf("slot0.OY: got %v, want -3", y)
	}
	if z := readF32(snap, slot(0)+BufInteriorColOZ); z != 4 {
		t.Errorf("slot0.OZ: got %v, want 4", z)
	}
	// Slot 1 (row 0, col 1) was never emitted → absent.
	if snap[slot(1)+BufInteriorColPresent] != 0 {
		t.Errorf("slot1.Present: got %d, want 0 (never emitted)", snap[slot(1)+BufInteriorColPresent])
	}
	// Slot 2 (row 1, col 0): present, value 0.
	if snap[slot(2)+BufInteriorColPresent] != 1 {
		t.Errorf("slot2.Present: got %d, want 1", snap[slot(2)+BufInteriorColPresent])
	}
	if v := readI32(snap, slot(2)+BufInteriorColValue); v != 0 {
		t.Errorf("slot2.Value: got %d, want 0", v)
	}

	// A present=false event on slot 0 clears it (popped bead disappears).
	s.Update(T.Event{Kind: T.KindNodeBead, Node: "n1", Row: 0, Col: 0, Present: false, Value: 0, X: 2, Y: -3, Z: 4})
	snap = s.BuildSnapshot()
	if snap[slot(0)+BufInteriorColPresent] != 0 {
		t.Errorf("slot0.Present after pop: got %d, want 0", snap[slot(0)+BufInteriorColPresent])
	}
}

// TestSelectionPersistsAndIsExclusive verifies KindSelect marks exactly one node's
// Selected column, that it PERSISTS across snapshots (unlike transient event flags),
// and that a later select on another node moves the flag (exclusive), while node=""
// clears it entirely.
func TestSelectionPersistsAndIsExclusive(t *testing.T) {
	s := NewSnapshotState(nil)
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n1", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "n2", Radius: 1})

	n1 := BufHeaderSize // no beads, no need to offset
	n2 := n1 + BufNodeStride

	// Select n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n1"})
	snap := s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after select n1: n1.Selected=%d n2.Selected=%d want 1,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Persists: a fresh snapshot with no further select keeps n1 selected.
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 1 {
		t.Fatalf("selection did not persist: n1.Selected=%d want 1", snap[n1+BufNodeColSelected])
	}

	// Exclusive: selecting n2 clears n1.
	s.Update(T.Event{Kind: T.KindSelect, Node: "n2"})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 1 {
		t.Fatalf("after select n2: n1.Selected=%d n2.Selected=%d want 0,1", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}

	// Clear: node="" deselects all.
	s.Update(T.Event{Kind: T.KindSelect, Node: ""})
	snap = s.BuildSnapshot()
	if snap[n1+BufNodeColSelected] != 0 || snap[n2+BufNodeColSelected] != 0 {
		t.Fatalf("after clear: n1.Selected=%d n2.Selected=%d want 0,0", snap[n1+BufNodeColSelected], snap[n2+BufNodeColSelected])
	}
}
