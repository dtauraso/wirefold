package Wiring

// rotating_pole_test.go — tests for the DETERMINISTIC LOCAL POLE (rotating_pole.go).

import (
	"context"
	"math"
	"os"
	"testing"
	"time"
)

// offsetFromDir builds a unit-length Cartesian offset vector pointing in direction d
// (colatitude/azimuth about world +y) — a test-only convenience for feeding localPole,
// which now takes Cartesian offset vectors rather than dirs.
func offsetFromDir(d dir) vec3 {
	return polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi})
}

// TestLocalPoleDeterministic asserts localPole is a pure function: the same set of
// neighbor offsets always yields the same pole, regardless of how many times it is
// evaluated.
func TestLocalPoleDeterministic(t *testing.T) {
	offsets := []vec3{
		offsetFromDir(dir{Theta: 0.15, Phi: 0.4}),
		offsetFromDir(dir{Theta: 1.2, Phi: -0.7}),
		offsetFromDir(dir{Theta: 0.6, Phi: 2.1}),
	}
	first := localPole(offsets)
	for i := 0; i < 5; i++ {
		got := localPole(offsets)
		if got != first {
			t.Fatalf("localPole not deterministic: run %d got %+v, want %+v", i, got, first)
		}
	}
}

// TestLocalPoleHomeWhenClear asserts that when every offset's colatitude (about world +y)
// is at or beyond poleKickTheta, the pole is exactly home (+y) — no tilt needed. The
// "near +y" test is now a y-component compare (y > cosPoleKick); at exactly poleKickTheta
// the offset's y-component equals cosPoleKick, which is still "clear" (strict >).
func TestLocalPoleHomeWhenClear(t *testing.T) {
	offsets := []vec3{
		offsetFromDir(dir{Theta: poleKickTheta, Phi: 0}), // exactly at the threshold — still "clear"
		offsetFromDir(dir{Theta: math.Pi / 2, Phi: 1.0}),
	}
	got := localPole(offsets)
	want := dir{Theta: 0, Phi: 0}
	if got != want {
		t.Fatalf("localPole should be home when every offset is clear of poleKickTheta: got %+v want %+v", got, want)
	}
}

// TestLocalPoleDodgesInsideZone asserts that when the closest offset's colatitude is
// just inside poleKickTheta, the pole tilts by the FIXED angle poleKickTheta away from
// home, and the offender's colatitude about the new pole no longer sits at the old
// (pre-dodge) value — it has moved (dodged away), never re-deriving a variable angle.
func TestLocalPoleDodgesInsideZone(t *testing.T) {
	closestDir := dir{Theta: 0.005, Phi: 0.9} // well inside the 1-degree zone (~0.29°)
	closest := offsetFromDir(closestDir)
	offsets := []vec3{closest, offsetFromDir(dir{Theta: 1.0, Phi: -2.0})}
	pole := localPole(offsets)
	home := dir{Theta: 0, Phi: 0}
	if pole == home {
		t.Fatalf("localPole should have tilted away from home for an offset inside poleKickTheta")
	}
	gotTilt := angularDistance(home, pole)
	if math.Abs(gotTilt-poleKickTheta) > 1e-9 {
		t.Fatalf("localPole should tilt by exactly the fixed poleKickTheta: got %v want %v", gotTilt, poleKickTheta)
	}
	oldC := angularDistance(home, closestDir)
	newC := angularDistance(pole, closestDir)
	if newC < oldC-1e-9 {
		t.Fatalf("localPole's dodge should not decrease the offender's colatitude: before=%v after=%v", oldC, newC)
	}
}

