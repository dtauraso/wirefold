// scene_overlays_persist.go — persist + load Go's OWN overlay-visibility state to scene.json,
// mirroring scene_fade_persist.go (writer + debounced persister + loader + seed).
//
// Go owns the overlay flags (overlay_gen.go's overlayState). Persistence has two triggers:
// the bare `save` command (stdin_reader.go) and — like fade/camera — an ON-CHANGE debounced
// write scheduled whenever an overlays update lands (applyUpdate toggle/set). The camera pose
// is continuously flushed by scene_camera_persist.go; this file handles the overlay half. No
// scene document crosses the TS→Go bridge — Go writes ITS OWN current snapshot.
//
// LOAD side: loadSceneOverlays reads the keys back (inverting the *Hidden polarity) and
// MoveDispatch.SeedOverlays installs them into md.ov on startup + emits them so the first
// snapshot reflects the saved state — closing the toggle→reload→still-toggled round trip.
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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// writeSceneOverlays writes the current overlay-visibility snapshot into
// scenePath (the resolved scene.json path, e.g. from sceneCameraPath), preserving
// every other field (cameraPolar, camera3d, …).
func writeSceneOverlays(scenePath string, ov overlayState) error {
	path := scenePath
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

// overlaysPersister coalesces rapid overlay toggles/sets into a debounced read-modify-write
// of scene.json's overlay-visibility keys. Owned by MoveDispatch (armed by EnableEditPersist),
// mirroring fadePersister. path == "" (tests that never arm) → no-op.
type overlaysPersister struct {
	path     string // scene.json path (sceneCameraPath(topologyPath))
	debounce time.Duration
	mu       sync.Mutex
	pending  overlayState
	has      bool
	timer    *time.Timer
	writes   int
}

// schedule records the latest overlay snapshot and (re)arms the debounce timer.
func (p *overlaysPersister) schedule(ov overlayState) {
	if p == nil || p.path == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = ov
	p.has = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes the pending overlay snapshot to scene.json (read-modify-write) and clears it.
func (p *overlaysPersister) flush() {
	p.mu.Lock()
	ov, has := p.pending, p.has
	p.has = false
	p.mu.Unlock()
	if !has {
		return
	}
	if err := writeSceneOverlays(p.path, ov); err != nil {
		fmt.Fprintf(os.Stderr, "scene_overlays_persist: write %s: %v\n", p.path, err)
		return
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// sceneOverlaysFile is the subset of scene.json the overlay loader reads. Pointer fields
// distinguish an ABSENT key (keep the code default) from a present false/true — the writer
// omits any key at its default, so absence must not be read as false. Key names + polarity
// mirror writeSceneOverlays (setVisible / setHidden / doubleLinksVisible) exactly.
type sceneOverlaysFile struct {
	SceneToriVisible      *bool `json:"sceneToriVisible"`
	ScenePolesVisible     *bool `json:"scenePolesVisible"`
	NodePolesVisible      *bool `json:"nodePolesVisible"`
	AngleLabelsVisible    *bool `json:"angleLabelsVisible"`
	SelSpherePolesVisible *bool `json:"selSpherePolesVisible"`
	HandholdsVisible      *bool `json:"handholdsVisible"`
	OverlaysActive        *bool `json:"overlaysActive"`
	LabelsGlobalHidden    *bool `json:"labelsGlobalHidden"`
	BadgesHidden          *bool `json:"badgesHidden"`
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
	if sf.AngleLabelsVisible != nil {
		ov.angleLabelsVisible = *sf.AngleLabelsVisible
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
	if sf.BadgesHidden != nil {
		ov.badgesGlobalVisible = !*sf.BadgesHidden
		found = true
	}
	if sf.DoubleLinksVisible != nil {
		ov.doubleLinksVisible = *sf.DoubleLinksVisible
		found = true
	}
	return ov, found
}

// SeedOverlays installs the persisted overlay-visibility snapshot from scene.json into md.ov
// on startup and emits each flag so the first buffer snapshot streams the loaded values (the
// UI shows them after a reload). Call after LoadTopology (which builds MoveDispatch) and, like
// SeedFade, BEFORE EnableEditPersist so the seed's own emit does not write the loaded state
// back. topologyPath is passed to sceneCameraPath, which handles both the directory-tree and
// monolithic forms. A scene.json with no overlay keys keeps the code defaults.
func (md *MoveDispatch) SeedOverlays(topologyPath string, tr *T.Trace) {
	scenePath := sceneCameraPath(topologyPath)
	ov, found := loadSceneOverlays(scenePath)
	if !found {
		return
	}
	if tr != nil {
		md.ov.SetGuideVisibility(ov, tr)
	} else {
		md.ov = ov
	}
}
