package Buffer

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestSnapshotFade verifies KindFade seeds drive the Node/Edge Faded columns via the
// fixpoint, and that a faded edge's transit bead is suppressed (Live=0).
func TestSnapshotFade(t *testing.T) {
	s := NewSnapshotState(nil)

	// Two nodes A→B joined by edge-1.
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "A", Radius: 1})
	s.Update(T.Event{Kind: T.KindNodeGeometry, Node: "B", Radius: 1})
	s.Update(T.Event{Kind: T.KindGeometry, Edge: "edge-1", Node: "A", Target: "B"})
	// A send establishes the A/out → B route so bead suppression can resolve the edge.
	s.Update(T.Event{Kind: T.KindSend, Node: "A", Port: "out", Target: "B", Bead: 1})
	// One in-flight bead on A/out.
	s.Update(T.Event{Kind: T.KindPosition, Node: "A", Port: "out", Value: 1, F: 0.5, Bead: 1})

	nodeRow := func(snap []byte, row int, col int) byte {
		return snap[BufHeaderSize+BufBeadStride*int(readU32(snap, 4))+row*BufNodeStride+col]
	}
	edgeFaded := func(snap []byte) byte {
		beadN := int(readU32(snap, 4))
		nodeN := int(readU32(snap, 8))
		off := BufHeaderSize + beadN*BufBeadStride + nodeN*BufNodeStride +
			nodeN*BufInteriorSlotsPerNode*BufInteriorStride
		return snap[off+BufEdgeColFaded]
	}
	beadLive := func(snap []byte) byte { return snap[BufHeaderSize+BufBeadColLive] }

	// Before any fade: nothing faded, bead live.
	snap := s.BuildSnapshot()
	if nodeRow(snap, 0, BufNodeColFaded) != 0 || nodeRow(snap, 1, BufNodeColFaded) != 0 {
		t.Fatalf("no nodes should be faded initially")
	}
	if edgeFaded(snap) != 0 {
		t.Fatalf("edge should not be faded initially")
	}
	if beadLive(snap) != 1 {
		t.Fatalf("bead should be live initially")
	}

	// Fade node A: edge-1 fades (rule 1), B auto-fades (rule 3), bead suppressed.
	s.Update(T.Event{Kind: T.KindFade, FadedNodes: []string{"A"}})
	snap = s.BuildSnapshot()
	if nodeRow(snap, 0, BufNodeColFaded) != 1 {
		t.Errorf("node A should be faded")
	}
	if nodeRow(snap, 1, BufNodeColFaded) != 1 {
		t.Errorf("node B should auto-fade")
	}
	if edgeFaded(snap) != 1 {
		t.Errorf("edge-1 should be faded")
	}
	if beadLive(snap) != 0 {
		t.Errorf("faded edge's transit bead should be suppressed (Live=0)")
	}

	// Clear fade: everything restores, bead live again.
	s.Update(T.Event{Kind: T.KindFade})
	snap = s.BuildSnapshot()
	if nodeRow(snap, 0, BufNodeColFaded) != 0 || edgeFaded(snap) != 0 || beadLive(snap) != 1 {
		t.Errorf("clearing fade should restore node/edge/bead")
	}
}
