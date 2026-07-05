package Wiring

// scene_fade_persist.go — persist + load the Go-owned fade SEED sets as view/scene.json data.
//
// Fade is view/display state, so it lives alongside the camera + overlays in
// `<topologyPath>/view/scene.json`. The FSM owns the directly-faded SEED sets
// (MoveDispatch.directlyFadedNodes / directlyFadedEdges — the node ids / edge labels the
// user pressed "f" on); the buffer snapshot recomputes the fade fixpoint from those seeds.
// We persist the SEEDS (not the computed fixpoint):
//
//	{ ..., "fadedNodes": ["9", ...], "fadedEdges": ["e0", ...] }
//
// WRITE side: ToggleFadeSelection schedules a debounced read-modify-write that replaces only
// fadedNodes/fadedEdges, preserving cameraPolar/overlays/etc. Serialized against the camera
// and overlay writers via sceneFileMu (scene_persist.go) so the scene.json writers never
// clobber each other. An empty set deletes its key so the on-disk shape matches a fresh scene.
//
// READ side: loadSceneFade parses those arrays back; MoveDispatch.SeedFade seeds the FSM
// sets on startup and emits the full seeds via tr.Fade so the fixpoint is rebuilt — closing
// the toggle→reload→still-faded round trip.
//
// The debounce/coalesce timer and the JSON read-modify-write/atomic-write plumbing are
// shared machinery from scene_persist.go (debouncedPersister, sceneReadModifyWrite,
// writeJSONAtomic) — this file holds only the fade-specific shape.

import (
	"encoding/json"
	"os"
	"sort"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// fadeSeeds is the fade-specific payload coalesced by fadePersister's debounce timer.
type fadeSeeds struct {
	nodes []string
	edges []string
}

// fadePersister coalesces rapid fade toggles into a debounced read-modify-write of
// scene.json's fadedNodes/fadedEdges. Owned by MoveDispatch (armed by EnableEditPersist).
type fadePersister struct {
	path     string // scene.json path (sceneCameraPath(topologyPath))
	debounce time.Duration
	debouncedPersister[fadeSeeds]
}

// schedule records the latest fade seed snapshot and (re)arms the debounce timer.
func (p *fadePersister) schedule(nodes, edges []string) {
	if p == nil {
		return
	}
	v := fadeSeeds{
		nodes: append([]string(nil), nodes...),
		edges: append([]string(nil), edges...),
	}
	p.arm(p.debounce, v, p.flush)
}

// flush writes the pending fade seeds to scene.json (read-modify-write) and clears pending.
func (p *fadePersister) flush() {
	v, has := p.take()
	if !has {
		return
	}
	if err := writeSceneFade(p.path, v.nodes, v.edges); err != nil {
		logPersistErr("scene_fade_persist", p.path, err)
		return
	}
	p.recordWrite()
}

// writeSceneFade sets ONLY the fadedNodes/fadedEdges fields of scene.json, preserving every
// other field (cameraPolar, overlays, …). Empty sets delete their key so the shape matches a
// fresh scene. Seeds are sorted so the on-disk order is deterministic.
func writeSceneFade(path string, nodes, edges []string) error {
	return sceneReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		setSeeds := func(key string, seeds []string) {
			if len(seeds) == 0 {
				delete(obj, key)
				return
			}
			s := append([]string(nil), seeds...)
			sort.Strings(s)
			b, _ := json.Marshal(s)
			obj[key] = b
		}
		setSeeds("fadedNodes", nodes)
		setSeeds("fadedEdges", edges)
	})
}

// sceneFadeFile is the subset of scene.json the fade loader reads.
type sceneFadeFile struct {
	FadedNodes []string `json:"fadedNodes"`
	FadedEdges []string `json:"fadedEdges"`
}

// loadSceneFade reads the persisted fade seed sets from scene.json. Absent/malformed → empty.
func loadSceneFade(topologyPath string) (nodes, edges []string) {
	raw, err := os.ReadFile(sceneCameraPath(topologyPath))
	if err != nil {
		return nil, nil
	}
	var sf sceneFadeFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, nil
	}
	return sf.FadedNodes, sf.FadedEdges
}

// SeedFade seeds the FSM's directly-faded sets from scene.json on startup and emits the full
// seeds via tr.Fade so the buffer snapshot rebuilds the fade fixpoint. Call after LoadTopology
// (which builds MoveDispatch) so a persisted fade is restored from the first frame. It does
// NOT arm persistence — EnableEditPersist does that — so the seed emit does not write back.
func (md *MoveDispatch) SeedFade(topologyPath string, tr *T.Trace) {
	nodes, edges := loadSceneFade(topologyPath)
	if len(nodes) == 0 && len(edges) == 0 {
		return
	}
	if md.directlyFadedNodes == nil {
		md.directlyFadedNodes = map[string]bool{}
	}
	if md.directlyFadedEdges == nil {
		md.directlyFadedEdges = map[string]bool{}
	}
	for _, n := range nodes {
		md.directlyFadedNodes[n] = true
	}
	for _, e := range edges {
		md.directlyFadedEdges[e] = true
	}
	if tr != nil {
		tr.Fade(setToSlice(md.directlyFadedNodes), setToSlice(md.directlyFadedEdges))
	}
}
