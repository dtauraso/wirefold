package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestEmitNodeBeadsPositions verifies that emitNodeBeads streams one node-bead
// event per PRESENT bead with positions matching interiorSlotPos for the right
// (row,col): top row (0) = backup, bottom row (1) = working. A popped/absent bead
// is omitted (fewer events). Discrete positions snap to the grid slots.
func TestEmitNodeBeadsPositions(t *testing.T) {
	g := nodeGeom{Kind: "Input", Pos: vec3{X: 100, Y: 200, Z: 0}}

	// Full state: working=[1,0], backup=[1,0] → 4 beads.
	tr := T.New(0)
	emitNodeBeads(tr, "in", g, []int{1, 0}, []int{1, 0})
	tr.Close()
	full := tr.Events()
	if len(full) != 4 {
		t.Fatalf("full state: got %d node-bead events, want 4", len(full))
	}

	// Each event's position must equal interiorSlotPos(row,col), and value must
	// equal the slice value at that slot.
	type slot struct{ row, col int }
	wantVal := map[slot]int{
		{0, 0}: 1, {0, 1}: 0, // backup row
		{1, 0}: 1, {1, 1}: 0, // working row
	}
	seen := map[slot]bool{}
	for _, e := range full {
		if e.Kind != T.KindNodeBead || e.Node != "in" {
			t.Fatalf("unexpected event: %+v", e)
		}
		s := slot{e.Row, e.Col}
		seen[s] = true
		if e.Value != wantVal[s] {
			t.Errorf("slot %+v: value=%d, want %d", s, e.Value, wantVal[s])
		}
		p := interiorSlotPos(g, e.Row, e.Col)
		if e.X != p.X || e.Y != p.Y || e.Z != p.Z {
			t.Errorf("slot %+v: pos=(%v,%v,%v), want (%v,%v,%v)", s, e.X, e.Y, e.Z, p.X, p.Y, p.Z)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct slots, got %d", len(seen))
	}

	// After one pop: working=[1] (end 0 removed) → 3 beads (working col 1 absent).
	tr2 := T.New(0)
	emitNodeBeads(tr2, "in", g, []int{1}, []int{1, 0})
	tr2.Close()
	if got := len(tr2.Events()); got != 3 {
		t.Fatalf("after pop: got %d node-bead events, want 3", got)
	}
}

// TestInteriorSlotPosFormula pins the slotPos(row,col) formula:
//
//	x = cx + (col-0.5)*colGap ; y = cy + (0.5-row)*rowGap ; z = cz
//
// with colGap = w*0.40, rowGap = h*0.40 and (cx,cy,cz) = nodeWorldPos.
func TestInteriorSlotPosFormula(t *testing.T) {
	g := nodeGeom{Kind: "Input", Pos: vec3{X: 100, Y: 200, Z: 5}}
	w, h := kindWidthHeight("Input") // 80, 60
	colGap := w * interiorColGapFrac // 32
	rowGap := h * interiorRowGapFrac // 24
	c := nodeWorldPos(g)

	cases := []struct{ row, col int }{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for _, tc := range cases {
		got := interiorSlotPos(g, tc.row, tc.col)
		wantX := c.X + (float64(tc.col)-0.5)*colGap
		wantY := c.Y + (0.5-float64(tc.row))*rowGap
		if got.X != wantX || got.Y != wantY || got.Z != c.Z {
			t.Errorf("slot(%d,%d) = (%v,%v,%v), want (%v,%v,%v)", tc.row, tc.col, got.X, got.Y, got.Z, wantX, wantY, c.Z)
		}
	}
}
