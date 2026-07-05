// scene_sphere_persist.go — persist + load the first-class SCENE SPHERE (sphere_layout.go
// sceneSphere; the fixed reference every node's scene polar is measured about) to
// scene.json, mirroring scene_camera_persist.go / scene_locks_persist.go (read-modify-write
// of one key, serialized against the other scene writers via sceneFileMu).
//
// The Center is the ONE cartesian value in the system — the world anchor. On-disk shape:
//
//	{ "sceneSphere": { "center": [x,y,z], "radius": n } }
//
// Pointer fields distinguish "absent" from a legitimate zero so a partial object is
// rejected (→ content-fit default) rather than silently read as a degenerate sphere.

package Wiring

import (
	"encoding/json"
	"os"
	"time"
)

type sceneSphereJSON struct {
	Center *[3]float64 `json:"center"`
	Radius *float64    `json:"radius"`
}

type sceneSphereFile struct {
	SceneSphere *sceneSphereJSON `json:"sceneSphere"`
}

// loadSceneSphere reads the persisted scene sphere from scene.json. ok is false when the
// file is absent/malformed or carries no complete sceneSphere — callers then content-fit.
func loadSceneSphere(topologyPath string) (sceneSphere, bool) {
	raw, err := os.ReadFile(sceneCameraPath(topologyPath))
	if err != nil {
		return sceneSphere{}, false
	}
	var sf sceneSphereFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return sceneSphere{}, false
	}
	s := sf.SceneSphere
	if s == nil || s.Center == nil || s.Radius == nil {
		return sceneSphere{}, false
	}
	return sceneSphere{
		Center: vec3{X: s.Center[0], Y: s.Center[1], Z: s.Center[2]},
		Radius: *s.Radius,
	}, true
}

// writeSceneSphere writes the scene sphere into scene.json's "sceneSphere" key, preserving
// every other field (read-modify-write, serialized via sceneFileMu like the sibling writers).
func writeSceneSphere(scenePath string, s sceneSphere) error {
	center := [3]float64{s.Center.X, s.Center.Y, s.Center.Z}
	radius := s.Radius
	rawSphere, err := json.Marshal(sceneSphereJSON{Center: &center, Radius: &radius})
	if err != nil {
		return err
	}
	return sceneReadModifyWrite(scenePath, func(obj map[string]json.RawMessage) {
		obj["sceneSphere"] = rawSphere
	})
}

// LoadSceneSphere installs md.sceneSphere from FILE DATA, or — when scene.json has no
// persisted sphere — from a one-time content-fit of the current node centers (so an
// existing scene gets a sane reference without any authored value). Call after LoadTopology
// (node centers are loaded) and before the sphere is used to derive positions. Phase 1: it
// only stores the sphere; nothing derives from it yet.
func (md *MoveDispatch) LoadSceneSphere(topologyPath string) {
	if s, ok := loadSceneSphere(topologyPath); ok {
		md.sceneSphere = s
		return
	}
	md.sceneSphere = contentFitSceneSphere(md.heldCenters())
}

// PanSceneSphere moves the scene sphere's Center by the SAME delta as a camera pan (phase 6:
// pan moves the scene sphere; orbit does NOT — callers must only invoke this from the pan
// path, never from orbit). Node WORLD positions are held fixed here; only the reference
// Center moves, so every node's SCENE polar (measured about Center) changes implicitly and
// is recomputed the next time it is read/persisted (scene_node_pos_persist.go's dual-write).
// Radius re-fits around the new Center via fitSceneRadius so it keeps covering the diagram.
// Schedules a debounced disk write when persistence is armed (EnableEditPersist); path=="" /
// nil persister is a no-op (tests that never arm).
func (md *MoveDispatch) PanSceneSphere(delta vec3) {
	md.sceneSphere.Center = md.sceneSphere.Center.add(delta)
	md.sceneSphere.Radius = fitSceneRadius(md.heldCenters(), md.sceneSphere.Center)
	if md.spherePersist != nil {
		md.spherePersist.schedule(md.sceneSphere)
	}
}

// sceneSpherePersister coalesces rapid pans into a debounced read-modify-write of
// scene.json's "sceneSphere" key, mirroring overlaysPersister/fadePersister. path == "" ⇒
// no-op (tests that never arm persistence).
type sceneSpherePersister struct {
	path     string
	debounce time.Duration
	debouncedPersister[sceneSphere]
}

// schedule records the latest sphere snapshot and (re)arms the debounce timer.
func (p *sceneSpherePersister) schedule(s sceneSphere) {
	if p == nil || p.path == "" {
		return
	}
	p.arm(p.debounce, s, p.flush)
}

// flush writes the pending sphere to scene.json and clears it. Fire-and-forget: errors are
// logged, not returned.
func (p *sceneSpherePersister) flush() {
	s, has := p.take()
	if !has {
		return
	}
	if err := writeSceneSphere(p.path, s); err != nil {
		logPersistErr("scene_sphere_persist", p.path, err)
		return
	}
	p.recordWrite()
}

// flushNow synchronously writes the current sphere, bypassing the debounce — used by the
// "save" command so the sphere is guaranteed persisted at save time even if the debounce
// timer hasn't fired yet.
func (p *sceneSpherePersister) flushNow(s sceneSphere) {
	if p == nil || p.path == "" {
		return
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	if err := writeSceneSphere(p.path, s); err != nil {
		logPersistErr("scene_sphere_persist", p.path, err)
	}
}
