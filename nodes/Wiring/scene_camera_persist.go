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
//   - SYNCHRONOUS: schedule() writes camera.json immediately, inline on the calling
//     goroutine (the stdin/gesture goroutine — every gesture path serializes through it, so
//     there is only ever one writer). No debounce: see scene_persist.go's header comment for
//     why the prior 250ms coalescing window was removed.
//   - WHOLE-FILE: camera.json holds ONLY the camera pose (one-file-per-writer,
//     the one-file-per-writer split) — no other writer touches it,
//     so each write marshals the pose fresh and overwrites the file, no read-modify-write.
//   - FIRE-AND-FORGET: errors are logged, not returned; it never blocks the gesture.
//
// Before this split, camera.json's content lived at scene.json's `cameraPolar` key,
// shared with the overlays and sphere writers under sceneFileMu. That lock is gone
// (scene_persist.go); an existing pre-split scene.json still loads — see
// loadSceneViewpoint's legacy fallback in scene_camera.go.
//
// The atomic-write plumbing is shared machinery from scene_persist.go (writeJSONAtomic) —
// this file holds only the camera-specific shape.

// viewpointPersister writes viewpoint changes to camera.json as they happen. Owned by
// MoveDispatch (armed after the startup seed).
type viewpointPersister struct {
	path string // camera.json path (cameraFilePath(topologyPath))
}

// schedule writes the given viewpoint to camera.json synchronously. Fire-and-forget:
// errors are logged, not returned.
func (p *viewpointPersister) schedule(v viewpoint) {
	if p == nil || p.path == "" {
		return
	}
	cam := viewpointToPolar(v)
	if err := writeSceneCameraPolar(p.path, cam); err != nil {
		logPersistErr("scene_camera_persist", p.path, err)
		return
	}
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
