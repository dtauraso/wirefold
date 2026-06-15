package Wiring

import "testing"

func TestLatticeToWorldOrigin(t *testing.T) {
	x, y, z := latticeToWorld(0, 0, 0)
	if x != 0 || y != 0 || z != 0 {
		t.Errorf("latticeToWorld(0,0,0) = (%v,%v,%v), want (0,0,0)", x, y, z)
	}
}

func TestLatticeToWorldScaled(t *testing.T) {
	x, y, z := latticeToWorld(1, 2, -3)
	wantX, wantY, wantZ := latticeSpacing, 2*latticeSpacing, -3*latticeSpacing
	if x != wantX || y != wantY || z != wantZ {
		t.Errorf("latticeToWorld(1,2,-3) = (%v,%v,%v), want (%v,%v,%v)", x, y, z, wantX, wantY, wantZ)
	}
}

func TestLatticeToWorldClamp(t *testing.T) {
	x, y, z := latticeToWorld(100, -100, 5)
	// 100 clamps to latticeHalf, -100 clamps to -latticeHalf, 5 stays 5
	wantX := float64(latticeHalf) * latticeSpacing
	wantY := -float64(latticeHalf) * latticeSpacing
	wantZ := float64(5) * latticeSpacing
	if x != wantX || y != wantY || z != wantZ {
		t.Errorf("latticeToWorld(100,-100,5) = (%v,%v,%v), want (%v,%v,%v)", x, y, z, wantX, wantY, wantZ)
	}
}

func TestWorldToLatticeRoundClamp(t *testing.T) {
	i, j, k := worldToLattice(latticeSpacing*2, 0, latticeSpacing*100)
	// k=100 clamps to latticeHalf
	if i != 2 || j != 0 || k != latticeHalf {
		t.Errorf("worldToLattice = (%v,%v,%v), want (2,0,%d)", i, j, k, latticeHalf)
	}

	// 0.48*spacing rounds to 0
	i2, j2, k2 := worldToLattice(0.48*latticeSpacing, 0, 0)
	if i2 != 0 || j2 != 0 || k2 != 0 {
		t.Errorf("worldToLattice(0.48*spacing,0,0) = (%v,%v,%v), want (0,0,0)", i2, j2, k2)
	}

	// 0.61*spacing rounds to 1
	i3, j3, k3 := worldToLattice(0.61*latticeSpacing, 0, 0)
	if i3 != 1 || j3 != 0 || k3 != 0 {
		t.Errorf("worldToLattice(0.61*spacing,0,0) = (%v,%v,%v), want (1,0,0)", i3, j3, k3)
	}
}

func TestWorldToLatticeRoundTrip(t *testing.T) {
	ci, cj, ck := 3, -4, 5
	x, y, z := latticeToWorld(ci, cj, ck)
	i, j, k := worldToLattice(x, y, z)
	if i != ci || j != cj || k != ck {
		t.Errorf("round-trip (%v,%v,%v) -> world -> (%v,%v,%v)", ci, cj, ck, i, j, k)
	}
}

func TestNodeCellResolvesViaLattice(t *testing.T) {
	cell := [3]int{1, 0, 0}
	g := nodeGeom{Kind: "Input", Cell: &cell}
	pos := nodeWorldPos(g)
	if pos.X != latticeSpacing || pos.Y != 0 || pos.Z != 0 {
		t.Errorf("nodeWorldPos with Cell{1,0,0} = (%v,%v,%v), want (%v,0,0)", pos.X, pos.Y, pos.Z, latticeSpacing)
	}
}

func TestNodeNilCellDefaultsToOrigin(t *testing.T) {
	// Cell is the only node-position model; a nil Cell falls back to cell {0,0,0},
	// which latticeToWorld maps to the world origin.
	g := nodeGeom{Kind: "Input", Cell: nil}
	pos := nodeWorldPos(g)
	if pos.X != 0 || pos.Y != 0 || pos.Z != 0 {
		t.Errorf("nodeWorldPos nil-cell fallback = (%v,%v,%v), want origin (0,0,0)", pos.X, pos.Y, pos.Z)
	}
}
