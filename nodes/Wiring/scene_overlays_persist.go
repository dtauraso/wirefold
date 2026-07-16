// scene_overlays_persist.go — persist + load Go's OWN overlay-visibility state to scene.json
// (writer + debounced persister + loader + seed, mirroring scene_camera_persist.go).
//
// Go owns the overlay flags (overlay_gen.go's overlayState). Persistence has two triggers:
// the bare `save` command (stdin_reader.go) and — like camera — an ON-CHANGE debounced
// write scheduled whenever an overlays update lands (applyUpdate toggle/set). The camera pose
// is continuously flushed by scene_camera_persist.go; this file handles the overlay half. No
// scene document crosses the TS→Go bridge — Go writes ITS OWN current snapshot.
//
// LOAD side: loadSceneOverlays reads the keys back (inverting the *Hidden polarity) and
// MoveDispatch.LoadOverlays installs them into md.ov on startup + emits them so the first
// snapshot reflects the saved state — closing the toggle→reload→still-toggled round trip.
//
// Read-modify-write, serialized against writeSceneCameraPolar via sceneFileMu, so the
// Go-owned cameraPolar (and any other fields) survive. The scene.json key names + polarity
// + default-omission mirror TS's serializeSceneState (webview/state/viewer/types.ts): most
// flags are visible-sense written only when hidden (false); labelsGlobalHidden/badgesHidden
// are hidden-sense written only when hidden (true); a key at its default is deleted so the
// on-disk shape matches what the editor would have written. An old scene.json that still
// carries a "badgesHidden" key (from before the occlusion-badge feature was removed) is
// tolerated: json.Unmarshal into sceneOverlaysFile silently ignores unknown keys, so it is
// dropped on the next save without needing an explicit migration.
//
// The debounce/coalesce timer and the JSON read-modify-write/atomic-write plumbing are
// shared machinery from scene_persist.go (debouncedPersister, sceneReadModifyWrite,
// writeJSONAtomic) — this file holds only the overlays-specific shape.

package Wiring

