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
// and overlay writers via sceneFileMu so the three scene.json writers never clobber each
// other. An empty set deletes its key so the on-disk shape matches a fresh scene.
//
// READ side: loadSceneFade parses those arrays back; MoveDispatch.SeedFade seeds the FSM
// sets on startup and emits the full seeds via tr.Fade so the fixpoint is rebuilt — closing
// the toggle→reload→still-faded round trip.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// fadePersister coalesces rapid fade toggles into a debounced read-modify-write of
// scene.json's fadedNodes/fadedEdges. Owned by MoveDispatch (armed by EnableEditPersist).
type fadePersister struct {
	path       string // scene.json path (sceneCameraPath(topologyPath))
	debounce   time.Duration
	mu         sync.Mutex
	pendNodes  []string
	pendEdges  []string
	hasPending bool
	timer      *time.Timer
	writes     int
}

// schedule records the latest fade seed snapshot and (re)arms the debounce timer.
func (p *fadePersister) schedule(nodes, edges []string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pendNodes = append([]string(nil), nodes...)
	p.pendEdges = append([]string(nil), edges...)
	p.hasPending = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes the pending fade seeds to scene.json (read-modify-write) and clears pending.
func (p *fadePersister) flush() {
	p.mu.Lock()
	nodes, edges, has := p.pendNodes, p.pendEdges, p.hasPending
	p.pendNodes, p.pendEdges, p.hasPending = nil, nil, false
	p.mu.Unlock()
	if !has {
		return
	}
	if err := writeSceneFade(p.path, nodes, edges); err != nil {
		fmt.Fprintf(os.Stderr, "scene_fade_persist: write %s: %v\n", p.path, err)
		return
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// writeSceneFade sets ONLY the fadedNodes/fadedEdges fields of scene.json, preserving every
// other field (cameraPolar, overlays, …). Empty sets delete their key so the shape matches a
// fresh scene. Serialized against the camera + overlay writers via sceneFileMu. Seeds are
// sorted so the on-disk order is deterministic.
func writeSceneFade(path string, nodes, edges []string) error {
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()

	obj := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &obj)
	}

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
