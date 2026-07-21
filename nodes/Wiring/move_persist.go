package Wiring

// persisters groups the six disk persisters MoveDispatch owns (md.persist), each nil until
// armed by EnableViewpointPersist / EnableEditPersist after the startup seed. Grouping
// mirrors vp/ov/gest: a bare test-constructed MoveDispatch reasons about one zero-value
// sub-struct instead of six loose nilable fields. Each persister writes synchronously the
// moment its value changes (see scene_persist.go's header comment for why the prior debounce
// was removed) — there is no pending-value/clean-shutdown-flush machinery to maintain.
type persisters struct {
	// vp is the camera-viewpoint persister (scene_camera_persist.go), armed by
	// EnableViewpointPersist after the startup seed. nil until armed (old path / tests).
	vp *viewpointPersister
	// pos / anchor are the disk persisters for the two FSM-applied edits (node-drag
	// position, ring-move anchor). Armed by EnableEditPersist after the startup seed; nil
	// until armed (tests that never arm).
	pos      *nodePosPersister
	anchor   *anchorPersister
	overlays *overlaysPersister
	// quantOffset is the disk persister for a node's scalar triple (iTheta,iPhi,iR) about
	// the scene center (quant_offset_persist.go) — the sole persisted position source under
	// the flat polar model. Armed by EnableEditPersist; scheduled from commitNodeMoveLocal
	// for the dragged node.
	quantOffset *quantOffsetPersister
	// sphere is the disk persister for the scene sphere (sphere_layout.go md.sceneSphere),
	// armed by EnableEditPersist. It is only ever flushed — by LoadSceneSphere on a
	// content-fit, and by handleSaveMsg — never scheduled on a value-change, because the
	// sphere is "established once and never moves" (MODEL.md). nil until armed (tests that
	// never arm).
	sphere *sceneSpherePersister
}

// EnableViewpointPersist arms gesture-driven camera persistence: every subsequent
// EmitViewpoint (orbit/zoom/pan/home) writes the current viewpoint to
// `<topologyPath>/view/camera.json` (scene_camera_persist.go). Call AFTER
// SeedInitialViewpoint so the seed's own emit does not write the loaded/default pose back.
// Go owns this write (MODEL.md); the old path persists the camera via its own TS scene-save.
func (md *MoveDispatch) EnableViewpointPersist(topologyPath string) {
	p := &viewpointPersister{path: cameraFilePath(topologyPath)}
	md.persist.vp = p
	md.vp.persist = p.schedule
}

// EnableEditPersist arms disk persistence for the FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's position in <root>/nodes/<id>/position.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/overlays.json
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
	md.persist.pos = &nodePosPersister{root: root}
	md.persist.anchor = &anchorPersister{root: root}
	md.persist.overlays = &overlaysPersister{path: overlaysFilePath(topologyPath)}
	md.persist.sphere = &sceneSpherePersister{path: sphereFilePath(topologyPath)}
	md.persist.quantOffset = &quantOffsetPersister{root: root}
}
