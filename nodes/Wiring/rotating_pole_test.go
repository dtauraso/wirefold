package Wiring

// rotating_pole_test.go — tests for the DETERMINISTIC LOCAL POLE
// (docs/planning/visual-editor/deterministic-local-pole.md, rotating_pole.go).

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLocalPoleDeterministic asserts localPole is a pure function: the same set of
// neighbor directions always yields the same pole, regardless of how many times it is
// evaluated.
func TestLocalPoleDeterministic(t *testing.T) {
	dirs := []dir{
		{Theta: 0.15, Phi: 0.4},
		{Theta: 1.2, Phi: -0.7},
		{Theta: 0.6, Phi: 2.1},
	}
	first := localPole(dirs)
	for i := 0; i < 5; i++ {
		got := localPole(dirs)
		if got != first {
			t.Fatalf("localPole not deterministic: run %d got %+v, want %+v", i, got, first)
		}
	}
}

// TestLocalPoleHomeWhenClear asserts that when every offset's colatitude (about world +y)
// is at or beyond poleKickTheta, the pole is exactly home (+y) — no dodge needed.
func TestLocalPoleHomeWhenClear(t *testing.T) {
	dirs := []dir{
		{Theta: poleKickTheta, Phi: 0}, // exactly at the threshold — still "clear"
		{Theta: math.Pi / 2, Phi: 1.0},
	}
	got := localPole(dirs)
	want := dir{Theta: 0, Phi: 0}
	if got != want {
		t.Fatalf("localPole should be home when every offset is clear of poleKickTheta: got %+v want %+v", got, want)
	}
}

// TestLocalPoleDodgesInsideZone asserts that when the closest offset's colatitude is
// just inside poleKickTheta, the pole tilts so that offset lands at EXACTLY colatitude
// poleKickTheta about the new pole — the closed-form dodge, not the old 90-degree kick.
func TestLocalPoleDodgesInsideZone(t *testing.T) {
	closest := dir{Theta: 0.05, Phi: 0.9} // well inside the 20-degree zone
	dirs := []dir{closest, {Theta: 1.0, Phi: -2.0}}
	pole := localPole(dirs)
	if pole == (dir{Theta: 0, Phi: 0}) {
		t.Fatalf("localPole should have dodged away from home for an offset inside poleKickTheta")
	}
	gotC := angularDistance(pole, closest)
	if math.Abs(gotC-poleKickTheta) > 1e-9 {
		t.Fatalf("localPole should place the closest offset at exactly poleKickTheta: got colatitude %v want %v", gotC, poleKickTheta)
	}
}

// TestLocalPoleContinuousAcrossThreshold asserts the pole moves CONTINUOUSLY as the
// closest offset's colatitude crosses poleKickTheta from below — no jump at the boundary
// (the old kick-and-recheck loop had a discontinuity; the closed-form dodge does not).
func TestLocalPoleContinuousAcrossThreshold(t *testing.T) {
	phi := 0.7
	const eps = 1e-6
	below := dir{Theta: poleKickTheta - eps, Phi: phi}
	above := dir{Theta: poleKickTheta + eps, Phi: phi}
	poleBelow := localPole([]dir{below})
	poleAbove := localPole([]dir{above})
	d := angularDistance(poleBelow, poleAbove)
	if d > 1e-3 {
		t.Fatalf("localPole is discontinuous across poleKickTheta: poleBelow=%+v poleAbove=%+v angularDistance=%v", poleBelow, poleAbove, d)
	}
}

// TestKickIncreasesAngularDistance pins the SIGN of the dodge rotation used inside
// localPole: rotating home away from the closest offending offset (rotateDir with
// arcBetween(closest,home).Axis) must move the pole so the offset's angular distance
// INCREASES relative to no dodge at all — i.e. the offset ends up farther from the new
// pole than it was from home. Verified empirically: arcBetween(oDir,pole), not
// arcBetween(pole,oDir), is the correct direction for the rotation axis.
func TestKickIncreasesAngularDistance(t *testing.T) {
	home := dir{Theta: 0, Phi: 0}
	oDir := dir{Theta: 0.05, Phi: 0.4} // inside poleKickTheta
	cHome := angularDistance(home, oDir)
	newPole := localPole([]dir{oDir})
	newC := angularDistance(newPole, oDir)
	if newC <= cHome {
		t.Fatalf("localPole's dodge decreased (or did not change) angular distance: before=%v after=%v", cHome, newC)
	}
	if math.Abs(newC-poleKickTheta) > 1e-9 {
		t.Fatalf("localPole should land oDir exactly at poleKickTheta; got %v", newC)
	}
}

