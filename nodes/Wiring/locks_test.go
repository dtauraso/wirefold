package Wiring

import (
	"math"
	"testing"
)

// Record #1: node-2 mirror. applyMirrorLocks writes node 7 to share node 3's θ about
// node 2 and the opposite φ, keeping node 7's own radius. Tested synchronously (the
// RootMove fan to movers is async; this checks the lock math directly).
func TestMirrorLockNode2(t *testing.T) {
	md := &MoveDispatch{}
	md.addMirror("2", "3", "7")
	md.addMirror("2", "7", "3")

	c2 := vec3{0, 0, 0}
	n3new := vec3{6, 8, 3} // node 3 dragged here
	n7old := vec3{9, -1, -5}
	pos := func(id string) (vec3, bool) {
		switch id {
		case "2":
			return c2, true
		case "3":
			return n3new, true
		case "7":
			return n7old, true
		}
		return vec3{}, false
	}
	r7before := surfaceCoord(c2, n7old).R

	out := md.applyMirrorLocks("3", pos)
	n7, ok := out["7"]
	if !ok {
		t.Fatal("mirror did not write node 7")
	}
	const eps = 1e-9
	p3 := surfaceCoord(c2, n3new)
	p7 := surfaceCoord(c2, n7)
	if math.Abs(p7.Theta-p3.Theta) > eps {
		t.Errorf("θ not shared: 7=%v 3=%v", p7.Theta, p3.Theta)
	}
	if math.Abs(p7.Phi+p3.Phi) > eps {
		t.Errorf("φ not mirrored: 7=%v 3=%v (sum want 0)", p7.Phi, p3.Phi)
	}
	if math.Abs(p7.R-r7before) > eps {
		t.Errorf("node 7 radius changed: %v -> %v", r7before, p7.R)
	}
	// Dragging node 7 mirrors node 3 (the other direction is registered).
	if out2 := md.applyMirrorLocks("7", pos); len(out2) == 0 || func() bool { _, k := out2["3"]; return !k }() {
		t.Error("reverse direction (drag 7 -> write 3) did not fire")
	}
}
