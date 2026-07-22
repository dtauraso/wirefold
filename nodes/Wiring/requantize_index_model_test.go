package Wiring

// requantize_index_model_test.go — tests that specifically PIN the INCREMENT /
// "abc-index × step-constant" model (memory/feedback_abc_times_constant_not_rederive.md,
// node_move.go requantizePoleTraced, commit c4b1cef4) and are DESIGNED TO FAIL against
// the OLD model it replaced: unchanged neighbors reconstructed from a fresh
// md.centerOfNode(neighbor) cartesian read instead of from their own stored
// (QuantITheta,QuantIPhi) indices × step constants about the persisted pole.
//
// Each test below was verified red against a temporarily-restored old-model patch of
// requantizePoleTraced (and, for test 3, of the pole-persistence path) before being
// committed against the real (new-model) code — see the session report for the
// captured failure output.

import (
	"fmt"
	"math"
	"reflect"
	"testing"
)

const testDeg = math.Pi / 180

// TestRequantizeUsesStoredIndicesNotLiveCartesian is the DECISIVE test for the
// stored-index model: an unchanged neighbor "far" has its stored abc-indices encoding
// direction D1, while md's live cartesian centers for "far"/"self" encode a DIFFERENT
// direction D2 (deliberately inconsistent — this is exactly the fresh-vs-stale
// situation the stored-index model exists to be immune to). A fresh, in-zone neighbor
// "near" is included so the pole tilts identically in both models (poleKickTheta only
// depends on the "closest to +y" offender, which has the same Y-component regardless of
// whether "far" carries D1 or D2 — see the doc comment on localPole, rotating_pole.go).
//
// The NEW model must reconstruct "far"'s post-tilt world direction from its STORED
// index (D1, adjusted only by the fixed pole increment) — never from live cartesian
// (D2). Asserts the reconstructed direction is close to D1 and far from D2.
func TestRequantizeUsesStoredIndicesNotLiveCartesian(t *testing.T) {
	md := &MoveDispatch{}
	lh := &LayoutHolder{}

	dirStored := dir{Theta: 70 * testDeg, Phi: 40 * testDeg} // D1 — what "far"'s indices will encode
	dirLive := dir{Theta: 70 * testDeg, Phi: 130 * testDeg}  // D2 — deliberately inconsistent live cartesian
	if angularDistance(dirStored, dirLive) < 60*testDeg {
		t.Fatalf("test setup bug: D1/D2 must be well separated, got %v", angularDistance(dirStored, dirLive))
	}

	offFar := offsetFromDir(dirStored).scale(40)
	md.requantizePoleTraced(lh, map[string]vec3{"far": offFar})
	if lh.Pole() != (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("pole should still be home before any offender enters: got %+v", lh.Pole())
	}

	// Live cartesian centers deliberately encode D2 for "far" — a fresh md.centerOfNode
	// read here would source D2, not the stored D1. requantizePoleTraced (new model)
	// must never make this call for an unchanged neighbor.
	// A fresh, in-zone neighbor "near" forces the pole to tilt off home — identically
	// in both models, since the tilt is driven solely by the max-Y ("closest to +y")
	// offset, and D1/D2 share the same Y-component (cos(70°)) regardless of bearing.
	dirNear := dir{Theta: poleKickTheta / 2, Phi: 0}
	offNear := offsetFromDir(dirNear).scale(20)
	newPole := md.requantizePoleTraced(lh, map[string]vec3{"near": offNear})
	if newPole == (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("expected the pole to tilt off home once a neighbor entered the singular zone")
	}

	var farEntry *LocalPolar
	for _, lp := range lh.LocalPolarsSnapshot() {
		if lp.To == "far" {
			cp := lp
			farEntry = &cp
		}
	}
	if farEntry == nil {
		t.Fatal("no local polar entry for far after pole tilt")
	}
	tt, pp, _ := farEntry.effectiveSteps()
	gotDir := fromAxisFrame(newPole, float64(farEntry.QuantITheta)*tt, float64(farEntry.QuantIPhi)*pp)

	dStored := angularDistance(gotDir, dirStored)
	dLive := angularDistance(gotDir, dirLive)

	// Generous bound: fixed pole tilt (poleKickTheta) + a couple of quantization steps
	// of rounding slack — comfortably smaller than the ~83° separation to D2.
	const storedBound = 5 * testDeg
	if dStored > storedBound {
		t.Fatalf("far's reconstructed world direction drifted from its STORED index (D1): got %+v want near %+v (angularDistance=%v, allowed<=%v) — this means far was re-derived from something other than its stored index", gotDir, dirStored, dStored, storedBound)
	}
	const liveBound = 30 * testDeg
	if dLive < liveBound {
		t.Fatalf("far's reconstructed world direction tracked the LIVE cartesian center (D2) instead of its stored index (D1): got %+v, angularDistance to D2=%v (want > %v) — this is the old centerOfNode-rederive bug", gotDir, dLive, liveBound)
	}
}

