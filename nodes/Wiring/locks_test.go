package Wiring

// locks_test.go — Active flag semantics: applyPolarEqs skips inactive equations,
// ToggleLockActive flips the flag, DeleteSelectedLock only deletes a deactivated equation.

import "testing"

// polarLockTestMD builds a MoveDispatch with a Center↔A and Center↔B link, each seeded
// with a polar so applyPolarEqs has real polar state to read/write.
func polarLockTestMD() *MoveDispatch {
	md := &MoveDispatch{selectedLockIndex: -1}
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
	md := &MoveDispatch{selectedLockIndex: -1}
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
	md.selectedLockIndex = 0

	// Active equation: delete is refused.
	md.DeleteSelectedLock(nil)
	if len(md.polarEqsSnap()) != 1 {
		t.Fatalf("DeleteSelectedLock deleted an ACTIVE equation: polarEqs=%v", md.polarEqsSnap())
	}

	// Deactivate, then delete succeeds and clears the focus.
	eqs := md.polarEqsSnap()
	eqs[0].Active = false
	md.setPolarEqs(eqs)
	md.DeleteSelectedLock(nil)
	if len(md.polarEqsSnap()) != 0 {
		t.Fatalf("DeleteSelectedLock on a deactivated equation left polarEqs=%v, want empty", md.polarEqsSnap())
	}
	if md.selectedLockIndex != -1 {
		t.Fatalf("selectedLockIndex=%d after delete, want -1", md.selectedLockIndex)
	}
}

func TestSelectLockClampsOutOfRange(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true}})

	md.SelectLock(0, nil)
	if md.selectedLockIndex != 0 {
		t.Fatalf("SelectLock(0): selectedLockIndex=%d want 0", md.selectedLockIndex)
	}
	md.SelectLock(9, nil)
	if md.selectedLockIndex != -1 {
		t.Fatalf("SelectLock(9) out-of-range: selectedLockIndex=%d want -1", md.selectedLockIndex)
	}
}

// Re-selecting the already-selected equation toggles it OFF (unhighlights it, which also
// clears the diagram guide overlay that follows selectedLockIndex).
func TestSelectLockTogglesOffOnReselect(t *testing.T) {
	md := polarLockTestMD()
	md.setPolarEqs([]polarEq{
		{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true},
		{Center: "Center", A: polarTerm{Node: "C", Comp: compPhi, Sign: 1}, B: polarTerm{Node: "D", Comp: compPhi, Sign: 1}, Active: true},
	})

	md.SelectLock(1, nil)
	if md.selectedLockIndex != 1 {
		t.Fatalf("SelectLock(1): selectedLockIndex=%d want 1", md.selectedLockIndex)
	}
	md.SelectLock(1, nil) // click the same row again → toggle off
	if md.selectedLockIndex != -1 {
		t.Fatalf("SelectLock(1) re-select: selectedLockIndex=%d want -1 (toggled off)", md.selectedLockIndex)
	}
	md.SelectLock(0, nil) // selecting a different row still selects normally
	if md.selectedLockIndex != 0 {
		t.Fatalf("SelectLock(0) after toggle-off: selectedLockIndex=%d want 0", md.selectedLockIndex)
	}
}
