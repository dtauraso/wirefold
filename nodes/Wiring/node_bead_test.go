package Wiring

import (
	"math"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestEmitNodeBeadsPositions verifies that emitNodeBeads streams a 4-SLOT SNAPSHOT
// (all rows {0,1} × cols {0,1}) with positions matching interiorSlotPos for each
// (row,col): top row (0) = backup, bottom row (1) = working. A popped/absent slot is
// emitted with present=false (not omitted) so TS can clear it. Always 4 events.
func TestEmitNodeBeadsPositions(t *testing.T) {
	g := nodeGeom{Kind: "Input", Pos: vec3{X: 100, Y: 200, Z: 0}}

	// Full state: working=[1,0], backup=[1,0] → 4 present slots.
	tr := T.New(0)
	emitNodeBeads(tr, "in", g, []int{1, 0}, []int{1, 0})
	tr.Close()
	full := tr.Events()
	if len(full) != 4 {
		t.Fatalf("full state: got %d node-bead events, want 4", len(full))
	}

	// Each event's position must equal interiorSlotPos(row,col); present must be
	// true for every slot, and value must equal the slice value at that slot.
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
		if !e.Present {
			t.Errorf("slot %+v: present=false, want true", s)
		}
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

	// After one pop: working=[1] (end 0 removed). Still a 4-slot snapshot, but the
	// working col-1 slot is now present=false; the other 3 are present=true.
	tr2 := T.New(0)
	emitNodeBeads(tr2, "in", g, []int{1}, []int{1, 0})
	tr2.Close()
	ev := tr2.Events()
	if len(ev) != 4 {
		t.Fatalf("after pop: got %d node-bead events, want 4 (snapshot)", len(ev))
	}
	for _, e := range ev {
		emptySlot := e.Row == 1 && e.Col == 1
		if emptySlot && e.Present {
			t.Errorf("popped slot (1,1): present=true, want false")
		}
		if !emptySlot && !e.Present {
			t.Errorf("slot (%d,%d): present=false, want true", e.Row, e.Col)
		}
	}
}

// TestInteriorSlotPosFormula pins the torus-aware slotPos(row,col) formula:
//
//	slot  = interiorTorusOuterR + interiorBeadGap/2 ; pitch = 2*slot
//	x = cx + (col-0.5)*pitch ; y = cy + (0.5-row)*pitch ; z = cz
//
// with (cx,cy,cz) = nodeWorldPos. Pitch follows bead size, not node radius.
func TestInteriorSlotPosFormula(t *testing.T) {
	g := nodeGeom{Kind: "Input", Pos: vec3{X: 100, Y: 200, Z: 5}}
	pitch := 2 * interiorSlot
	c := nodeWorldPos(g)

	cases := []struct{ row, col int }{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for _, tc := range cases {
		got := interiorSlotPos(g, tc.row, tc.col)
		wantX := c.X + (float64(tc.col)-0.5)*pitch
		wantY := c.Y + (0.5-float64(tc.row))*pitch
		if got.X != wantX || got.Y != wantY || got.Z != c.Z {
			t.Errorf("slot(%d,%d) = (%v,%v,%v), want (%v,%v,%v)", tc.row, tc.col, got.X, got.Y, got.Z, wantX, wantY, c.Z)
		}
	}
}

// TestInteriorBeadsInsideSphere asserts each of the 4 interior bead's TORUS reach
// stays inside the node sphere: dist(center, slot) + interiorTorusOuterR ≤ r.
// The torus (outer radius rt), not the sphere, is the bead's true visual extent.
func TestInteriorBeadsInsideSphere(t *testing.T) {
	rt := interiorTorusOuterR
	g := nodeGeom{Kind: "Input", Pos: vec3{X: 100, Y: 200, Z: 0}}
	r := nodeRadius("Input")
	center := nodeWorldPos(g)
	cases := []struct{ row, col int }{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for _, tc := range cases {
		p := interiorSlotPos(g, tc.row, tc.col)
		dx, dy, dz := p.X-center.X, p.Y-center.Y, p.Z-center.Z
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		reach := dist + rt
		if reach > r {
			t.Errorf("slot(%d,%d): torus reach %v > r %v — ring pokes outside sphere", tc.row, tc.col, reach, r)
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
