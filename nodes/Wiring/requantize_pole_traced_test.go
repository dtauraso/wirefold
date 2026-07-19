package Wiring

// requantize_pole_traced_test.go — headless tests for the FIXED-INCREMENT /
// STORED-INDEX requantize model (memory/feedback_abc_times_constant_not_rederive.md,
// docs/demos/polar-drag-3d.html's autoPole/ΔR⁻¹·q block): requantizePoleTraced must
// carry an unchanged neighbor's stored indices forward (adjusted only by the pole's
// fixed increment), never re-derive them from a fresh live-cartesian measurement.
//
// requantizePoleTraced no longer touches md.centerOfNode at all (every direction it
// quantizes comes from either the `updates` map — a genuinely fresh cartesian offset —
// or a stored-index reconstruction via fromAxisFrame), so these tests call it directly
// on a bare *LayoutHolder with a zero-value *MoveDispatch receiver: there is nothing in
// md for the function to read.

import (
	"reflect"
	"testing"
)

// TestRequantizePoleTracedNoOpWhenPoleUnchanged: once a neighbor's local polar is
// quantized about a pole, re-running requantizePoleTraced with that neighbor absent
// from `updates` (i.e. unchanged) and no OTHER neighbor entering the singular zone
// (so the pole stays put) must leave its stored entry byte-identical — a TRUE no-op,
// not a reproject that happens to land on the same numbers.
func TestRequantizePoleTracedNoOpWhenPoleUnchanged(t *testing.T) {
	md := &MoveDispatch{}
	lh := &LayoutHolder{}

	offA := offsetFromDir(dir{Theta: 1.0, Phi: 0.3}).scale(30)
	offB := offsetFromDir(dir{Theta: 1.4, Phi: -1.9}).scale(50)

	md.requantizePoleTraced(lh, map[string]vec3{"a": offA, "b": offB}, "self")
	before := lh.LocalPolarsSnapshot()
	if len(before) != 2 {
		t.Fatalf("expected 2 local polars after initial quantize, got %d", len(before))
	}

	// Second call: neither neighbor is fresh (empty updates) — both must be carried
	// forward unchanged since the pole (home, +y — neither offset is near it) does not
	// move.
	md.requantizePoleTraced(lh, map[string]vec3{}, "self")
	after := lh.LocalPolarsSnapshot()

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unchanged neighbors were reprojected instead of carried forward:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestRequantizePoleTracedPreservesWorldDirectionOnPoleTilt: builds two neighbors away
// from +y (never touched again) and then introduces a THIRD, fresh neighbor whose
// offset falls inside poleKickTheta of +y — forcing the measurement pole to tilt by its
// fixed increment (rotating_pole.go localPole). The two untouched neighbors' STORED
// indices are never re-measured against live cartesian (there is none to measure — this
// test never calls centerOfNode), yet their reconstructed WORLD direction (fromAxisFrame
// on the NEW indices about the NEW pole) must still match their ORIGINAL offset
// direction within one quantization step — the invariant the fixed-increment/
// stored-index model exists to guarantee.
func TestRequantizePoleTracedPreservesWorldDirectionOnPoleTilt(t *testing.T) {
	md := &MoveDispatch{}
	lh := &LayoutHolder{}

	dirA := dir{Theta: 1.1, Phi: 0.4}  // well away from +y
	dirB := dir{Theta: 1.3, Phi: -2.2} // well away from +y
	offA := offsetFromDir(dirA).scale(40)
	offB := offsetFromDir(dirB).scale(60)

	md.requantizePoleTraced(lh, map[string]vec3{"a": offA, "b": offB}, "self")
	if lh.Pole() != (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("pole should still be home before any offender enters: got %+v", lh.Pole())
	}

	// A fresh third neighbor whose direction is INSIDE the singular zone around +y
	// forces the pole to kick off home by its fixed increment.
	dirC := dir{Theta: poleKickTheta / 2, Phi: 0}
	offC := offsetFromDir(dirC).scale(20)

	newPole := md.requantizePoleTraced(lh, map[string]vec3{"c": offC}, "self")
	if newPole == (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("expected the pole to tilt off home once a neighbor entered the singular zone")
	}
	if lh.Pole() != newPole {
		t.Fatalf("returned pole %+v was not the one persisted on the holder (%+v)", newPole, lh.Pole())
	}

	byID := map[string]LocalPolar{}
	for _, lp := range lh.LocalPolarsSnapshot() {
		byID[lp.To] = lp
	}

	check := func(id string, wantDir dir) {
		lp, ok := byID[id]
		if !ok {
			t.Fatalf("no local polar entry for %q after pole tilt", id)
		}
		t_, p_, _ := lp.effectiveSteps()
		gotDir := fromAxisFrame(newPole, float64(lp.QuantITheta)*t_, float64(lp.QuantIPhi)*p_)
		if d := angularDistance(gotDir, wantDir); d > t_+p_ {
			t.Fatalf("%q's world direction was not preserved across the pole tilt: got %+v want %+v (angularDistance=%v, allowed<=%v)",
				id, gotDir, wantDir, d, t_+p_)
		}
	}
	check("a", dirA)
	check("b", dirB)
}
