// scene_overlays_persist.go — persist + load Go's OWN overlay-visibility state to
// view/overlays.json (writer + debounced persister + loader + seed, mirroring
// scene_camera_persist.go).
//
// Go owns the overlay flags (overlay_gen.go's overlayState). Persistence has two triggers:
// the bare `save` command (stdin_reader.go) and — like camera — an ON-CHANGE synchronous
// write scheduled whenever an overlays update lands (applyUpdate toggle/set); see
// scene_persist.go's header comment for why the prior debounce was removed. The camera pose
// is written the same way by scene_camera_persist.go; this file handles the overlay half. No
// scene document crosses the TS→Go bridge — Go writes ITS OWN current snapshot.
//
// LOAD side: loadSceneOverlays reads the keys back (inverting the *Hidden polarity) and
// MoveDispatch.LoadOverlays installs them into md.ov on startup + emits them so the first
// snapshot reflects the saved state — closing the toggle→reload→still-toggled round trip.
// It tries overlays.json first and falls back to the legacy scene.json's overlay keys (a
// pre-split topology) — see loadSceneOverlays.
//
// WHOLE-FILE write (one-file-per-writer, the one-file-per-writer split):
// overlays.json holds ONLY these flags and has exactly one writer, so each flush marshals the
// current overlayState fresh — no read-modify-write, no sceneFileMu (deleted). The key names
// + polarity + default-omission mirror TS's serializeSceneState (webview/state/viewer/types.ts):
// most flags are visible-sense written only when hidden (false); labelsGlobalHidden/badgesHidden
// are hidden-sense written only when hidden (true); a key at its default is deleted so the
// on-disk shape matches what the editor would have written. An old scene.json that still
// carries a "badgesHidden" key (from before the occlusion-badge feature was removed) is
// tolerated: json.Unmarshal into sceneOverlaysFile silently ignores unknown keys, so it is
// dropped on the next save without needing an explicit migration.
//
// The atomic-write plumbing is shared machinery from scene_persist.go (writeJSONAtomic) —
// this file holds only the overlays-specific shape.

package Wiring

import (
	"encoding/json"

	T "github.com/dtauraso/wirefold/Trace"
)

// writeSceneOverlays writes the current overlay-visibility snapshot as the WHOLE content of
// overlaysPath (overlays.json) — the sole writer of that file, so each call builds a fresh
// object (no read-modify-write of any prior content).
func writeSceneOverlays(overlaysPath string, ov overlayState) error {
	obj := map[string]json.RawMessage{}
	// visible-sense: default true — write `false` only when hidden, else drop the key.
	setVisible := func(key string, visible bool) {
		if !visible {
			obj[key] = json.RawMessage("false")
		}
	}
	// hidden-sense: default visible — write `true` only when hidden, else drop the key.
	setHidden := func(key string, visible bool) {
		if !visible {
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
	}
	return writeJSONAtomic(overlaysPath, obj)
}

// overlaysPersister writes overlay toggles/sets to overlays.json as they happen. Owned by
// MoveDispatch (armed by EnableEditPersist). path == "" (tests that never arm) → no-op.
type overlaysPersister struct {
	path string // overlays.json path (overlaysFilePath(topologyPath))
}

// schedule writes the given overlay snapshot to overlays.json synchronously.
func (p *overlaysPersister) schedule(ov overlayState) {
	if p == nil || p.path == "" {
		return
	}
	if err := writeSceneOverlays(p.path, ov); err != nil {
		logPersistErr("scene_overlays_persist", p.path, err)
		return
	}
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

// loadSceneOverlays reads the persisted overlay-visibility snapshot, applying the same key
// names + polarity the writer used (visible-sense keys straight through; the two *Hidden
// keys inverted back to visible-sense). It tries overlaysPath (overlays.json) first and
// falls back to legacyScenePath (the old shared scene.json, a pre-split topology) when
// overlaysPath carries no overlay keys. Starts from defaultOverlayState so any key the
// writer omitted (because it was at its default) keeps the code default. The bool return is
// false when NEITHER file yields an overlay key (fresh topology) — the caller then keeps the
// code defaults.
func loadSceneOverlays(overlaysPath, legacyScenePath string) (overlayState, bool) {
	ov := defaultOverlayState()
	var sf sceneOverlaysFile
	readJSONBestEffort(overlaysPath, &sf)
	if sf == (sceneOverlaysFile{}) {
		// Legacy fallback: pre-split topology only has scene.json's overlay keys.
		readJSONBestEffort(legacyScenePath, &sf)
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
	ov, _ := loadSceneOverlays(overlaysFilePath(topologyPath), sceneCameraPath(topologyPath)) // ov = defaults with any persisted keys applied
	if tr != nil {
		md.ov.SetGuideVisibility(ov, tr) // ALWAYS emit so the buffer reflects the state
	} else {
		md.ov = ov
	}
	// Decentralized (Step C, per-owner-buffer-rows.md): the gesture/stdin-reader goroutine
	// also writes its own VIEW frame directly, carrying the same 8 one-time overlay-flag
	// events SetGuideVisibility's tr.Emit* calls above already logged on the fd-3 fallback
	// path — one RowEvent per flag kind, matching that per-kind count. Every overlay kind
	// decodes entirely from the VIEW frame's own Overlay block (buffer-log.ts's
	// decodeEventLine OVERLAY_KINDS branch) — no row identity to resolve.
	md.emitViewFrame([]RowEvent{
		{Kind: T.KindSceneTori, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindScenePoles, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindNodePoles, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindSelSpherePoles, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindHandholds, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindLabelsGlobal, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindOverlaysVis, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
		{Kind: T.KindDoubleLinks, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1},
	})
}