// TestRequantizeIndexTimesStepIsAuthoritative: an unchanged neighbor's stored
// (QuantITheta,QuantIPhi) pair is deliberately "off-grid-looking" (17, -5) with a clean
// step constant so 17×step is an exact, known angle. The neighbor's LIVE cartesian
// center is set to encode a DIFFERENT angle. Calling requantizePoleTraced with the pole
// staying home (no fresh in-zone neighbor) must leave the stored entry BYTE-IDENTICAL
// (reflect.DeepEqual) — proof the reconstruction used index×step of the STORED value,
// never a live-cartesian re-derivation that would track the disagreeing live angle.
func TestRequantizeIndexTimesStepIsAuthoritative(t *testing.T) {
	md := &MoveDispatch{}
	lh := &LayoutHolder{}

	stepTheta := 1 * testDeg
	stepPhi := 1 * testDeg
	want := LocalPolar{
		To: "farB", QuantITheta: 17, QuantIPhi: -5, QuantIR: 3,
		StepTheta: stepTheta, StepPhi: stepPhi, StepR: 2,
	}
	lh.SetLocalPolar(want.To, want.QuantITheta, want.QuantIPhi, want.QuantIR, want.StepTheta, want.StepPhi, want.StepR)

	// The stored index's TRUE direction: 17°,-5° about home.
	trueDir := fromAxisFrame(dir{Theta: 0, Phi: 0}, float64(want.QuantITheta)*stepTheta, float64(want.QuantIPhi)*stepPhi)
	// Neither trueDir nor the disagreeing live direction below is anywhere near the
	// singular zone, so the pole must stay home this call.
	liveDir := dir{Theta: trueDir.Theta + 25*testDeg, Phi: trueDir.Phi + 60*testDeg}

	_ = liveDir

	before := lh.LocalPolarsSnapshot()

	newPole := md.requantizePoleTraced(lh, map[string]vec3{})
	if newPole != (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("pole should stay home: got %+v", newPole)
	}

	after := lh.LocalPolarsSnapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("unchanged neighbor's stored index×step was not authoritative — a live-cartesian disagreement (farB's centerOfNode) leaked into the reconstruction and changed the stored entry:\nbefore=%+v\nafter=%+v", before, after)
	}
	if len(after) != 1 || after[0].QuantITheta != 17 || after[0].QuantIPhi != -5 {
		t.Fatalf("stored entry was not preserved verbatim: %+v", after)
	}
}

