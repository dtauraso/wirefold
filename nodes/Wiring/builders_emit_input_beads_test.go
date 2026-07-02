package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// builders_emit_input_beads_test.go — emitInputBeads streams a gate's two held inputs as
// interior beads: LEFT at slot (0,0) with negative-x offset, RIGHT at slot (0,1) with
// positive-x offset. A value of -1 means "not held" → Present=false. Row is always 0.

func nodeBeadAt(events []T.Event, node string, row, col int) (T.Event, bool) {
	for _, e := range events {
		if e.Kind == T.KindNodeBead && e.Node == node && e.Row == row && e.Col == col {
			return e, true
		}
	}
	return T.Event{}, false
}

func TestEmitInputBeadsBothHeld(t *testing.T) {
	tr := T.New(16)
	emitInputBeads(tr, "G", 1, 0)
	tr.Close()
	evs := tr.Events()

	left, ok := nodeBeadAt(evs, "G", 0, 0)
	if !ok {
		t.Fatal("no left interior bead (0,0) emitted")
	}
	if !left.Present || left.Value != 1 || left.X >= 0 {
		t.Fatalf("left bead: present=%v value=%v x=%v, want present=true value=1 x<0", left.Present, left.Value, left.X)
	}
	right, ok := nodeBeadAt(evs, "G", 0, 1)
	if !ok {
		t.Fatal("no right interior bead (0,1) emitted")
	}
	if !right.Present || right.Value != 0 || right.X <= 0 {
		t.Fatalf("right bead: present=%v value=%v x=%v, want present=true value=0 x>0", right.Present, right.Value, right.X)
	}
	// Offsets are symmetric about the node center.
	if left.X != -right.X {
		t.Fatalf("left/right x offsets not symmetric: %v vs %v", left.X, right.X)
	}
}

// TestEmitInputBeadsNotHeld: a -1 input marks the slot empty (Present=false).
func TestEmitInputBeadsNotHeld(t *testing.T) {
	tr := T.New(16)
	emitInputBeads(tr, "G", -1, 5)
	tr.Close()
	evs := tr.Events()

	left, ok := nodeBeadAt(evs, "G", 0, 0)
	if !ok || left.Present {
		t.Fatalf("left bead with -1 input: present=%v, want present=false (found=%v)", left.Present, ok)
	}
	right, ok := nodeBeadAt(evs, "G", 0, 1)
	if !ok || !right.Present || right.Value != 5 {
		t.Fatalf("right bead: present=%v value=%v, want present=true value=5", right.Present, right.Value)
	}
}
