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
//   - FIRE-AND-FORGET: the write runs on the debounce timer's goroutine and logs on error;
//     it never blocks the gesture.
//
// Two writers touch scene.json (this persister for cameraPolar, writeScene for the rest).
// sceneFileMu (scene_persist.go) serializes their read-modify-write cycles so neither
// clobbers the other's fields, and writeScene preserves the Go-owned cameraPolar under the
// new system.
//
// The debounce/coalesce timer and the JSON read-modify-write/atomic-write plumbing are
// shared machinery from scene_persist.go (debouncedPersister, sceneReadModifyWrite,
// writeJSONAtomic) — this file holds only the camera-specific shape.

import (
	"encoding/json"
	"time"
)

// viewpointPersistDebounce is how long the current viewpoint must be stable before it is
// written. A drag emits a viewpoint every pointermove; this coalesces the burst into a
// single write at gesture settle.
const viewpointPersistDebounce = 250 * time.Millisecond

// viewpointPersister coalesces rapid viewpoint changes into a debounced read-modify-write
// of scene.json's cameraPolar. Owned by MoveDispatch (armed after the startup seed).
type viewpointPersister struct {
	path     string        // scene.json path (sceneCameraPath(topologyPath))
	debounce time.Duration // coalescing window
	debouncedPersister[*scenePolarCamera]
}

// schedule records the latest viewpoint and (re)arms the debounce timer. Each call resets
// the window, so a continuous drag writes once — after motion stops for `debounce`.
func (p *viewpointPersister) schedule(v viewpoint) {
	p.arm(p.debounce, viewpointToPolar(v), p.flush)
}

// flush writes the pending viewpoint to scene.json (read-modify-write, preserving other
// fields) and clears the pending value. Fire-and-forget: errors are logged, not returned.
func (p *viewpointPersister) flush() {
	cam, has := p.take()
	if !has || cam == nil {
		return
	}
	if err := writeSceneCameraPolar(p.path, cam); err != nil {
		logPersistErr("scene_camera_persist", p.path, err)
		return
	}
	p.recordWrite()
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
// just cameraPolar.
func writeSceneCameraPolar(path string, cam *scenePolarCamera) error {
	camJSON, err := json.Marshal(cam)
	if err != nil {
		return err
	}
	return sceneReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		obj["cameraPolar"] = camJSON
	})
}
