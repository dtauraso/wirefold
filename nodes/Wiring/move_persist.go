package Wiring

// persisters groups the six debounced disk persisters MoveDispatch owns (md.persist),
// each nil until armed by EnableViewpointPersist / EnableEditPersist after the startup
// seed. Grouping mirrors vp/ov/gest: a bare test-constructed MoveDispatch reasons about
// one zero-value sub-struct instead of six loose nilable fields.
type persisters struct {
	// vp is the debounced camera-viewpoint persister (scene_camera_persist.go), armed by
	// EnableViewpointPersist after the startup seed. nil until armed (old path / tests).
	vp *viewpointPersister
	// pos / anchor are the debounced disk persisters for the two FSM-applied edits
	// (node-drag position, ring-move anchor). Armed by EnableEditPersist after the startup
	// seed; nil until armed (tests that never arm).
	pos      *nodePosPersister
	anchor   *anchorPersister
	overlays *overlaysPersister
	// quantOffset is the debounced disk persister for a node's scalar triple
	// (iTheta,iPhi,iR) about the scene center (quant_offset_persist.go) — the sole
	// persisted position source under the flat polar model. Armed by EnableEditPersist;
	// scheduled from RootMove for the dragged node.
	quantOffset *quantOffsetPersister
	// sphere is the debounced disk persister for the scene sphere (sphere_layout.go
	// md.sceneSphere), armed by EnableEditPersist. Its DEBOUNCE has no caller by design: the
	// sphere is "established once and never moves" (MODEL.md), so there is nothing to coalesce.
	// It is only ever flushed — by LoadSceneSphere on a content-fit, and by handleSaveMsg.
	// nil until armed (tests that never arm).
	sphere *sceneSpherePersister
}

// flushPendingPersists synchronously flushes every debounced persister's pending value on
// clean process shutdown (RunStdinReader's stdin-EOF/channel-close return paths). Without
// this, a drag/gesture that lands within the 250ms debounce window of exit is silently
// abandoned — the write never happens and the edit reverts on the next load. Each persister
// is nil-guarded (some may be unarmed in tests/headless runs); pos is an inert stub
// (writes nothing) and is intentionally skipped.
func (md *MoveDispatch) flushPendingPersists() {
	if md == nil {
		return
	}
	md.persist.quantOffset.flushPending()
	md.persist.anchor.flushPending()
	md.persist.vp.flushPending()
	md.persist.overlays.flushPending()
	// The scene sphere is established once at load and never moves again (MODEL.md), so its
	// debounce is rarely pending, but flushing it here too is cheap and matches the "save"
	// command's behavior (handleSaveMsg) for completeness.
	if md.persist.sphere != nil {
		md.persist.sphere.flushNow(md.sceneSphere)
	}
}

// EnableViewpointPersist arms gesture-driven camera persistence: every subsequent
// EmitViewpoint (orbit/zoom/pan/home) debounces a write of the current viewpoint to
// `<topologyPath>/view/scene.json`'s cameraPolar (scene_camera_persist.go). Call AFTER
// SeedInitialViewpoint so the seed's own emit does not write the loaded/default pose back.
// Go owns this write (MODEL.md); the old path persists the camera via its own TS scene-save.
func (md *MoveDispatch) EnableViewpointPersist(topologyPath string) {
	p := &viewpointPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.persist.vp = p
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
	md.persist.pos = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.persist.anchor = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.persist.overlays = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.persist.sphere = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.persist.quantOffset = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}
