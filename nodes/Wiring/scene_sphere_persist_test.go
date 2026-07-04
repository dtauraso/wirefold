package Wiring

import (
	"encoding/json"
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
		"a": {id: "a", geom: nodeGeom{Center: &vec3{X: 0, Y: 0, Z: 0}}},
		"b": {id: "b", geom: nodeGeom{Center: &vec3{X: 100, Y: 0, Z: 0}}},
	}
	for _, nm := range md.nodeMovers {
		nm.snap.Store(&centerSnap{c: *nm.geom.Center})
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

// TestPanSceneSphereHoldsNodesWorldFixed: PanSceneSphere moves the Center by delta, re-fits
// a positive Radius, and leaves every node's WORLD center (centerOfNode) unchanged — nodes
// are held fixed in world space; only the reference frame moves (phase 6, polar-model.md).
func TestPanSceneSphereHoldsNodesWorldFixed(t *testing.T) {
	md := &MoveDispatch{}
	md.nodeMovers = map[string]*nodeMover{
		"a": {id: "a", geom: nodeGeom{Center: &vec3{X: 0, Y: 0, Z: 0}}},
		"b": {id: "b", geom: nodeGeom{Center: &vec3{X: 100, Y: 0, Z: 0}}},
	}
	for _, nm := range md.nodeMovers {
		nm.snap.Store(&centerSnap{c: *nm.geom.Center})
	}
	md.sceneSphere = sceneSphere{Center: vec3{X: 50, Y: 0, Z: 0}, Radius: 60}

	wantA, _ := md.centerOfNode("a")
	wantB, _ := md.centerOfNode("b")

	delta := vec3{X: 10, Y: 5, Z: -3}
	md.PanSceneSphere(delta)

	wantCenter := vec3{X: 60, Y: 5, Z: -3}
	if md.sceneSphere.Center != wantCenter {
		t.Fatalf("Center = %+v, want %+v", md.sceneSphere.Center, wantCenter)
	}
	if md.sceneSphere.Radius <= 0 {
		t.Fatalf("Radius = %v, want > 0", md.sceneSphere.Radius)
	}
	gotA, _ := md.centerOfNode("a")
	gotB, _ := md.centerOfNode("b")
	if gotA != wantA {
		t.Fatalf("node a world moved: got %+v want %+v (held fixed)", gotA, wantA)
	}
	if gotB != wantB {
		t.Fatalf("node b world moved: got %+v want %+v (held fixed)", gotB, wantB)
	}
}

// TestPanSceneSphereThenNodeSaveUpdatesScenePolar: after a pan (new sphere center) and a
// node-position save, the persisted node meta.json scenePolar reflects the NEW center —
// cart2polar(world − newCenter), not the old one.
func TestPanSceneSphereThenNodeSaveUpdatesScenePolar(t *testing.T) {
	root := t.TempDir()
	nodeDir := filepath.Join(root, "nodes", "a")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(nodeDir, "meta.json")
	if err := os.WriteFile(metaPath, []byte(`{"id":"a","type":"Input","x":100,"y":0,"z":0}`), 0o600); err != nil {
		t.Fatal(err)
	}

	md := &MoveDispatch{}
	md.nodeMovers = map[string]*nodeMover{
		"a": {id: "a", geom: nodeGeom{Center: &vec3{X: 100, Y: 0, Z: 0}}},
	}
	md.nodeMovers["a"].snap.Store(&centerSnap{c: *md.nodeMovers["a"].geom.Center})
	md.sceneSphere = sceneSphere{Center: vec3{X: 0, Y: 0, Z: 0}, Radius: 200}

	delta := vec3{X: 30, Y: 0, Z: 0}
	md.PanSceneSphere(delta) // new center (30,0,0)

	world, _ := md.centerOfNode("a")
	sc := vec3{X: 30, Y: 0, Z: 0}
	wantPolar := cart2polar(world.sub(sc))

	// Directly exercise the write side used by the debounced persister's flush.
	if err := writeNodePosition(root, "a", world, sc, true); err != nil {
		t.Fatalf("writeNodePosition: %v", err)
	}
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	numField := func(key string) float64 {
		var v float64
		if err := json.Unmarshal(obj[key], &v); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	if got := numField("scenePolarR"); got != wantPolar.R {
		t.Fatalf("scenePolarR = %v, want %v", got, wantPolar.R)
	}
	if got := numField("scenePolarTheta"); got != wantPolar.Theta {
		t.Fatalf("scenePolarTheta = %v, want %v", got, wantPolar.Theta)
	}
	if got := numField("scenePolarPhi"); got != wantPolar.Phi {
		t.Fatalf("scenePolarPhi = %v, want %v", got, wantPolar.Phi)
	}
}

// TestSceneSpherePersisterFlushNow: flushNow synchronously writes the sphere so
// loadSceneSphere returns ok immediately, without waiting on the debounce timer — this is
// what the bare "save" command relies on to activate the polar-load path.
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