// TestLocalPoleFixedMagnitudeAcrossZone asserts the tilt magnitude is a FIXED constant
// (poleKickTheta) whenever the closest offset is inside the zone, regardless of exactly
// how far inside — unlike the old variable-angle rederive, the fixed-increment model
// applies the same rotation size near the boundary and deep inside the zone alike.
func TestLocalPoleFixedMagnitudeAcrossZone(t *testing.T) {
	phi := 0.7
	home := dir{Theta: 0, Phi: 0}
	justInside := offsetFromDir(dir{Theta: poleKickTheta - 1e-6, Phi: phi})
	deepInside := offsetFromDir(dir{Theta: poleKickTheta * 0.1, Phi: phi})

	poleJustInside := localPole([]vec3{justInside})
	poleDeepInside := localPole([]vec3{deepInside})

	dJust := angularDistance(home, poleJustInside)
	dDeep := angularDistance(home, poleDeepInside)
	if math.Abs(dJust-poleKickTheta) > 1e-9 {
		t.Fatalf("tilt magnitude just inside the zone should be exactly poleKickTheta: got %v", dJust)
	}
	if math.Abs(dDeep-poleKickTheta) > 1e-9 {
		t.Fatalf("tilt magnitude deep inside the zone should be exactly poleKickTheta: got %v", dDeep)
	}

	atThreshold := offsetFromDir(dir{Theta: poleKickTheta, Phi: phi})
	poleAtThreshold := localPole([]vec3{atThreshold})
	if poleAtThreshold != home {
		t.Fatalf("localPole should be exactly home right at the threshold (clear): got %+v", poleAtThreshold)
	}
}

// TestPoleTiltIncreasesAngularDistance pins the SIGN of the fixed-increment tilt: it
// moves the pole so the offending offset's colatitude about the NEW pole is never less
// than its colatitude about home — i.e. the offset dodges away, never toward, the pole.
func TestPoleTiltIncreasesAngularDistance(t *testing.T) {
	home := dir{Theta: 0, Phi: 0}
	oDir := dir{Theta: 0.005, Phi: 0.4} // inside poleKickTheta (~0.29°)
	cHome := angularDistance(home, oDir)
	newPole := localPole([]vec3{offsetFromDir(oDir)})
	newC := angularDistance(newPole, oDir)
	if newC < cHome-1e-9 {
		t.Fatalf("localPole's tilt decreased the offender's angular distance: before=%v after=%v", cHome, newC)
	}
	if math.Abs(angularDistance(home, newPole)-poleKickTheta) > 1e-9 {
		t.Fatalf("localPole should tilt home by exactly poleKickTheta; got %v", angularDistance(home, newPole))
	}
}

// TestRotatingPoleClearsSingularityOnDrag drags a neighbor so its offset direction (from
// the dragged-into node's perspective) sweeps to within poleKickTheta of world +y — the
// exact scenario a FIXED world +y pole cannot represent (rotating-pole-frame.md /
// deterministic-local-pole.md). Asserts the resulting pole has tilted away from home and
// the offender's colatitude about it has not decreased relative to home.
func TestRotatingPoleClearsSingularityOnDrag(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}
	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src")
	}
	var lpBefore LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			lpBefore = lp
		}
	}

	// Sync point for the post-drag lhSrc read below: src (the neighbor, NOT the dragged
	// node) writes its own requantized LocalPolar entry (SetLocalPolar/SetPole, on src's
	// OWN goroutine, inside neighborSetCRequantize) strictly BEFORE it logs its
	// "abc-drag" breadcrumb in that same call — waiting for the breadcrumb (rather than
	// polling lhSrc directly, a data race against src's own mover goroutine) establishes
	// the happens-before edge. See time_node_abc_drag_breadcrumb_test.go.
	var dbg syncBuffer
	md.tr.SetDebugSink(&dbg)

	// Drag dst to a position 0.5 degrees off world +y (well inside the 1-degree kick
	// threshold).
	home := dir{Theta: 0, Phi: 0}
	near := fromAxisFrame(home, 0.5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))

	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	offVec := dstCenter.sub(srcCenter)
	offDir, _ := dirFromOffset(offVec)
	pole := localPole([]vec3{offVec})
	if pole == home {
		t.Fatalf("pole should have tilted away from home for an offset inside poleKickTheta: offDir=%+v", offDir)
	}
	cHome := angularDistance(home, offDir)
	cPole := angularDistance(pole, offDir)
	if cPole < cHome-1e-9 {
		t.Fatalf("offset colatitude about the new pole (%v) should not be less than about home (%v) — pole=%+v", cPole, cHome, pole)
	}

	// src is dst's plain (role-free) direct neighbor: under the current single-
	// assignment set-c REQUANTIZE model (node_move.go moveMsgKindNeighborSetC /
	// neighborSetCRequantize), src STAYS PUT — only dst moved — and re-quantizes its
	// OWN stored (QuantITheta,QuantIPhi,QuantIR) to dst fresh from the live offset.
	// Wait for src's own "abc-drag" breadcrumb (see the sync-point comment above)
	// before reading its LocalPolar entry to dst.
	waitForAbcDrag(t, &dbg, "src")
	var got LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			got = lp
		}
	}
	if got.QuantIR == lpBefore.QuantIR {
		t.Fatalf("src's local polar to dst never picked up the new set-c: before=%+v after=%+v", lpBefore, got)
	}

	// src's world center must NOT have moved — only dst moved.
	srcCenterAfter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after drag")
	}
	if d := srcCenterAfter.sub(srcCenter).length(); d > 1e-9 {
		t.Fatalf("src must stay put on a dst drag: before=%+v after=%+v (moved by %g)", srcCenter, srcCenterAfter, d)
	}

	// src's requantized local polar to dst must match a fresh quantization of the live
	// offset (dst_newcenter - src_center) about src's own pole.
	offsetAfter := dstCenter.sub(srcCenterAfter)
	dAfter, rAfter := dirFromOffset(offsetAfter)
	cAfter, psiAfter := azimuthFrom(lhSrc.Pole(), dAfter)
	st, sp, sr := got.effectiveSteps()
	wantTheta := int(math.Round(cAfter / st))
	wantPhi := int(math.Round(psiAfter / sp))
	wantR := int(math.Round(rAfter / sr))
	if got.QuantITheta != wantTheta || got.QuantIPhi != wantPhi || got.QuantIR != wantR {
		t.Fatalf("src's requantized local polar to dst should match a fresh quantization of the live offset: got=(theta=%d,phi=%d,r=%d) want=(theta=%d,phi=%d,r=%d)",
			got.QuantITheta, got.QuantIPhi, got.QuantIR, wantTheta, wantPhi, wantR)
	}

}

