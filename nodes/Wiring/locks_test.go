package Wiring

// locks_test.go — Active flag semantics: applyPolarEqs skips inactive equations,
// ToggleLockActive flips the flag, DeleteSelectedLock only deletes a deactivated equation.

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// polarLockTestMD builds a MoveDispatch with a Center↔A and Center↔B link, each seeded
// with a polar so applyPolarEqs has real polar state to read/write.
func polarLockTestMD() *MoveDispatch {
	md := &MoveDispatch{}
	md.addLink("Center", "A")
	md.addLink("Center", "B")
	ca := md.linkBetween("Center", "A")
	ca.setPolar("Center", "A", polar{R: 10, Theta: 1.0, Phi: 0})
	cb := md.linkBetween("Center", "B")
	cb.setPolar("Center", "B", polar{R: 10, Theta: 0.4, Phi: 0})
	return md
}

func posFn(md *MoveDispatch) func(string) (vec3, bool) {
	return func(id string) (vec3, bool) {
		if id == "Center" {
			return vec3{0, 0, 0}, true
		}
		return vec3{}, false
	}
}

func TestApplyPolarEqsSkipsInactive(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{{
		Center: "Center",
		A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "B", Comp: compTheta, Sign: 1},
		Active: false,
	}})
	out := md.applyPolarEqs("A", posFn(md))
	if len(out) != 0 {
		t.Fatalf("applyPolarEqs on an inactive equation wrote %v, want no writes", out)
	}

	eqs := md.polarEqsSnap()
	eqs[0].Active = true
	md.setPolarEqs(eqs)
	out = md.applyPolarEqs("A", posFn(md))
	if _, ok := out["B"]; !ok {
		t.Fatalf("applyPolarEqs on an active equation wrote %v, want a write to B", out)
	}
}

// TestEnsureEqLinksMakesUnlinkedEquationApply reproduces the "equation doesn't apply after
// the second side is set" report: an equation whose Center↔term pairs have no movement link
// (no topology edge) silently no-ops, and ensureEqLinks fixes it by creating those links.
func TestEnsureEqLinksMakesUnlinkedEquationApply(t *testing.T) {
	md := &MoveDispatch{}
	eq := polarEq{
		Center: "C",
		A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "B", Comp: compTheta, Sign: 1},
		Active: true,
	}
	md.setPolarEqs([]polarEq{eq})
	center := func(id string) (vec3, bool) { return vec3{}, id == "C" }

	// Before: no links → nothing is written even though the equation is active.
	if out := md.applyPolarEqs("A", center); len(out) != 0 {
		t.Fatalf("expected no writes without links, got %v", out)
	}

	md.ensureEqLinks(eq)
	if md.linkBetween("C", "A") == nil || md.linkBetween("C", "B") == nil {
		t.Fatal("ensureEqLinks did not create the Center↔term links")
	}
	// Seed the links' polar as a drag-edge refresh would, then the equation applies.
	md.linkBetween("C", "A").setPolar("C", "A", polar{R: 10, Theta: 1.0})
	md.linkBetween("C", "B").setPolar("C", "B", polar{R: 10, Theta: 0.4})
	if out := md.applyPolarEqs("A", center); out["B"] == (vec3{}) && len(out) == 0 {
		t.Fatalf("equation still did not apply after ensureEqLinks: %v", out)
	}

	// A degenerate Center==term must not create a self-link.
	md.ensureLink("C", "C")
	if md.linkBetween("C", "C") != nil {
		t.Fatal("ensureLink created a Center==node self-link")
	}
}

func TestToggleLockActive(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true}})

	md.ToggleLockActive(0, nil)
	if md.polarEqsSnap()[0].Active {
		t.Fatalf("after ToggleLockActive(0): Active=true, want false")
	}
	md.ToggleLockActive(0, nil)
	if !md.polarEqsSnap()[0].Active {
		t.Fatalf("after second ToggleLockActive(0): Active=false, want true")
	}
	// Out-of-range index is a no-op, not a panic.
	md.ToggleLockActive(5, nil)
}

func TestDeleteSelectedLockGuard(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{
		{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true},
	})
	md.selectedLocks = []int{0}

	// Active equation: delete is refused.
	md.DeleteSelectedLock(nil)
	if len(md.polarEqsSnap()) != 1 {
		t.Fatalf("DeleteSelectedLock deleted an ACTIVE equation: polarEqs=%v", md.polarEqsSnap())
	}

	// Deactivate, then delete succeeds and clears the selection.
	eqs := md.polarEqsSnap()
	eqs[0].Active = false
	md.setPolarEqs(eqs)
	md.DeleteSelectedLock(nil)
	if len(md.polarEqsSnap()) != 0 {
		t.Fatalf("DeleteSelectedLock on a deactivated equation left polarEqs=%v, want empty", md.polarEqsSnap())
	}
	if len(md.selectedLocks) != 0 {
		t.Fatalf("selectedLocks=%v after delete, want empty", md.selectedLocks)
	}
}

func TestSelectLockClampsOutOfRange(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true}})

	md.SelectLock(0, nil)
	if want := []int{0}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("SelectLock(0): selectedLocks=%v want %v", md.selectedLocks, want)
	}
	md.SelectLock(9, nil)
	if want := []int{0}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("SelectLock(9) out-of-range: selectedLocks=%v want %v (unchanged)", md.selectedLocks, want)
	}
}

// Re-selecting the already-selected equation toggles it OFF (unhighlights it, which also
// clears the diagram guide overlay that follows selectedLocks).
func TestSelectLockTogglesOffOnReselect(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{
		{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true},
		{Center: "Center", A: polarTerm{Node: "C", Comp: compPhi, Sign: 1}, B: polarTerm{Node: "D", Comp: compPhi, Sign: 1}, Active: true},
	})

	md.SelectLock(1, nil)
	if want := []int{1}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("SelectLock(1): selectedLocks=%v want %v", md.selectedLocks, want)
	}
	md.SelectLock(1, nil) // click the same row again → toggle off
	if want := []int{}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("SelectLock(1) re-select: selectedLocks=%v want empty (toggled off)", md.selectedLocks)
	}
	md.SelectLock(0, nil) // selecting a different row still selects normally
	if want := []int{0}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("SelectLock(0) after toggle-off: selectedLocks=%v want %v", md.selectedLocks, want)
	}
}

// TestSelectLockMultiSelect verifies selecting two locks marks BOTH as selected (ordered),
// and that both stream Selected=1 in the emitted PolarLock payload.
func TestSelectLockMultiSelect(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{
		{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true},
		{Center: "Center", A: polarTerm{Node: "C", Comp: compPhi, Sign: 1}, B: polarTerm{Node: "D", Comp: compPhi, Sign: 1}, Active: true},
	})

	md.SelectLock(0, nil)
	md.SelectLock(1, nil)
	if want := []int{0, 1}; !slicesEqualInt(md.selectedLocks, want) {
		t.Fatalf("after selecting 0 then 1: selectedLocks=%v want %v", md.selectedLocks, want)
	}

	tr := T.New(8)
	md.emitPolarLocks(tr)
	tr.Close()
	events := tr.Events()
	if len(events) != 1 || len(events[0].PolarLocks) != 2 {
		t.Fatalf("emitPolarLocks: events=%+v want one KindPolarLocks event with 2 locks", events)
	}
	locks := events[0].PolarLocks
	if !locks[0].Selected || !locks[1].Selected {
		t.Fatalf("PolarLocks=%+v want both rows Selected=true", locks)
	}
}

// slicesEqualInt reports whether a and b contain the same ints in the same order.
func slicesEqualInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
