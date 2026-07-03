package Wiring

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// scene_camera_persist_test.go — the WRITE side of camera-viewpoint-as-file-data. These
// pin: a full FSM→file→loadSceneViewpoint round-trip, preservation of the other scene.json
// fields across the camera write, debounce coalescing of a rapid gesture burst, and that
// writeScene (the other scene.json writer) does not clobber the Go-owned cameraPolar under
// the new system.

// vpEqual asserts two FSM viewpoint tuples match within tolerance.
func vpEqual(t *testing.T, gotPivot vec3, gotR float64, gotPos, gotUp dir, wantPivot vec3, wantR float64, wantPos, wantUp dir) {
	t.Helper()
	if !vecClose(gotPivot, wantPivot, 1e-9) {
		t.Fatalf("pivot=%v want %v", gotPivot, wantPivot)
	}
	if math.Abs(gotR-wantR) > 1e-9 {
		t.Fatalf("r=%v want %v", gotR, wantR)
	}
	if math.Abs(gotPos.Theta-wantPos.Theta) > 1e-9 || math.Abs(gotPos.Phi-wantPos.Phi) > 1e-9 {
		t.Fatalf("pos=%v want %v", gotPos, wantPos)
	}
	if math.Abs(gotUp.Theta-wantUp.Theta) > 1e-9 || math.Abs(gotUp.Phi-wantUp.Phi) > 1e-9 {
		t.Fatalf("up=%v want %v", gotUp, wantUp)
	}
}

// TestPersistViewpointRoundTrips sets a viewpoint via the FSM, triggers the persist, and
// reads the file back with loadSceneViewpoint — the exact inverse — asserting equality.
func TestPersistViewpointRoundTrips(t *testing.T) {
	td := t.TempDir()
	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.EnableViewpointPersist(td)

	wantPivot := vec3{X: 10, Y: 20, Z: 30}
	wantR := 250.0
	wantPos := dir{Theta: 1.1, Phi: 2.2}
	wantUp := dir{Theta: 0.3, Phi: 0.4}

	md.SetViewpoint(wantPivot, wantR, wantPos, wantUp)
	md.EmitViewpoint(nil) // schedules the debounced write (gesture path is EmitViewpoint)
	md.vpPersist.flush()  // force the coalesced write now (no timing dependence)

	pivot, r, pos, up, ok := loadSceneViewpoint(td)
	if !ok {
		t.Fatalf("loadSceneViewpoint: ok=false after persist")
	}
	vpEqual(t, pivot, r, pos, up, wantPivot, wantR, wantPos, wantUp)
}

// TestPersistPreservesOtherSceneFields asserts the camera write is a read-modify-write:
// unrelated scene.json fields (camera3d, labelsGlobalHidden, an overlay flag) survive.
func TestPersistPreservesOtherSceneFields(t *testing.T) {
	td := t.TempDir()
	viewDir := filepath.Join(td, "view")
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatalf("mkdir view: %v", err)
	}
	scenePath := filepath.Join(viewDir, "scene.json")
	orig := `{
	  "camera3d": {"position": [1,2,3], "quaternion": [0,0,0,1]},
	  "labelsGlobalHidden": true,
	  "sceneToriVisible": false,
	  "cameraPolar": {"pivot": [0,0,0], "r": 5, "pos": [0,0], "up": [0,0]}
	}`
	if err := os.WriteFile(scenePath, []byte(orig), 0o644); err != nil {
		t.Fatalf("write scene.json: %v", err)
	}

	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.EnableViewpointPersist(td)
	md.SetViewpoint(vec3{X: 7, Y: 8, Z: 9}, 123, dir{Theta: 1, Phi: 2}, dir{Theta: 0.1, Phi: 0.2})
	md.EmitViewpoint(nil)
	md.vpPersist.flush()

	raw, err := os.ReadFile(scenePath)
	if err != nil {
		t.Fatalf("read scene.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("scene.json not valid JSON: %v", err)
	}
	// Other fields survive.
	for _, k := range []string{"camera3d", "labelsGlobalHidden", "sceneToriVisible"} {
		if _, ok := obj[k]; !ok {
			t.Fatalf("field %q was clobbered by the camera write", k)
		}
	}
	if string(obj["labelsGlobalHidden"]) != "true" {
		t.Fatalf("labelsGlobalHidden=%s want true", obj["labelsGlobalHidden"])
	}
	if string(obj["sceneToriVisible"]) != "false" {
		t.Fatalf("sceneToriVisible=%s want false", obj["sceneToriVisible"])
	}
	// cameraPolar updated to the new pose.
	pivot, r, pos, up, ok := loadSceneViewpoint(td)
	if !ok {
		t.Fatalf("loadSceneViewpoint: ok=false")
	}
	vpEqual(t, pivot, r, pos, up, vec3{X: 7, Y: 8, Z: 9}, 123, dir{Theta: 1, Phi: 2}, dir{Theta: 0.1, Phi: 0.2})
}