// TestRotatingPolePersistReload drags src's neighbor, flushes, then RELOADS from disk
// into a fresh MoveDispatch and asserts a drag-then-reload lands the same bearings. The
// measurement pole a node's LocalPolars entries were last quantized about IS persisted
// (WriteLocalPolars, layout_holder.go LayoutHolder.Pole/SetPole) — required by the
// fixed-increment/stored-index requantize model (memory/feedback_abc_times_constant_not_rederive.md):
// requantizePoleTraced reconstructs an unchanged neighbor's direction from stored indices
// about the OLD pole, so the pole itself must round-trip losslessly, not be re-derived from
// scratch on reload.
func TestRotatingPolePersistReload(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}
	srcCenter, _ := md.centerOfNode("src")
	var preDrag LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			preDrag = lp
		}
	}
	// Sync point for the post-drag lhSrc read below: src (the neighbor, NOT the dragged
	// node) writes its own requantized LocalPolar entry (SetLocalPolar/SetPole, on src's
	// OWN goroutine, inside neighborSetCRequantize) strictly BEFORE it logs its
	// "abc-drag" breadcrumb in that same call — waiting for the breadcrumb (rather than
	// polling lhSrc directly, a data race against src's own mover goroutine) establishes
	// the happens-before edge. See time_node_abc_drag_breadcrumb_test.go.
	var dbg syncBuffer
	md.tr.SetDebugSink(&dbg)

	home := dir{Theta: 0, Phi: 0}
	near := fromAxisFrame(home, 5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	// src is dst's plain (role-free) direct neighbor: under the current single-
	// assignment set-c REQUANTIZE model, src stays put and re-quantizes its own
	// bearing AND distance to dst fresh from the live offset
	// (moveMsgKindNeighborSetC / neighborSetCRequantize). Wait for src's own
	// "abc-drag" breadcrumb before reading, so the persisted value is not a stale
	// pre-drag one.
	waitForAbcDrag(t, &dbg, "src")
	var before *LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			cp := lp
			before = &cp
		}
	}
	if before == nil || before.QuantIR == preDrag.QuantIR {
		t.Fatalf("src's local polar to dst never picked up the new set-c: before=%+v preDrag=%+v", before, preDrag)
	}
	// waitForAbcDrag only proves the "abc-drag" breadcrumb has logged; neighborSetCRequantize
	// writes local-polars.json to disk (WriteLocalPolars, synchronous) a few statements
	// AFTER that breadcrumb on the same goroutine, so reading disk immediately after can
	// still race ahead of that write landing. Poll the read-back under a deadline (same
	// shape as pollDragConverged/pollPositionFileWritten).
	var raw []byte
	deadline := time.Now().Add(2 * time.Second)
	for {
		var err error
		raw, err = os.ReadFile(localPolarsFilePath(root, "src"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read src local-polars.json: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if !containsKey(raw, "localPoleTheta") || !containsKey(raw, "localPolePhi") {
		t.Fatalf("src local-polars.json should persist the local pole (fixed-increment/stored-index model): %s", raw)
	}

	// Reload into a fresh MoveDispatch.
	md2 := loadTreeMD(t, root)
	lhSrc2, ok := md2.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src on reload")
	}
	var after *LocalPolar
	for _, lp := range lhSrc2.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			cp := lp
			after = &cp
		}
	}
	if after == nil {
		t.Fatal("reloaded src has no local polar entry for dst")
	}
	if before.QuantITheta != after.QuantITheta || before.QuantIPhi != after.QuantIPhi || before.QuantIR != after.QuantIR {
		t.Fatalf("bearing did not round-trip identically on reload: before=%+v after=%+v", before, after)
	}
}

