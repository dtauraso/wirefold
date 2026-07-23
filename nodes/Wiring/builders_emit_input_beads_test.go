package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// builders_emit_input_beads_test.go — emitInputBeads streams a gate's two held inputs as
// interior beads: LEFT at slot (0,0) with negative-x offset, RIGHT at slot (0,1) with
// positive-x offset. A value of -1 means "not held" → present=false. Row is always 0.

func TestEmitInputBeadsBothHeld(t *testing.T) {
	tr := T.New(0)
	var snap nodeBeadSnapshot
	emitInputBeads(tr, "G", 1, 0, captureInteriorSnapshot(&snap))

	// slot 0 == (row 0, col 0) == left; slot 1 == (row 0, col 1) == right.
	if snap.present[0] == 0 {
		t.Fatal("no left interior bead (0,0) present")
	}
	if snap.value[0] != 1 || snap.ox[0] >= 0 {
		t.Fatalf("left bead: value=%v x=%v, want value=1 x<0", snap.value[0], snap.ox[0])
	}
	if snap.present[1] == 0 {
		t.Fatal("no right interior bead (0,1) present")
	}
	if snap.value[1] != 0 || snap.ox[1] <= 0 {
		t.Fatalf("right bead: value=%v x=%v, want value=0 x>0", snap.value[1], snap.ox[1])
	}
	// Offsets are symmetric about the node center.
	if snap.ox[0] != -snap.ox[1] {
		t.Fatalf("left/right x offsets not symmetric: %v vs %v", snap.ox[0], snap.ox[1])
	}
	for _, e := range snap.events {
		if e.Kind != T.KindNodeBead {
			t.Fatalf("unexpected event kind: %+v", e)
		}
	}
}

// TestEmitInputBeadsNotHeld: a -1 input marks the slot empty (present=false).
func TestEmitInputBeadsNotHeld(t *testing.T) {
	tr := T.New(0)
	var snap nodeBeadSnapshot
	emitInputBeads(tr, "G", -1, 5, captureInteriorSnapshot(&snap))

	if snap.present[0] != 0 {
		t.Fatalf("left bead with -1 input: present=true, want present=false")
	}
	if snap.present[1] == 0 || snap.value[1] != 5 {
		t.Fatalf("right bead: present=%v value=%v, want present=true value=5", snap.present[1] != 0, snap.value[1])
	}
}