// TestPersistDebounceCoalesces schedules many rapid viewpoint changes (a drag burst) and
// asserts a single flush produces exactly ONE write carrying the FINAL value — the debounce
// keeps per-frame writes off the hot path.
func TestPersistDebounceCoalesces(t *testing.T) {
	td := t.TempDir()
	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.EnableViewpointPersist(td)
	// Stop the debounce timer so it cannot fire during the burst; we flush explicitly and
	// assert coalescing (many schedules → one write).
	md.SetViewpoint(vec3{}, 1, dir{}, dir{})
	for i := 0; i < 50; i++ {
		md.SetViewpoint(vec3{X: float64(i)}, float64(100+i), dir{Theta: float64(i) * 0.01}, dir{Phi: float64(i) * 0.02})
		md.EmitViewpoint(nil) // schedule; resets the debounce window each time
	}
	if md.vpPersist.timer != nil {
		md.vpPersist.timer.Stop()
	}
	md.vpPersist.flush()

	if got := md.vpPersist.writes; got != 1 {
		t.Fatalf("writes=%d want 1 (burst should coalesce to a single write)", got)
	}
	// Final value is the last scheduled viewpoint (i=49).
	pivot, r, pos, up, ok := loadSceneViewpoint(td)
	if !ok {
		t.Fatalf("loadSceneViewpoint: ok=false")
	}
	vpEqual(t, pivot, r, pos, up,
		vec3{X: 49}, 149, dir{Theta: 49 * 0.01}, dir{Phi: 49 * 0.02})

	// A second flush with nothing pending does not write again.
	md.vpPersist.flush()
	if got := md.vpPersist.writes; got != 1 {
		t.Fatalf("writes=%d want 1 after empty flush", got)
	}
}

// TestWriteSceneOverlaysPreservesCameraPolar asserts the OTHER scene.json writer
// (writeSceneOverlays, the bare `save` command's overlay persister) does not clobber the
// Go-owned cameraPolar — the double-writer guard — while it DOES persist the current
// overlay-visibility snapshot.
func TestWriteSceneOverlaysPreservesCameraPolar(t *testing.T) {
	td := t.TempDir()

	// Go persists a camera first.
	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.EnableViewpointPersist(td)
	md.SetViewpoint(vec3{X: 11, Y: 22, Z: 33}, 321, dir{Theta: 0.5, Phi: 1.5}, dir{Theta: 0.05, Phi: 0.15})
	md.EmitViewpoint(nil)
	md.vpPersist.flush()

	// A save persists Go's OWN overlay state. Here labelsGlobal is hidden (visible=false),
	// which must land as labelsGlobalHidden:true in scene.json.
	ov := defaultOverlayState()
	ov.labelsGlobalVisible = false
	if err := writeSceneOverlays(td, ov); err != nil {
		t.Fatalf("writeSceneOverlays: %v", err)
	}

	// The Go-owned camera survives; the overlay field is applied.
	pivot, r, pos, up, ok := loadSceneViewpoint(td)
	if !ok {
		t.Fatalf("loadSceneViewpoint: ok=false")
	}
	vpEqual(t, pivot, r, pos, up, vec3{X: 11, Y: 22, Z: 33}, 321, dir{Theta: 0.5, Phi: 1.5}, dir{Theta: 0.05, Phi: 0.15})

	raw, _ := os.ReadFile(filepath.Join(td, "view", "scene.json"))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("scene.json invalid: %v", err)
	}
	if string(obj["labelsGlobalHidden"]) != "true" {
		t.Fatalf("labelsGlobalHidden=%s want true (overlay field should persist)", obj["labelsGlobalHidden"])
	}
}
