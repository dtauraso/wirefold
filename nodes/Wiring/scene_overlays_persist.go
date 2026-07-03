// scene_overlays_persist.go — persist Go's OWN overlay-visibility state to scene.json.
//
// The bare `save` command (stdin_reader.go) tells Go to persist its authoritative scene
// state. The camera pose is already continuously flushed by scene_camera_persist.go; this
// writer handles the overlay-visibility half. Go owns the overlay flags (overlay_gen.go's
// overlayState), so a save is Go writing ITS OWN current snapshot — no scene document
// crosses the TS→Go bridge.
//
// Read-modify-write, serialized against writeSceneCameraPolar via sceneFileMu, so the
// Go-owned cameraPolar (and any other fields) survive. The scene.json key names + polarity
// + default-omission mirror TS's serializeSceneState (webview/state/viewer/types.ts): most
// flags are visible-sense written only when hidden (false); labelsGlobalHidden/badgesHidden
// are hidden-sense written only when hidden (true); a key at its default is deleted so the
// on-disk shape matches what the editor would have written.

package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// writeSceneOverlays writes the current overlay-visibility snapshot into
// <treeRoot>/view/scene.json, preserving every other field (cameraPolar, camera3d, …).
func writeSceneOverlays(treeRoot string, ov overlayState) error {
	path := filepath.Join(treeRoot, "view", "scene.json")
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()

	obj := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		// Best-effort: a malformed existing file is replaced, but the overlay keys are
		// still written so visibility persists.
		_ = json.Unmarshal(raw, &obj)
	}

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
	setVisible("angleLabelsVisible", ov.angleLabelsVisible)
	setVisible("selSpherePolesVisible", ov.selSpherePolesVisible)
	setVisible("handholdsVisible", ov.handholdsVisible)
	setVisible("overlaysActive", ov.overlaysVisible)
	setHidden("labelsGlobalHidden", ov.labelsGlobalVisible)
	setHidden("badgesHidden", ov.badgesGlobalVisible)
	// doubleLinksVisible is visible-sense with a FALSE default — write `true` only when on.
	if ov.doubleLinksVisible {
		obj["doubleLinksVisible"] = json.RawMessage("true")
	} else {
		delete(obj, "doubleLinksVisible")
	}

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
