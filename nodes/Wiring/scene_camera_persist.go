package Wiring

// scene_camera_persist.go — the WRITE side of camera-viewpoint-as-file-data.
//
// The read side (scene_camera.go) loads the saved polar camera from
// `<topologyPath>/view/camera.json` into the gesture-FSM viewpoint on startup. This file
// is the mirror: whenever a GESTURE changes the FSM viewpoint (orbit/zoom/pan/home), Go
// persists the current viewpoint back to that same file, in the EXACT schema
// loadSceneViewpoint reads, so navigate-then-reload round-trips.
//
// Go owns persistence (MODEL.md): there is no TS→Go camera-save on the new path. The
// write is:
//   - DEBOUNCED: a drag emits a viewpoint every pointermove; we coalesce and write once
//     the viewpoint has been stable for a beat (viewpointPersistDebounce), off the hot path.
//   - WHOLE-FILE: camera.json holds ONLY the camera pose (one-file-per-writer,
//     docs/planning/visual-editor/one-file-per-goroutine.md) — no other writer touches it,
//     so each flush marshals the pose fresh and overwrites the file, no read-modify-write.
//   - FIRE-AND-FORGET: the write runs on the debounce timer's goroutine and logs on error;
//     it never blocks the gesture.
//
// Before this split, camera.json's content lived at scene.json's `cameraPolar` key,
// shared with the overlays and sphere writers under sceneFileMu. That lock is gone
// (scene_persist.go); an existing pre-split scene.json still loads — see
// loadSceneViewpoint's legacy fallback in scene_camera.go.
//
// The debounce/coalesce timer and the atomic-write plumbing are shared machinery from
// scene_persist.go (debouncedPersister, writeJSONAtomic) — this file holds only the
// camera-specific shape.

import (
	"time"
)

// viewpointPersistDebounce is how long the current viewpoint must be stable before it is
// written. A drag emits a viewpoint every pointermove; this coalesces the burst into a
// single write at gesture settle.
const viewpointPersistDebounce = 250 * time.Millisecond

// viewpointPersister coalesces rapid viewpoint changes into a debounced whole-file write of
// camera.json. Owned by MoveDispatch (armed after the startup seed).
type viewpointPersister struct {
	path     string        // camera.json path (cameraFilePath(topologyPath))
	debounce time.Duration // coalescing window
	debouncedPersister[*scenePolarCamera]
}

// schedule records the latest viewpoint and (re)arms the debounce timer. Each call resets
// the window, so a continuous drag writes once — after motion stops for `debounce`.
func (p *viewpointPersister) schedule(v viewpoint) {
	p.arm(p.debounce, viewpointToPolar(v), p.flush)
}

// flush writes the pending viewpoint to camera.json (whole-file write) and clears the
// pending value. Fire-and-forget: errors are logged, not returned.
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

// flushPending cancels any pending debounce timer and synchronously writes whatever is
// still pending, for the clean-shutdown path (RunStdinReader) — a camera move within the
// debounce window of process exit would otherwise be silently lost.
func (p *viewpointPersister) flushPending() {
	if p == nil {
		return
	}
	p.stop()
	p.flush()
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

// writeSceneCameraPolar writes cam as the whole content of path (camera.json) — the sole
// writer of that file, so no read-modify-write is needed.
func writeSceneCameraPolar(path string, cam *scenePolarCamera) error {
	return writeJSONAtomic(path, cam)
}