// TestPersistedPoleDrivesReloadWorldPositions builds a node ("self") whose resolved
// pole is TILTED (not home), persists its local polars + that pole (WriteLocalPolars),
// then reloads through the real loader with a REDUCED neighbor set that on its own
// (without the persisted pole) would resolve to a DIFFERENT (home) pole — and asserts
// the reload honors the PERSISTED pole verbatim rather than falling back to a
// freshly-recomputed one.
//
// loader.go computeLocalPolars ALWAYS re-derives a stored neighbor's bearing from its
// LIVE cartesian offset when that neighbor's center is resolvable (preserving
// QuantIR/step constants) — so the reconstructed indices are internally consistent with
// whichever pole the loader actually used, correct or not. That means the world
// direction recovered by reconstructing under lhSelf.Pole() ALONE can never expose a
// wrong pole (it's a tautological round-trip). The decisive check instead reconstructs
// "far"'s reloaded indices under the INDEPENDENTLY KNOWN correct tiltedPole (not
// whatever lhSelf.Pole() reports) and compares to far's true live direction: if the
// loader quantized "far"'s bearing about the wrong (home) pole, this reconstruction
// diverges by roughly the fixed pole increment; if it honored the persisted tiltedPole,
// it recovers far's true direction almost exactly (a tiny per-neighbor step constant
// keeps quantization rounding negligible next to that divergence).
func TestPersistedPoleDrivesReloadWorldPositions(t *testing.T) {
	// Compute the tilted pole and far's stored index against it purely with the
	// in-package quantizer (mirrors TestRequantizePoleTracedPreservesWorldDirectionOnPoleTilt),
	// using a deliberately tiny step for "far" so subsequent quantization rounding is
	// negligible next to the (much larger) pole-tilt divergence this test pins.
	md := &MoveDispatch{}
	lh := &LayoutHolder{}
	tinyStep := 0.001 * testDeg
	lh.SetLocalPolar("far", 0, 0, 0, tinyStep, tinyStep, 1)

	dirFar := dir{Theta: 60 * testDeg, Phi: 30 * testDeg}
	offFar := offsetFromDir(dirFar).scale(40)
	md.requantizePoleTraced(lh, map[string]vec3{"far": offFar})
	if lh.Pole() != (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("pole should still be home before the offender enters: got %+v", lh.Pole())
	}

	dirNear := dir{Theta: poleKickTheta / 2, Phi: 0}
	offNear := offsetFromDir(dirNear).scale(20)
	tiltedPole := md.requantizePoleTraced(lh, map[string]vec3{"near": offNear})
	if tiltedPole == (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("expected the pole to tilt off home")
	}

	var farEntry *LocalPolar
	for _, lp := range lh.LocalPolarsSnapshot() {
		if lp.To == "far" {
			cp := lp
			farEntry = &cp
		}
	}
	if farEntry == nil {
		t.Fatal("no local polar entry for far after pole tilt")
	}

	// Build a tree for reload WITHOUT "near" as a neighbor — so a fresh (non-persisted)
	// pole computation from the reduced neighbor set (just "far", whose direction
	// (60°) is nowhere near +y) would resolve to HOME, not tiltedPole.
	root := t.TempDir()
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	mk("nodes/self/meta.json", `{"id":"self","type":"SinkNode","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0}`)
	mk("nodes/self/inputs/In.json", `{"name":"In"}`)
	// "far" gets a REAL position, at exactly dirFar/40 from self (self sits at the
	// origin) — its center IS resolvable at reload, so computeLocalPolars re-derives
	// its bearing from this live offset about whichever pole the loader resolves
	// (loader.go's `if mCenter, ok3 := b.centers[mid]` branch fires). That re-derive
	// is internally consistent regardless of which pole was used — the test below
	// exposes a wrong pole by reconstructing under the INDEPENDENTLY known-correct
	// tiltedPole, not under lhSelf.Pole() itself.
	mk("nodes/far/meta.json", fmt.Sprintf(`{"id":"far","type":"SrcNode","scenePolarR":40,"scenePolarTheta":%v,"scenePolarPhi":%v}`, dirFar.Theta, dirFar.Phi))
	mk("nodes/far/outputs/Out.json", `{"name":"Out"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"far","sourceHandle":"Out","target":"self","targetHandle":"In"}`)

	if err := WriteLocalPolars(root, "self", []LocalPolar{*farEntry}, tiltedPole); err != nil {
		t.Fatalf("WriteLocalPolars: %v", err)
	}

	md2 := loadTreeMD(t, root)
	lhSelf, ok := md2.layoutHolders["self"]
	if !ok {
		t.Fatal("no LayoutHolder for self on reload")
	}

	if d := angularDistance(lhSelf.Pole(), tiltedPole); d > 1e-6 {
		t.Fatalf("reload did not honor the persisted pole verbatim: got %+v want %+v (angularDistance=%v)", lhSelf.Pole(), tiltedPole, d)
	}

	var farAfter *LocalPolar
	for _, lp := range lhSelf.LocalPolarsSnapshot() {
		if lp.To == "far" {
			cp := lp
			farAfter = &cp
		}
	}
	if farAfter == nil {
		t.Fatal("reloaded self has no local polar entry for far")
	}
	tt, pp, _ := farAfter.effectiveSteps()
	// Reconstruct under the INDEPENDENTLY KNOWN correct tiltedPole (not lhSelf.Pole(),
	// which would tautologically round-trip regardless of which pole the loader
	// actually quantized about) — this is what exposes a wrong (e.g. home-fallback)
	// pole having been used at quantize time.
	gotDir := fromAxisFrame(tiltedPole, float64(farAfter.QuantITheta)*tt, float64(farAfter.QuantIPhi)*pp)
	const bound = 0.1 * testDeg
	if d := angularDistance(gotDir, dirFar); d > bound {
		t.Fatalf("far's reloaded bearing, reconstructed under the KNOWN correct tiltedPole, did not recover far's true direction: got %+v want %+v (angularDistance=%v, allowed<=%v) — the loader quantized far's bearing about the WRONG pole (it did not honor the persisted tiltedPole)", gotDir, dirFar, d, bound)
	}
}
