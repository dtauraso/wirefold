package Wiring

// flushPendingPersists synchronously flushes every debounced persister's pending value on
// clean process shutdown (RunStdinReader's stdin-EOF/channel-close return paths). Without
// this, a drag/gesture that lands within the 250ms debounce window of exit is silently
// abandoned — the write never happens and the edit reverts on the next load. Each persister
// is nil-guarded (some may be unarmed in tests/headless runs); posPersist is an inert stub
// (writes nothing) and is intentionally skipped.
func (md *MoveDispatch) flushPendingPersists() {
	if md == nil {
		return
	}
	md.quantOffsetPersist.flushPending()
	md.anchorPersist.flushPending()
	md.vpPersist.flushPending()
	md.overlaysPersist.flushPending()
	// The scene sphere is established once at load and never moves again (MODEL.md), so its
	// debounce is rarely pending, but flushing it here too is cheap and matches the "save"
	// command's behavior (handleSaveMsg) for completeness.
	if md.spherePersist != nil {
		md.spherePersist.flushNow(md.sceneSphere)
	}
}

// EnableViewpointPersist arms gesture-driven camera persistence: every subsequent
// EmitViewpoint (orbit/zoom/pan/home) debounces a write of the current viewpoint to
// `<topologyPath>/view/scene.json`'s cameraPolar (scene_camera_persist.go). Call AFTER
// SeedInitialViewpoint so the seed's own emit does not write the loaded/default pose back.
// Go owns this write (MODEL.md); the old path persists the camera via its own TS scene-save.
func (md *MoveDispatch) EnableViewpointPersist(topologyPath string) {
	p := &viewpointPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.vpPersist = p
	md.vp.persist = p.schedule
}

// EnableEditPersist arms disk persistence for the FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's x/y/z in <root>/nodes/<id>/meta.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/scene.json
//
// Node-position + anchor persistence needs the per-node/per-port files of the directory-tree
// form; for a monolithic topology.json (no per-node files) their root is "" and those two
// persisters no-op. Call AFTER SeedInitialViewpoint so the seed emits do not write the
// loaded state back.
func (md *MoveDispatch) EnableEditPersist(topologyPath string) {
	// sceneTreeRoot handles both the directory form and the file-inside-tree form (and
	// returns "" for a true monolithic topology with no tree), making the two-form bug
	// class unrepresentable here. Do not hand-roll os.Stat/IsDir — use sceneTreeRoot.
	root := sceneTreeRoot(topologyPath)
	md.posPersist = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.anchorPersist = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.overlaysPersist = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.spherePersist = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.quantOffsetPersist = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}