import (
	"encoding/json"
	"os"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// writeSceneOverlays writes the current overlay-visibility snapshot into
// scenePath (the resolved scene.json path, e.g. from sceneCameraPath), preserving
// every other field (cameraPolar, camera3d, …).
func writeSceneOverlays(scenePath string, ov overlayState) error {
	return sceneReadModifyWrite(scenePath, func(obj map[string]json.RawMessage) {
		// visible-sense: default true — write `false` only when hidden, else drop the key.
		setVisible := func(key string, visible bool) {
			if visible {
				delete(obj, key)
			} else {
				obj[key] = json.RawMessage("false")
			}
		}
		// hidden-sense: default visible — write `true` only when hidden, else drop the key.
		setHidden := func(key string, visible bool) {
			if visible {
				delete(obj, key)
			} else {
				obj[key] = json.RawMessage("true")
			}
		}

		setVisible("sceneToriVisible", ov.sceneToriVisible)
		setVisible("scenePolesVisible", ov.scenePolesVisible)
		setVisible("nodePolesVisible", ov.nodePolesVisible)
		setVisible("selSpherePolesVisible", ov.selSpherePolesVisible)
		setVisible("handholdsVisible", ov.handholdsVisible)
		setVisible("overlaysActive", ov.overlaysVisible)
		setHidden("labelsGlobalHidden", ov.labelsGlobalVisible)
		// doubleLinksVisible is visible-sense with a FALSE default — write `true` only when on.
		if ov.doubleLinksVisible {
			obj["doubleLinksVisible"] = json.RawMessage("true")
		} else {
			delete(obj, "doubleLinksVisible")
		}
	})
}

// overlaysPersister coalesces rapid overlay toggles/sets into a debounced read-modify-write
// of scene.json's overlay-visibility keys. Owned by MoveDispatch (armed by EnableEditPersist).
// path == "" (tests that never arm) → no-op.
type overlaysPersister struct {
	path     string // scene.json path (sceneCameraPath(topologyPath))
	debounce time.Duration
	debouncedPersister[overlayState]
}

// schedule records the latest overlay snapshot and (re)arms the debounce timer.
func (p *overlaysPersister) schedule(ov overlayState) {
	if p == nil || p.path == "" {
		return
	}
	p.arm(p.debounce, ov, p.flush)
}

// flush writes the pending overlay snapshot to scene.json (read-modify-write) and clears it.
func (p *overlaysPersister) flush() {
	ov, has := p.take()
	if !has {
		return
	}
	if err := writeSceneOverlays(p.path, ov); err != nil {
		logPersistErr("scene_overlays_persist", p.path, err)
		return
	}
	p.recordWrite()
}

// sceneOverlaysFile is the subset of scene.json the overlay loader reads. Pointer fields
// distinguish an ABSENT key (keep the code default) from a present false/true — the writer
// omits any key at its default, so absence must not be read as false. Key names + polarity
// mirror writeSceneOverlays (setVisible / setHidden) exactly.
type sceneOverlaysFile struct {
	SceneToriVisible      *bool `json:"sceneToriVisible"`
	ScenePolesVisible     *bool `json:"scenePolesVisible"`
	NodePolesVisible      *bool `json:"nodePolesVisible"`
	SelSpherePolesVisible *bool `json:"selSpherePolesVisible"`
	HandholdsVisible      *bool `json:"handholdsVisible"`
	OverlaysActive        *bool `json:"overlaysActive"`
	LabelsGlobalHidden    *bool `json:"labelsGlobalHidden"`
	DoubleLinksVisible    *bool `json:"doubleLinksVisible"`
}

// loadSceneOverlays reads the persisted overlay-visibility snapshot from scenePath (the
// resolved scene.json path, e.g. from sceneCameraPath), applying the same key names +
// polarity the writer used (visible-sense keys straight through; the two *Hidden keys
// inverted back to visible-sense). Starts from defaultOverlayState so any key the writer
// omitted (because it was at its default) keeps the code default. The bool return is false
// when scene.json is absent/malformed OR carries no overlay keys (fresh topology) — the
// caller then keeps the code defaults.
func loadSceneOverlays(scenePath string) (overlayState, bool) {
	ov := defaultOverlayState()
	raw, err := os.ReadFile(scenePath)
	if err != nil {
		return ov, false
	}
	var sf sceneOverlaysFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return ov, false
	}
	found := false
	if sf.SceneToriVisible != nil {
		ov.sceneToriVisible = *sf.SceneToriVisible
		found = true
	}
	if sf.ScenePolesVisible != nil {
		ov.scenePolesVisible = *sf.ScenePolesVisible
		found = true
	}
	if sf.NodePolesVisible != nil {
		ov.nodePolesVisible = *sf.NodePolesVisible
		found = true
	}
	if sf.SelSpherePolesVisible != nil {
		ov.selSpherePolesVisible = *sf.SelSpherePolesVisible
		found = true
	}
	if sf.HandholdsVisible != nil {
		ov.handholdsVisible = *sf.HandholdsVisible
		found = true
	}
	if sf.OverlaysActive != nil {
		ov.overlaysVisible = *sf.OverlaysActive
		found = true
	}
	if sf.LabelsGlobalHidden != nil {
		ov.labelsGlobalVisible = !*sf.LabelsGlobalHidden
		found = true
	}
	if sf.DoubleLinksVisible != nil {
		ov.doubleLinksVisible = *sf.DoubleLinksVisible
		found = true
	}
	return ov, found
}

// LoadOverlays reads the overlay-visibility state from scene.json (FILE DATA) into md.ov and
// streams it so the buffer reflects the current overlay state from the first frame. A scene.json
// with no overlay keys resolves to the code defaults (loadSceneOverlays starts from
// defaultOverlayState and applies any present keys) — and those defaults are STILL emitted, so
// the UI shows the default-visible overlays instead of an all-off buffer. Call after LoadTopology
// (which builds MoveDispatch) and BEFORE EnableEditPersist so this emit does not write the
// loaded/default state back. topologyPath is passed to sceneCameraPath, which handles both the
// directory-tree and monolithic forms.
func (md *MoveDispatch) LoadOverlays(topologyPath string, tr *T.Trace) {
	scenePath := sceneCameraPath(topologyPath)
	ov, _ := loadSceneOverlays(scenePath) // ov = defaults with any persisted keys applied
	if tr != nil {
		md.ov.SetGuideVisibility(ov, tr) // ALWAYS emit so the buffer reflects the state
	} else {
		md.ov = ov
	}
}
