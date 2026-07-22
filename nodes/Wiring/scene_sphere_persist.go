// scene_sphere_persist.go — persist + load the first-class SCENE SPHERE (sphere_layout.go
// sceneSphere; the fixed reference every node's scene polar is measured about) to
// view/sphere.json, mirroring scene_camera_persist.go. sphere.json has exactly one writer
// (writeSceneSphere), so each write is a fresh whole-file marshal — no read-modify-write, no
// sceneFileMu (deleted; one-file-per-writer,
// the one-file-per-writer split). loadSceneSphere tries sphere.json
// first and falls back to the legacy scene.json's sceneSphere key for a pre-split topology.
//
// The Center is the only PERSISTED, AUTHORITATIVE cartesian value — the world anchor every
// scene polar is measured about. It is NOT the only cartesian value in the system: the
// camera pose (gesture_camera.go), port anchors (port_geometry.go), bead segments
// (paced_wire.go) and per-node centers (quantized_layout.go deriveCenters) are all cartesian
// too. The invariant is narrower and stronger than "cartesian appears once": every other
// cartesian is DERIVED from this anchor (sceneCenter + polar2cart(…)) or QUARANTINED at the
// renderer edge — none is persisted, and none is a source of truth. Nav stays polar-only
// (guard: tools/check-polar-only-nav.sh). On-disk shape:
//
//	{ "sceneSphere": { "center": [x,y,z], "radius": n } }
//
// Pointer fields distinguish "absent" from a legitimate zero so a partial object is
// rejected (→ content-fit default) rather than silently read as a degenerate sphere.

package Wiring

type sceneSphereJSON struct {
	Center *[3]float64 `json:"center"`
	Radius *float64    `json:"radius"`
}

type sceneSphereFile struct {
	SceneSphere *sceneSphereJSON `json:"sceneSphere"`
}

// loadSceneSphere reads the persisted scene sphere. It tries sphere.json first and falls
// back to the legacy scene.json's sceneSphere key (a pre-split topology) when sphere.json is
// absent/malformed. ok is false when NEITHER yields a complete sphere — callers then
// content-fit.
func loadSceneSphere(topologyPath string) (sceneSphere, bool) {
	var sj sceneSphereJSON
	readJSONBestEffort(sphereFilePath(topologyPath), &sj)
	if sj.Center == nil || sj.Radius == nil {
		// Legacy fallback: pre-split topology only has scene.json's sceneSphere key.
		var sf sceneSphereFile
		readJSONBestEffort(sceneCameraPath(topologyPath), &sf)
		if sf.SceneSphere != nil {
			sj = *sf.SceneSphere
		}
	}
	if sj.Center == nil || sj.Radius == nil {
		return sceneSphere{}, false
	}
	return sceneSphere{
		Center: vec3{X: sj.Center[0], Y: sj.Center[1], Z: sj.Center[2]},
		Radius: *sj.Radius,
	}, true
}

// writeSceneSphere writes the scene sphere as the whole content of sphereJSONPath
// (sphere.json) — the sole writer of that file, so no read-modify-write is needed.
func writeSceneSphere(sphereJSONPath string, s sceneSphere) error {
	center := [3]float64{s.Center.X, s.Center.Y, s.Center.Z}
	radius := s.Radius
	return writeJSONAtomic(sphereJSONPath, sceneSphereJSON{Center: &center, Radius: &radius})
}

// LoadSceneSphere installs md.sceneSphere from FILE DATA, or — when scene.json has no
// persisted sphere — from a one-time content-fit of the current node centers (so an
// existing scene gets a sane reference without any authored value). Call after LoadTopology
// (node centers are loaded) and before the sphere is used to derive positions.
//
// A content-fit fallback is PERSISTED IMMEDIATELY, and that is load-bearing. Every node's
// position is a scene polar measured ABOUT THIS CENTER, so the center must be the same on
// the next load or every position is silently reinterpreted:
//
//	load 1: no sphere -> content-fit S1 -> user drags -> scenePolars persisted about S1
//	load 2: still no sphere -> content-fit over the NEW centers -> S2 != S1
//	        -> every scenePolar now read about S2 -> the whole diagram drifts
//
// The sphere used to reach disk only via the "save" command (spherePersist.flushNow). That
// command has not fired since the old system was erased: its trigger chain (save.ts
// markViewSynced <- store.ts) died with it, so _sendScene() has early-returned
// "not-synced-yet" ever since. The existing topology/ is masked only because its sphere was
// persisted back when save still worked. Any NEW topology walks straight into the drift.
//
// Go owns the authoritative scene state (MODEL.md), so its durability must not depend on the
// webview sending a command. Persisting here removes the last reason for TS to trigger a
// save at all.
func (md *MoveDispatch) LoadSceneSphere(topologyPath string) {
	if s, ok := loadSceneSphere(topologyPath); ok {
		md.sceneSphere = s
	} else {
		// LoadSceneSphere runs on the main goroutine BEFORE Start launches any mover
		// goroutine and before RunStdinReader's dispatch loop begins, so md.positions
		// (which heldCenters reads) is still empty here — use the load-time geom sweep
		// instead (safe: no mover goroutine is mutating geom yet).
		md.sceneSphere = contentFitSceneSphere(md.loadTimeCenters())
		// Best-effort: a read-only or absent scene dir must not stop the sim from running.
		// The in-memory sphere is correct either way; only cross-run stability is at stake.
		// Path via sceneCameraPath (scene_paths.go) — the authoritative resolver, per
		// check-scene-path-resolution.sh; never hand-rolled.
		if topologyPath != "" {
			_ = writeSceneSphere(sphereFilePath(topologyPath), md.sceneSphere)
		}
	}
	// Emit the scene sphere ONCE at load, on both paths: it is established here and never
	// moves again (MODEL.md), so this is the single source-of-truth broadcast the renderer
	// uses in place of deriving a content-sphere centroid from live node positions.
	if md.tr != nil {
		c := md.sceneSphere.Center
		md.tr.SceneSphere(c.X, c.Y, c.Z, md.sceneSphere.Radius)
	}
}

// sceneSpherePersister writes the scene sphere to view/sphere.json, mirroring
// overlaysPersister. path == "" ⇒ no-op (tests that never arm persistence). The sphere is
// "established once and never moves" (MODEL.md) — flushNow is its only writer, called by
// LoadSceneSphere's content-fit and by the "save" command (handleSaveMsg); there was never a
// debounced schedule() for it to begin with.
type sceneSpherePersister struct {
	path string
}

// flushNow writes the current sphere synchronously.
func (p *sceneSpherePersister) flushNow(s sceneSphere) {
	if p == nil || p.path == "" {
		return
	}
	if err := writeSceneSphere(p.path, s); err != nil {
		logPersistErr("scene_sphere_persist", p.path, err)
		return
	}
}
