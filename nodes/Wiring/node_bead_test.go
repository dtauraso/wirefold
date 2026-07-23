package Wiring

import (
	"io"
	"math"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// nodeBeadSnapshot captures one interiorStream.write() call's full 4-slot arrays plus
// its row-resolved RowEvents, so a test can assert on emitNodeBeads/emitHeldBead/
// emitInputBeads' output without a real fd. Slot index == row*2+col (see
// interiorStream's doc comment).
type nodeBeadSnapshot struct {
	present    []uint8
	value      []int32
	ox, oy, oz []float32
	events     []RowEvent
}

func captureInteriorSnapshot(snap *nodeBeadSnapshot) *interiorStream {
	return &interiorStream{
		out: io.Discard,
		buildFrame: func(tick uint32, present []uint8, value []int32, ox, oy, oz []float32, evs []RowEvent) []byte {
			snap.present, snap.value = present, value
			snap.ox, snap.oy, snap.oz = ox, oy, oz
			snap.events = evs
			return nil
		},
	}
}

// TestEmitNodeBeadsPositions verifies that emitNodeBeads streams a 4-SLOT SNAPSHOT
// (all rows {0,1} × cols {0,1}) with positions matching interiorSlotPos for each
// (row,col): top row (0) = backup, bottom row (1) = working. A popped/absent slot is
// present=false (not omitted) so TS can clear it. Always a 4-element snapshot.
func TestEmitNodeBeadsPositions(t *testing.T) {
	// Full state: working=[1,0], backup=[1,0] → 4 present slots.
	tr := T.New(0)
	var snap nodeBeadSnapshot
	emitNodeBeads(tr, "in", []int{1, 0}, []int{1, 0}, captureInteriorSnapshot(&snap))
	if len(snap.present) != 4 {
		t.Fatalf("full state: got %d slots, want 4", len(snap.present))
	}
	if len(snap.events) != 4 {
		t.Fatalf("full state: got %d node-bead events, want 4", len(snap.events))
	}

	// Slot index == row*2+col: 0=(0,0) 1=(0,1) 2=(1,0) 3=(1,1). Position asserted from
	// the RowEvent's float64 X/Y/Z (not the ox/oy/oz float32 arrays, which are lossy).
	byslot := map[int32]RowEvent{}
	for _, e := range snap.events {
		byslot[e.Slot] = e
	}
	wantVal := []int32{1, 0, 1, 0}
	for slot := 0; slot < 4; slot++ {
		row, col := slot/2, slot%2
		if snap.present[slot] == 0 {
			t.Errorf("slot (%d,%d): present=false, want true", row, col)
		}
		if snap.value[slot] != wantVal[slot] {
			t.Errorf("slot (%d,%d): value=%d, want %d", row, col, snap.value[slot], wantVal[slot])
		}
		p := interiorSlotOffset(row, col)
		e, ok := byslot[int32(slot)]
		if !ok {
			t.Errorf("slot (%d,%d): no RowEvent", row, col)
			continue
		}
		if e.X != p.X || e.Y != p.Y || e.Z != p.Z {
			t.Errorf("slot (%d,%d): pos=(%v,%v,%v), want (%v,%v,%v)", row, col, e.X, e.Y, e.Z, p.X, p.Y, p.Z)
		}
	}
	for _, e := range snap.events {
		if e.Kind != T.KindNodeBead {
			t.Fatalf("unexpected event kind: %+v", e)
		}
	}

	// After one pop: working=[1] (end 0 removed). Still a 4-slot snapshot, but the
	// working col-1 slot (row=1,col=1 → slot 3) is now present=false; the other 3
	// are present=true.
	tr2 := T.New(0)
	var snap2 nodeBeadSnapshot
	emitNodeBeads(tr2, "in", []int{1}, []int{1, 0}, captureInteriorSnapshot(&snap2))
	if len(snap2.present) != 4 {
		t.Fatalf("after pop: got %d slots, want 4 (snapshot)", len(snap2.present))
	}
	for slot := 0; slot < 4; slot++ {
		emptySlot := slot == 3
		present := snap2.present[slot] != 0
		if emptySlot && present {
			t.Errorf("popped slot (1,1): present=true, want false")
		}
		if !emptySlot && !present {
			row, col := slot/2, slot%2
			t.Errorf("slot (%d,%d): present=false, want true", row, col)
		}
	}
}

// TestInteriorSlotOffsetFormula pins the torus-aware slotOffset(row,col) formula —
// a NODE-LOCAL offset centered at the origin (no node center added):
//
//	slot = interiorTorusOuterR + interiorBeadGap/2 ; pitch = 2*slot
//	dx = (col-0.5)*pitch ; dy = (0.5-row)*pitch ; dz = 0
//
// Pitch follows bead size, not node radius.
func TestInteriorSlotOffsetFormula(t *testing.T) {
	pitch := 2 * interiorSlot

	cases := []struct{ row, col int }{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for _, tc := range cases {
		got := interiorSlotOffset(tc.row, tc.col)
		wantX := (float64(tc.col) - 0.5) * pitch
		wantY := (0.5 - float64(tc.row)) * pitch
		if got.X != wantX || got.Y != wantY || got.Z != 0 {
			t.Errorf("slot(%d,%d) = (%v,%v,%v), want (%v,%v,0)", tc.row, tc.col, got.X, got.Y, got.Z, wantX, wantY)
		}
	}
}

// TestInteriorBeadsInsideSphere asserts each of the 4 interior bead's TORUS reach
// stays inside the node sphere: |offset| + interiorTorusOuterR ≤ r. Offsets are
// node-local (centered at origin), so the distance is measured from the origin.
// The torus (outer radius rt), not the sphere, is the bead's true visual extent.
func TestInteriorBeadsInsideSphere(t *testing.T) {
	rt := interiorTorusOuterR
	r := nodeRadius("Input")
	cases := []struct{ row, col int }{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for _, tc := range cases {
		p := interiorSlotOffset(tc.row, tc.col)
		dist := math.Sqrt(p.X*p.X + p.Y*p.Y + p.Z*p.Z)
		reach := dist + rt
		if reach > r {
			t.Errorf("slot(%d,%d): torus reach %v > r %v — ring pokes outside sphere", tc.row, tc.col, reach, r)
		}
	}
}

// TestInputBeadsInsideSphere asserts the two WindowAndInhibitRightGate side beads (at
// ±interiorSlot on x, vertically centered) keep their torus reach inside the
// node sphere: |offset| + interiorTorusOuterR ≤ nodeRadius("WindowAndInhibitRightGate").
func TestInputBeadsInsideSphere(t *testing.T) {
	rt := interiorTorusOuterR
	r := nodeRadius("WindowAndInhibitRightGate")
	for _, x := range []float64{-interiorSlot, interiorSlot} {
		dist := math.Abs(x)
		reach := dist + rt
		if reach > r {
			t.Errorf("side bead x=%v: torus reach %v > r %v — ring pokes outside sphere", x, reach, r)
		}
	}
}

// TestInteriorTorusesDoNotOverlap asserts adjacent same-row/col toruses keep a
// non-negative gap: pitch (2*slot) ≥ 2*rt, i.e. torus-to-torus gap ≥ 0.
func TestInteriorTorusesDoNotOverlap(t *testing.T) {
	pitch := 2 * interiorSlot
	gap := pitch - 2*interiorTorusOuterR
	if gap < 0 {
		t.Errorf("adjacent toruses overlap: pitch %v < 2*rt %v (gap %v)", pitch, 2*interiorTorusOuterR, gap)
	}
}
