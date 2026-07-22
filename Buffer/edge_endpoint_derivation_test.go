// Buffer/edge_endpoint_derivation_test.go — regression test for the edge-tube drift fix:
// the Edge block's SX..EZ must be DERIVED from the Port block's live port positions at
// buildSnapshot time, not read from whatever the edge's own goroutine last emitted. A node
// drag emits fresh node-geometry (moving the port) every cycle; the edge only re-emits its
// own geometry a cycle later, so trusting the edge's stored endpoint perpetually lags the
// port sphere by one drag-step. See CLAUDE.md / the task description for the full model.
package Buffer

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// edgeBlockRowOffset returns the byte offset of edge row `row` in a built snapshot, given
// the header counts already decoded — same arithmetic as pack_test.go's TestEventBlockPopulate.
func edgeBlockRowOffset(snap []byte, row int) int {
	beadCount := int(readU32(snap, 4))
	nodeCount := int(readU32(snap, 8))
	off := BufHeaderSize +
		beadCount*BufBeadStride +
		nodeCount*BufNodeStride +
		nodeCount*BufInteriorSlotsPerNode*BufInteriorStride
	return off + row*BufEdgeStride
}

// TestEdgeEndpointTracksPortAcrossStaleEmit is the regression proof: a dest node's IN-port
// moves from P0 to P1 via a NEW node-geometry event with NO subsequent edge-geometry event,
// and the Edge block's END must read P1 (derived from the live port), not the stale P0 the
// edge's own last geometry emit carried.
func TestEdgeEndpointTracksPortAcrossStaleEmit(t *testing.T) {
	s := NewSnapshotState(nil)

	// Source node "src" with one OUT port "out" at (0,0,0).
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "src", Radius: 1,
		Ports: []T.PortGeom{{Name: "out", IsInput: false, PX: 0, PY: 0, PZ: 0}}})
	// Dest node "dst" with one IN port "in" at P0 = (1,1,1).
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "dst", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, PX: 1, PY: 1, PZ: 1}}})

	// Edge geometry event establishing the edge, endpoints from the initial port positions
	// (mirrors main.go's real EdgeSeeds()/live emit: SX..EZ == the port world positions).
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "src", Target: "dst",
		SrcPort: "out", DstPort: "in",
		SX: 0, SY: 0, SZ: 0, EX: 1, EY: 1, EZ: 1})

	// Drag: the dest port moves to P1 = (5,6,7) via a NEW node-geometry event — WITHOUT any
	// subsequent edge-geometry event (the edge goroutine hasn't caught up yet).
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "dst", Radius: 1,
		Ports: []T.PortGeom{{Name: "in", IsInput: true, PX: 5, PY: 6, PZ: 7}}})

	snap := s.BuildSnapshot()
	off := edgeBlockRowOffset(snap, 0)
	gotEX := readF32(snap, off+12) // SX,SY,SZ,EX,EY,EZ float32 each = EX at +12
	gotEY := readF32(snap, off+16)
	gotEZ := readF32(snap, off+20)

	if gotEX != 5 || gotEY != 6 || gotEZ != 7 {
		t.Fatalf("edge END: got (%v,%v,%v), want (5,6,7) (derived from the live port, not the stale P0=(1,1,1) from the edge's last geometry emit)",
			gotEX, gotEY, gotEZ)
	}
}

// TestEdgeEndpointFallsBackWhenPortUnresolved proves the fallback path: an edge whose
// endpoint node/port hasn't registered yet must still serialize its own stored sx..ez
// (edges can register before their endpoint nodes do — see onEdgeGeometry).
func TestEdgeEndpointFallsBackWhenPortUnresolved(t *testing.T) {
	s := NewSnapshotState(nil)

	// Edge registers FIRST, before either endpoint node exists.
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "e1", Node: "src", Target: "dst",
		SrcPort: "out", DstPort: "in",
		SX: 2, SY: 3, SZ: 4, EX: 9, EY: 8, EZ: 7})

	snap := s.BuildSnapshot()
	off := edgeBlockRowOffset(snap, 0)
	gotSX := readF32(snap, off+0)
	gotSY := readF32(snap, off+4)
	gotSZ := readF32(snap, off+8)
	gotEX := readF32(snap, off+12)
	gotEY := readF32(snap, off+16)
	gotEZ := readF32(snap, off+20)

	if gotSX != 2 || gotSY != 3 || gotSZ != 4 {
		t.Fatalf("edge START (unresolved endpoint): got (%v,%v,%v), want fallback (2,3,4)", gotSX, gotSY, gotSZ)
	}
	if gotEX != 9 || gotEY != 8 || gotEZ != 7 {
		t.Fatalf("edge END (unresolved endpoint): got (%v,%v,%v), want fallback (9,8,7)", gotEX, gotEY, gotEZ)
	}
}
