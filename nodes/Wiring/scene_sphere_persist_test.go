package Wiring

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSceneSphereRoundTrip: writeSceneSphere then loadSceneSphere returns the same sphere,
// preserving other scene.json keys.
func TestSceneSphereRoundTrip(t *testing.T) {
	dir := t.TempDir()
	scenePath := sceneCameraPath(dir)
	if err := os.MkdirAll(filepath.Dir(scenePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed an unrelated key to prove read-modify-write preserves it.
	if err := os.WriteFile(scenePath, []byte(`{"cameraPolar":{"r":42}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	want := sceneSphere{Center: vec3{X: 10, Y: -20, Z: 30}, Radius: 250}
	if err := writeSceneSphere(scenePath, want); err != nil {
		t.Fatalf("writeSceneSphere: %v", err)
	}
	got, ok := loadSceneSphere(dir)
	if !ok {
		t.Fatal("loadSceneSphere: ok=false after write")
	}
	if got != want {
		t.Fatalf("round-trip: got %+v want %+v", got, want)
	}
	// The unrelated key must survive the sphere write.
	raw, _ := os.ReadFile(scenePath)
	if !contains(string(raw), `"cameraPolar"`) {
		t.Fatalf("writeSceneSphere clobbered cameraPolar: %s", raw)
	}
}

// TestSceneSphereDefaultsFromContentFit: with no persisted sphere, LoadSceneSphere falls
// back to a content-fit of the node centers rather than a zero sphere.
func TestSceneSphereDefaultsFromContentFit(t *testing.T) {
	md := &MoveDispatch{}
	md.nodeMovers = map[string]*nodeMover{
		"a": {id: "a", geom: nodeGeom{HasPos: true, ScenePolar: cart2polar(vec3{X: 0, Y: 0, Z: 0})}},
		"b": {id: "b", geom: nodeGeom{HasPos: true, ScenePolar: cart2polar(vec3{X: 100, Y: 0, Z: 0})}},
	}
	for _, nm := range md.nodeMovers {
		nm.snap.Store(&centerSnap{c: nodeWorldPos(nm.geom)})
	}
	md.LoadSceneSphere(t.TempDir()) // no scene.json → content-fit
	if md.sceneSphere.Radius <= 0 {
		t.Fatalf("content-fit sphere has non-positive radius: %+v", md.sceneSphere)
	}
	// Center should be the bbox midpoint (≈ (50,0,0)), not the origin default.
	if md.sceneSphere.Center.X < 40 || md.sceneSphere.Center.X > 60 {
		t.Fatalf("content-fit center X=%v, want ≈50", md.sceneSphere.Center.X)
	}
}

// TestSceneSphereContentFitSurvivesReloadAfterMove pins the invariant the content-fit
// persist exists for: the scene center must NOT be re-derived from moved nodes.
//
// Every node position is a scene polar measured about this center, so a center that shifts
// between runs silently reinterprets the whole diagram. Load 1 content-fits S1 and the user
// drags; load 2 must still see S1. If the fallback is not persisted, load 2 content-fits
// over the NEW centers, gets S2 != S1, and every position drifts.
//
// A plain "does it write the file" assertion would NOT catch that — it passes while the
// second load still recomputes. Drive two real loads across a move instead.
func TestSceneSphereContentFitSurvivesReloadAfterMove(t *testing.T) {
	dir := t.TempDir()

	newMD := func(bx float64) *MoveDispatch {
		md := &MoveDispatch{}
		md.nodeMovers = map[string]*nodeMover{
			"a": {id: "a", geom: nodeGeom{HasPos: true, ScenePolar: cart2polar(vec3{X: 0, Y: 0, Z: 0})}},
			"b": {id: "b", geom: nodeGeom{HasPos: true, ScenePolar: cart2polar(vec3{X: bx, Y: 0, Z: 0})}},
		}
		for _, nm := range md.nodeMovers {
			nm.snap.Store(&centerSnap{c: nodeWorldPos(nm.geom)})
		}
		return md
	}

	// Load 1: no scene.json → content-fit S1, which must be persisted.
	md1 := newMD(100)
	md1.LoadSceneSphere(dir)
	s1 := md1.sceneSphere
	if s1.Radius <= 0 {
		t.Fatalf("load 1: content-fit sphere has non-positive radius: %+v", s1)
	}

	// The user drags node b far away. Its scene polar was measured about S1.
	// Load 2: a NEW process over the MOVED tree. It must read S1 back, not re-fit.
	md2 := newMD(900)
	md2.LoadSceneSphere(dir)
	s2 := md2.sceneSphere

	if s2.Center != s1.Center || s2.Radius != s1.Radius {
		t.Fatalf("scene sphere drifted across reload after a move:\n  load 1: %+v\n  load 2: %+v\n"+
			"every node's scenePolar is measured about this center, so the diagram would shift.", s1, s2)
	}
}

func TestSceneSpherePersisterFlushNow(t *testing.T) {
	dir := t.TempDir()
	p := &sceneSpherePersister{path: sceneCameraPath(dir), debounce: time.Hour}
	s := sceneSphere{Center: vec3{X: 1, Y: 2, Z: 3}, Radius: 40}
	p.flushNow(s)

	got, ok := loadSceneSphere(dir)
	if !ok {
		t.Fatal("loadSceneSphere: ok=false after flushNow")
	}
	if got != s {
		t.Fatalf("flushNow round-trip: got %+v want %+v", got, s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
