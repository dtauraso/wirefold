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
	md.polarEqs = []polarEq{{
		Center: "Center",
		A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "B", Comp: compTheta, Sign: 1},
		Active: false,
	}}
	out := md.applyPolarEqs("A", posFn(md))
	if len(out) != 0 {
		t.Fatalf("applyPolarEqs on an inactive equation wrote %v, want no writes", out)
	}

	md.polarEqs[0].Active = true
	out = md.applyPolarEqs("A", posFn(md))
	if _, ok := out["B"]; !ok {
		t.Fatalf("applyPolarEqs on an active equation wrote %v, want a write to B", out)
	}
}

func TestToggleLockActive(t *testing.T) {
	md := polarLockTestMD()
	md.polarEqs = []polarEq{{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true}}

	md.ToggleLockActive(0, nil)
	if md.polarEqs[0].Active {
		t.Fatalf("after ToggleLockActive(0): Active=true, want false")
	}
	md.ToggleLockActive(0, nil)
	if !md.polarEqs[0].Active {
		t.Fatalf("after second ToggleLockActive(0): Active=false, want true")
	}
	// Out-of-range index is a no-op, not a panic.
	md.ToggleLockActive(5, nil)
}

func TestDeleteSelectedLockGuard(t *testing.T) {
	md := polarLockTestMD()
	md.polarEqs = []polarEq{
		{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true},
	}
	md.selectedLockIndex = 0

	// Active equation: delete is refused.
	md.DeleteSelectedLock(nil)
	if len(md.polarEqs) != 1 {
		t.Fatalf("DeleteSelectedLock deleted an ACTIVE equation: polarEqs=%v", md.polarEqs)
	}

	// Deactivate, then delete succeeds and clears the focus.
	md.polarEqs[0].Active = false
	md.DeleteSelectedLock(nil)
	if len(md.polarEqs) != 0 {
		t.Fatalf("DeleteSelectedLock on a deactivated equation left polarEqs=%v, want empty", md.polarEqs)
	}
	if md.selectedLockIndex != -1 {
		t.Fatalf("selectedLockIndex=%d after delete, want -1", md.selectedLockIndex)
	}
}

func TestSelectLockClampsOutOfRange(t *testing.T) {
	md := polarLockTestMD()
	md.polarEqs = []polarEq{{Center: "Center", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: 1}, Active: true}}

	md.SelectLock(0, nil)
	if md.selectedLockIndex != 0 {
		t.Fatalf("SelectLock(0): selectedLockIndex=%d want 0", md.selectedLockIndex)
	}
	md.SelectLock(9, nil)
	if md.selectedLockIndex != -1 {
		t.Fatalf("SelectLock(9) out-of-range: selectedLockIndex=%d want -1", md.selectedLockIndex)
	}
}