// TestRotatingPoleClearsSingularityOnDrag drags a neighbor so its offset direction (from
// the dragged-into node's perspective) sweeps to within poleKickTheta of world +y — the
// exact scenario a FIXED world +y pole cannot represent (rotating-pole-frame.md /
// deterministic-local-pole.md). Asserts the resulting offset's colatitude about the
// (freshly recomputed) pole is bounded clear of the threshold after the drag settles.
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

	// Drag dst to a position 5 degrees off world +y (well inside the 20-degree kick
	// threshold).
	home := dir{Theta: 0, Phi: 0}
	near := fromAxisFrame(home, 5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))

	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	offDir, _ := dirFromOffset(dstCenter.sub(srcCenter))
	pole := localPole([]dir{offDir})
	c := angularDistance(pole, offDir)
	if c < poleKickTheta-1e-6 {
		t.Fatalf("offset colatitude %v still below poleKickTheta %v after dodge — pole=%+v", c, poleKickTheta, pole)
	}

	// src's own local polar to dst is requantized by src's OWN mover goroutine, reached
	// asynchronously via a moveMsgKindRequantize message sent from dst's mover after dst's
	// own position commits (node_move.go requantizeLocalPolars) — dst converging to target
	// does not itself guarantee src has processed that message yet, so poll for it rather
	// than asserting immediately (this exact race is why the old kick-based test was
	// flaky; here it is a plain message-delivery race, not a fixed-point convergence one).
	var got *LocalPolar
	deadline := time.Now().Add(2 * time.Second)
	for {
		got = nil
		for _, lp := range lhSrc.LocalPolarsSnapshot() {
			if lp.To == "dst" {
				cp := lp
				got = &cp
			}
		}
		if got != nil {
			st, sp, _ := got.effectiveSteps()
			gotDir := fromAxisFrame(pole, float64(got.QuantITheta)*st, float64(got.QuantIPhi)*sp)
			if angularDistance(gotDir, offDir) <= st+sp {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("src's local polar to dst never converged to the live offset direction: got=%+v want=%+v", got, offDir)
		}
		time.Sleep(time.Millisecond)
	}
	md.quantOffsetPersist.flush()
}

// TestRotatingPolePersistReload drags src's neighbor, flushes, then RELOADS from disk
// into a fresh MoveDispatch and asserts a drag-then-reload lands the same bearings: since
// the pole is a pure function of live geometry (never persisted), both the live runtime
// and the reload evaluate localPole on the same (persisted, lossless scenePolar) geometry
// and must agree exactly.
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
	home := dir{Theta: 0, Phi: 0}
	near := fromAxisFrame(home, 5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)

	dstCenter, _ := md.centerOfNode("dst")
	wantDir, _ := dirFromOffset(dstCenter.sub(srcCenter))
	// src's own local polar to dst is requantized asynchronously (a moveMsgKindRequantize
	// message from dst's mover) — poll until it reflects the post-drag geometry before
	// flushing, so the persisted bearing is not a stale pre-drag value.
	var before *LocalPolar
	deadline := time.Now().Add(2 * time.Second)
	for {
		before = nil
		for _, lp := range lhSrc.LocalPolarsSnapshot() {
			if lp.To == "dst" {
				cp := lp
				before = &cp
			}
		}
		if before != nil {
			st, sp, _ := before.effectiveSteps()
			gotDir := fromAxisFrame(localPole([]dir{wantDir}), float64(before.QuantITheta)*st, float64(before.QuantIPhi)*sp)
			if angularDistance(gotDir, wantDir) <= st+sp {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("src's local polar to dst never converged to the live offset direction")
		}
		time.Sleep(time.Millisecond)
	}
	md.quantOffsetPersist.flush()

	raw, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read src meta: %v", err)
	}
	if containsKey(raw, "localPoleTheta") || containsKey(raw, "localPolePhi") {
		t.Fatalf("src meta.json should NOT persist a local pole (pure function of geometry): %s", raw)
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
// (fromAxisFrame) lands within one step cell of the live offset direction, while Role
// and QuantIR are preserved verbatim.
func TestComputeLocalPolarsRequantizesStoredBearingAboutResolvedPole(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	// src's stored localPolars entry for dst carries a bearing (quantITheta/quantIPhi)
	// that is deliberately bogus/stale — NOT what src↔dst's live geometry implies about
	// any pole — so computeLocalPolars must re-quantize this stored bearing about the
	// freshly-resolved pole rather than trusting the stale numbers. Role and quantIR must
	// survive untouched.
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
	wantDir, _ := dirFromOffset(dstCenter.sub(srcCenter))
	pole := localPole([]dir{wantDir})

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
	if got.Role != "source" {
		t.Fatalf("Role not preserved: got %q want %q", got.Role, "source")
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
