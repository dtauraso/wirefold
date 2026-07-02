package Wiring

import (
	"testing"
)

// links_test.go — the movementLink polar accessors (polarOf/setPolar) round-trip in both
// endpoint orientations, and MoveDispatch.linkBetween finds a registered link regardless of
// argument order (returning nil when absent).

func TestMovementLinkPolarRoundTrip(t *testing.T) {
	lk := movementLink{A: "a", B: "b"}
	pB := polar{R: 10, Theta: 1.0, Phi: 0.5}  // B seen from A
	pA := polar{R: 10, Theta: 2.0, Phi: -0.5} // A seen from B

	lk.setPolar("a", "b", pB) // frame a, node b → BfromA
	lk.setPolar("b", "a", pA) // frame b, node a → AfromB

	got, ok := lk.polarOf("a", "b")
	if !ok || got != pB {
		t.Fatalf("polarOf(a,b): got %+v ok=%v, want %+v", got, ok, pB)
	}
	got, ok = lk.polarOf("b", "a")
	if !ok || got != pA {
		t.Fatalf("polarOf(b,a): got %+v ok=%v, want %+v", got, ok, pA)
	}
	// A pair not on this link reports ok=false.
	if _, ok := lk.polarOf("a", "c"); ok {
		t.Fatal("polarOf(a,c) reported ok on a non-matching pair")
	}
}

func TestLinkBetween(t *testing.T) {
	md := &MoveDispatch{}
	md.addLink("1", "2")
	md.addLink("2", "3")

	// Found in either orientation.
	if lk := md.linkBetween("1", "2"); lk == nil || !(lk.A == "1" && lk.B == "2") {
		t.Fatalf("linkBetween(1,2) did not find the 1↔2 link: %+v", lk)
	}
	if lk := md.linkBetween("3", "2"); lk == nil || !lk.touches("3") || !lk.touches("2") {
		t.Fatalf("linkBetween(3,2) did not find the 2↔3 link: %+v", lk)
	}
	// Absent pair → nil.
	if lk := md.linkBetween("1", "3"); lk != nil {
		t.Fatalf("linkBetween(1,3): expected nil, got %+v", lk)
	}
}
