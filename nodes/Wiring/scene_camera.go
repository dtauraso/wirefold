package Wiring

// scene_camera.go — the initial camera VIEWPOINT is FILE DATA, not a computed seed.
//
// The saved camera lives in the scene sidecar `<topologyPath>/view/scene.json`, written
// by the editor (the same `cameraPolar` shape TS's parseViewerState/serializeSceneState
// round-trip). On startup Go reads that file itself and installs the parsed pose into the
// gesture-FSM viewpoint (SetViewpoint + EmitViewpoint), so the buffer camera columns carry
// a REAL saved pose from the first frame — non-degenerate, so pan works immediately. This
// replaces the rejected on-load "home" command the webview used to send (a compute-fit
// seed): the viewpoint is PERSISTED STATE, so Go loads it from the file.
//
// If the file is absent/empty/malformed (a fresh topology), Go falls back to a fixed,
// clearly-labelled DEFAULT viewpoint (defaultViewpoint) — a valid non-degenerate basis,
// NOT a geometry fit. That keeps pan working before the first save without seeding from
// the scene's node positions.

import (
	"encoding/json"
	"math"
	"os"

	T "github.com/dtauraso/wirefold/Trace"
)

// SeedInitialViewpoint installs the initial camera viewpoint from FILE DATA. It loads the
// saved polar camera from `<topologyPath>/view/scene.json` (or the fixed default when the
// file is absent/malformed) and installs it into the gesture-FSM viewpoint via
// SetViewpoint + EmitViewpoint — the exact path a gesture uses — so the pose streams out to
// the buffer camera columns. Called on startup only under the new system; the old render
// path restores the camera via the webview's PolarCameraRestorer instead.
func SeedInitialViewpoint(topologyPath string, md *MoveDispatch, tr *T.Trace) {
	if md == nil {
		return
	}
	pivot, r, pos, up, ok := loadSceneViewpoint(topologyPath)
	if !ok {
		pivot, r, pos, up = defaultViewpoint()
	}
	md.SetViewpoint(pivot, r, pos, up)
	md.EmitViewpoint(tr)
}

// scenePolarCamera mirrors TS's persisted `cameraPolar` JSON shape exactly
// (camera-store.ts PolarCamera / parse.ts parsePolarCamera):
//
//	{ "pivot": [x,y,z], "r": n, "pos": [theta,phi], "up": [theta,phi] }
//
// Pointer fields distinguish "absent" from a legitimate zero so a partial object is
// rejected rather than silently read as a degenerate pose.
type scenePolarCamera struct {
	Pivot *[3]float64 `json:"pivot"`
	R     *float64    `json:"r"`
	Pos   *[2]float64 `json:"pos"`
	Up    *[2]float64 `json:"up"`
}

// sceneFile is the subset of scene.json Go reads: the persisted polar camera.
// Other scene fields (camera3d, guide-visibility flags) are ignored here.
type sceneFile struct {
	CameraPolar *scenePolarCamera `json:"cameraPolar"`
}

// loadSceneViewpoint reads the saved polar camera from the scene sidecar and converts it
// to the FSM viewpoint tuple (pivot, r, pos, up). ok is false when the file is
// absent/empty/malformed or carries no complete cameraPolar — callers then use
// defaultViewpoint. The mapping is 1:1 with the TS schema:
//
//	pivot = (pivot[0], pivot[1], pivot[2])   r = r
//	pos   = {Theta: pos[0], Phi: pos[1]}     up = {Theta: up[0], Phi: up[1]}
func loadSceneViewpoint(topologyPath string) (pivot vec3, r float64, pos, up dir, ok bool) {
	raw, err := os.ReadFile(sceneCameraPath(topologyPath))
	if err != nil {
		return vec3{}, 0, dir{}, dir{}, false
	}
	var sf sceneFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return vec3{}, 0, dir{}, dir{}, false
	}
	cp := sf.CameraPolar
	// Require every field (matches parsePolarCamera, which drops a partial object).
	if cp == nil || cp.Pivot == nil || cp.R == nil || cp.Pos == nil || cp.Up == nil {
		return vec3{}, 0, dir{}, dir{}, false
	}
	pivot = vec3{X: cp.Pivot[0], Y: cp.Pivot[1], Z: cp.Pivot[2]}
	r = *cp.R
	pos = dir{Theta: cp.Pos[0], Phi: cp.Pos[1]}
	up = dir{Theta: cp.Up[0], Phi: cp.Up[1]}
	return pivot, r, pos, up, true
}

// defaultViewpointR is the fallback orbit distance when no camera is saved yet. It is a
// plain sane default, NOT a fit computed from node positions.
const defaultViewpointR = 500.0

// defaultViewpoint is the fixed, non-degenerate fallback pose used when the scene sidecar
// has no saved camera (fresh topology). It is deliberately NOT a geometry fit — it is a
// square-on view of the origin with a valid screen basis so PAN works before the first
// save:
//
//	pivot = origin
//	pos   = +Z  (theta=pi/2, phi=pi/2 → anglesToWorldOffset = (0,0,1))  — camera looks along -Z
//	up    = +Y  (theta=0            → anglesToWorldOffset = (0,1,0))
//
// pos (+Z) and up (+Y) are orthogonal, so basisFromViewpoint (up × pole) is non-degenerate
// — the exact bug the old degenerate zero-value viewpoint (pos = up = +Y) caused for pan.
func defaultViewpoint() (pivot vec3, r float64, pos, up dir) {
	return vec3{X: 0, Y: 0, Z: 0},
		defaultViewpointR,
		dir{Theta: math.Pi / 2, Phi: math.Pi / 2}, // +Z, square-on (matches the home pose's +z look)
		dir{Theta: 0, Phi: 0} // +Y up
}
