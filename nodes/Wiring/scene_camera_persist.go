package Wiring

// scene_camera_persist.go — the WRITE side of camera-viewpoint-as-file-data.
//
// The read side (scene_camera.go) loads the saved polar camera from
// `<topologyPath>/view/scene.json` into the gesture-FSM viewpoint on startup. This file
// is the mirror: whenever a GESTURE changes the FSM viewpoint (orbit/zoom/pan/home), Go
// persists the current viewpoint back to that same file's `cameraPolar` field, in the
// EXACT schema loadSceneViewpoint reads, so navigate-then-reload round-trips.
//
// Go owns persistence (MODEL.md): there is no TS→Go camera-save on the new path. The
// write is:
//   - DEBOUNCED: a drag emits a viewpoint every pointermove; we coalesce and write once
//     the viewpoint has been stable for a beat (viewpointPersistDebounce), off the hot path.
//   - READ-MODIFY-WRITE: scene.json also holds camera3d, overlay flags, etc. We parse the
//     existing file, replace ONLY `cameraPolar`, and write it back — other fields survive.
//   - GATED to the new system (WIREFOLD_NEW_SYSTEM=="true"); the old path persists the
//     camera via its own TS scene-save instead.
//   - FIRE-AND-FORGET: the write runs on the debounce timer's goroutine and logs on error;
//     it never blocks the gesture.
//
// Two writers touch scene.json (this persister for cameraPolar, writeScene for the rest).
// sceneFileMu serializes their read-modify-write cycles so neither clobbers the other's
// fields, and writeScene preserves the Go-owned cameraPolar under the new system.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// newSystemEnabled reports whether the new-system flag is on. Mirrors the gate main.go
// uses for SeedInitialViewpoint (the read side), so read and write persist together.
func newSystemEnabled() bool {
	return os.Getenv("WIREFOLD_NEW_SYSTEM") == "true"
}

// sceneFileMu serializes read-modify-write cycles on view/scene.json across the two
// writers (this camera persister and writeScene) so their field updates do not race.
var sceneFileMu sync.Mutex

// viewpointPersistDebounce is how long the current viewpoint must be stable before it is
// written. A drag emits a viewpoint every pointermove; this coalesces the burst into a
// single write at gesture settle.
const viewpointPersistDebounce = 250 * time.Millisecond

// viewpointPersister coalesces rapid viewpoint changes into a debounced read-modify-write
// of scene.json's cameraPolar. Owned by MoveDispatch (armed after the startup seed).
type viewpointPersister struct {
	path     string        // scene.json path (sceneCameraPath(topologyPath))
	debounce time.Duration // coalescing window
	mu       sync.Mutex
	pending  *scenePolarCamera // latest viewpoint awaiting write; nil when flushed
	timer    *time.Timer
	writes   int // count of completed writes (test observability)
}

// schedule records the latest viewpoint and (re)arms the debounce timer. Each call resets
// the window, so a continuous drag writes once — after motion stops for `debounce`.
func (p *viewpointPersister) schedule(v viewpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = viewpointToPolar(v)
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes the pending viewpoint to scene.json (read-modify-write, preserving other
// fields) and clears the pending value. Fire-and-forget: errors are logged, not returned.
func (p *viewpointPersister) flush() {
	p.mu.Lock()
	cam := p.pending
	p.pending = nil
	p.mu.Unlock()
	if cam == nil {
		return
	}
	if err := writeSceneCameraPolar(p.path, cam); err != nil {
		fmt.Fprintf(os.Stderr, "scene_camera_persist: write %s: %v\n", p.path, err)
		return
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// viewpointToPolar converts an FSM viewpoint to the persisted cameraPolar shape. It is the
// exact inverse of loadSceneViewpoint's mapping, so a load→persist→load round-trips.
func viewpointToPolar(v viewpoint) *scenePolarCamera {
	pivot := [3]float64{v.pivot.X, v.pivot.Y, v.pivot.Z}
	r := v.r
	pos := [2]float64{v.pos.Theta, v.pos.Phi}
	up := [2]float64{v.up.Theta, v.up.Phi}
	return &scenePolarCamera{Pivot: &pivot, R: &r, Pos: &pos, Up: &up}
}

// writeSceneCameraPolar sets ONLY the cameraPolar field of scene.json, preserving every
// other field (camera3d, overlay flags, …). If the file/dir is absent it is created with
// just cameraPolar. Serialized against writeScene via sceneFileMu.
func writeSceneCameraPolar(path string, cam *scenePolarCamera) error {
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()

	obj := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		// Best-effort: a malformed existing file is replaced (we cannot preserve fields
		// we cannot parse), but cameraPolar is still written so the camera persists.
		_ = json.Unmarshal(raw, &obj)
	}
	camJSON, err := json.Marshal(cam)
	if err != nil {
		return err
	}
	obj["cameraPolar"] = camJSON
	out, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