// TestComputeLocalPolarsRequantizesStoredBearingAboutResolvedPole guards the load-time
// bug where a STORED localPolars entry's bearing was copied verbatim even though the
// node's pole is resolved from LIVE geometry — so a bearing quantized about a different
// pole (e.g. pre-deterministic-pole data quantized about a stale rotated pole) would be
// inconsistent with the resolved pole. Writes a tree whose src/meta.json carries a stored
// localPolars entry with a bearing that is NOT consistent with the live dst offset, and
// asserts that after load, reconstructing the stored bearing under the resolved pole
// (fromAxisFrame) lands within one step cell of the live offset direction, while
// QuantIR is preserved verbatim.
func TestComputeLocalPolarsRequantizesStoredBearingAboutResolvedPole(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	// src's stored localPolars entry for dst carries a bearing (quantITheta/quantIPhi)
	// that is deliberately bogus/stale — NOT what src↔dst's live geometry implies about
	// any pole — so computeLocalPolars must re-quantize this stored bearing about the
	// freshly-resolved pole rather than trusting the stale numbers. quantIR must
	// survive untouched. The stored "role" key is retained ONLY for on-disk
	// compatibility with old meta.json files (unconsumed field, JSON decode
	// silently ignores it — LocalPolar has no Role field).
	mk("nodes/src/meta.json", `{"id":"src","type":"FanInSrc","r":100,"scenePolarR":37.4165738677,"scenePolarTheta":1.00685368543,"scenePolarPhi":1.2490457724,"localPolars":[{"to":"dst","role":"source","quantITheta":9999,"quantIPhi":-9999,"quantIR":42}]}`)
	mk("nodes/src/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/dst/meta.json", `{"id":"dst","type":"FanInSink","r":100,"scenePolarR":87.7496438739,"scenePolarTheta":0.96453035788,"scenePolarPhi":-2.15879893034}`)
	mk("nodes/dst/inputs/In.json", `{"name":"In"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}`)

	md := loadTreeMD(t, root)
	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}
	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src")
	}
	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst")
	}
	wantOff := dstCenter.sub(srcCenter)
	wantDir, _ := dirFromOffset(wantOff)
	pole := localPole([]vec3{wantOff})

	var got *LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			cp := lp
			got = &cp
		}
	}
	if got == nil {
		t.Fatal("src has no local polar entry for dst after load")
	}
	if got.QuantIR != 42 {
		t.Fatalf("QuantIR not preserved: got %d want 42", got.QuantIR)
	}
	if got.QuantITheta == 9999 || got.QuantIPhi == -9999 {
		t.Fatalf("stored stale bearing was copied verbatim instead of re-quantized about the resolved pole: %+v", got)
	}
	st, sp, _ := got.effectiveSteps()
	gotDir := fromAxisFrame(pole, float64(got.QuantITheta)*st, float64(got.QuantIPhi)*sp)
	if d := angularDistance(gotDir, wantDir); d > st+sp {
		t.Fatalf("re-quantized bearing does not match live offset direction within one step cell: angularDistance=%v (steps theta=%v phi=%v) pole=%+v got=%+v want=%+v", d, st, sp, pole, gotDir, wantDir)
	}
}

// containsKey is a minimal raw-JSON substring check for a top-level key's presence —
// avoids re-parsing into a map just to check existence.
func containsKey(raw []byte, key string) bool {
	needle := []byte(`"` + key + `"`)
	return len(raw) > 0 && indexOf(raw, needle) >= 0
}

func indexOf(hay, needle []byte) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
