package Wiring

// rotating_pole_test.go — tests for the ROTATING PER-NODE LOCAL POLE
// (docs/planning/visual-editor/rotating-pole-frame.md, rotating_pole.go).

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestKickIncreasesAngularDistance pins the SIGN of kickPoleAwayFrom: it must move the
// pole so the offending offset's angular distance to the pole INCREASES (toward π/2), not
// decreases. Verified empirically — arcBetween(oDir,pole), not arcBetween(pole,oDir), is
// the correct direction; the reversed call decreases the distance instead (would make the
// kick worse than a no-op).
func TestKickIncreasesAngularDistance(t *testing.T) {
	pole := dir{Theta: 0.1, Phi: 0} // near world +y — deliberately close to an offset below
	oDir := dir{Theta: 0.15, Phi: 0.4}
	c := angularDistance(pole, oDir)
	if c >= poleKickTheta {
		t.Fatalf("test setup: c=%v not below poleKickTheta=%v", c, poleKickTheta)
	}
	newPole := kickPoleAwayFrom(pole, oDir, c)
	newC := angularDistance(newPole, oDir)
	if newC <= c {
		t.Fatalf("kickPoleAwayFrom decreased (or did not change) angular distance: before=%v after=%v", c, newC)
	}
	if math.Abs(newC-math.Pi/2) > 1e-9 {
		t.Fatalf("kickPoleAwayFrom should land oDir exactly at the equator (pi/2); got %v", newC)
	}
}

// TestRotatingPoleKicksAwayFromOffset drags a neighbor so its offset direction (from the
// dragged-into node's perspective) sweeps to within poleKickTheta of that node's CURRENT
// local pole — the exact scenario a FIXED world +y pole cannot represent (the fixed pole
// has no way to move away from an offset passing near it; it would instead record a
// bearing near the azimuth singularity, rotating-pole-frame.md). Asserts the per-node
// pole itself moves (kicks) and the resulting offset colatitude is bounded (>= the
// threshold, up to float slack).
func TestRotatingPoleKicksAwayFromOffset(t *testing.T) {
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
	pole0, hasPole0 := lhSrc.LocalPole()
	if !hasPole0 {
		t.Fatal("src has no local pole after load — computeLocalPolars must seed one")
	}
	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src")
	}

	// Drag dst to a position 5 degrees off src's pole (well inside the 20-degree kick
	// threshold).
	near := fromAxisFrame(pole0, 5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))

	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)
	md.quantOffsetPersist.flush()

	pole1, hasPole1 := lhSrc.LocalPole()
	if !hasPole1 {
		t.Fatal("src lost its local pole after the drag")
	}
	if pole1 == pole0 {
		t.Fatalf("pole did not kick away from the near-pole offset (still %+v)", pole0)
	}

	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	offDir, _ := dirFromOffset(dstCenter.sub(srcCenter))
	c := angularDistance(pole1, offDir)
	if c < poleKickTheta-1e-6 {
		t.Fatalf("offset colatitude %v still below poleKickTheta %v after kick — pole1=%+v", c, poleKickTheta, pole1)
	}

	// Sanity: this WOULD have failed under the retired fixed +y pole — cart2polar(offset)
	// directly (no kick possible) leaves the offset's bearing arbitrarily close to the
	// azimuth singularity whenever its Theta (colatitude from world +y) is small; nothing
	// in that model can push the reference frame away from an approaching offset.
}

// TestRotatingPolePersistReload drags src's neighbor to trigger a pole kick, flushes,
// then RELOADS from disk into a fresh MoveDispatch and asserts: (1) the persisted pole
// round-trips exactly, and (2) reloading (which re-quantizes nothing on its own — the
// persisted pole is adopted as-is, hasPole=true) does not itself drift the pole further
// (no reload-time oscillation).
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
	pole0, _ := lhSrc.LocalPole()
	srcCenter, _ := md.centerOfNode("src")
	near := fromAxisFrame(pole0, 5*math.Pi/180, 0)
	target := srcCenter.add(polar2cart(polar{R: 50, Theta: near.Theta, Phi: near.Phi}))
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)
	md.quantOffsetPersist.flush()

	poleAfterDrag, hasPole := lhSrc.LocalPole()
	if !hasPole {
		t.Fatal("src has no pole after drag")
	}

	raw, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read src meta: %v", err)
	}
	if !containsKey(raw, "localPoleTheta") || !containsKey(raw, "localPolePhi") {
		t.Fatalf("src meta.json missing persisted local pole: %s", raw)
	}

	// Reload into a fresh MoveDispatch.
	md2 := loadTreeMD(t, root)
	lhSrc2, ok := md2.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src on reload")
	}
	poleReloaded, hasPoleReloaded := lhSrc2.LocalPole()
	if !hasPoleReloaded {
		t.Fatal("reloaded src has no pole")
	}
	const eps = 1e-9
	if math.Abs(poleReloaded.Theta-poleAfterDrag.Theta) > eps || math.Abs(poleReloaded.Phi-poleAfterDrag.Phi) > eps {
		t.Fatalf("pole did not round-trip: before-reload=%+v after-reload=%+v", poleAfterDrag, poleReloaded)
	}

	// No oscillation: re-running the same requantize (same neighbor, same offset) against
	// the reloaded holder must not move the pole further — it is already clear of every
	// neighbor's offset.
	dstCenter, ok := md2.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after reload")
	}
	requantizeLocalPolarsAboutPole(lhSrc2, map[string]vec3{"dst": dstCenter.sub(srcCenter)})
	poleAfterRequantize, _ := lhSrc2.LocalPole()
	if math.Abs(poleAfterRequantize.Theta-poleReloaded.Theta) > eps || math.Abs(poleAfterRequantize.Phi-poleReloaded.Phi) > eps {
		t.Fatalf("pole oscillated on a redundant re-quantize: reloaded=%+v after=%+v", poleReloaded, poleAfterRequantize)
	}
}

// TestComputeLocalPolarsRequantizesStoredBearingAboutResolvedPole guards the load-time
// bug where a STORED localPolars entry's bearing was copied verbatim even though the
// node's pole is resolved from LIVE geometry — so a bearing quantized about a different
// pole (e.g. pre-rotating-pole data quantized about world +y) would be inconsistent with
// the resolved pole. Writes a tree whose src/meta.json carries a stored localPolars
// entry with a bearing that is NOT consistent with the live dst offset (no persisted
// pole, so computeLocalPolars must seed+resolve one from live geometry), and asserts
// that after load, reconstructing the stored bearing under the resolved pole
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
	// any pole — and no localPoleTheta/Phi, so computeLocalPolars must seed a fresh pole
	// from live geometry and (per the fix) re-quantize this stored bearing about it
	// rather than trusting the stale numbers. Role and quantIR must survive untouched.
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
	pole, hasPole := lhSrc.LocalPole()
	if !hasPole {
		t.Fatal("src has no resolved local pole after load")
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
