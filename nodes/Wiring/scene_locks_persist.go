// scene_locks_persist.go — persist + load the polar rule-builder's equations
// (md.polarEqs, locks.go) to scene.json, mirroring scene_overlays_persist.go
// (writer + debounced persister + loader).
//
// Go owns the polar equations authored by the rule-builder (gesture.go
// trySelectSphereRule). Persistence has two triggers: the bare `save` command
// (stdin_reader.go) and an ON-CHANGE debounced write scheduled whenever a rule
// completes. No equation document crosses the TS→Go bridge — Go writes ITS OWN
// current snapshot.
//
// LOAD side: loadScenePolarEqs reads the "polarLocks" key back; MoveDispatch.LoadPolarEqs
// installs it into md.polarEqs on startup — closing the author→reload→still-locked round
// trip. Read-modify-write, serialized against the camera/overlay/fade writers via
// sceneFileMu, so it never clobbers their fields (and vice versa).

package Wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// scenePolarTerm / scenePolarEq are the on-disk JSON shape for one polarTerm / polarEq.
// Comp is written as its name ("theta"/"phi") rather than the raw int so the file stays
// self-describing.
type scenePolarTerm struct {
	Node string  `json:"node"`
	Comp string  `json:"comp"` // "theta" | "phi"
	Sign float64 `json:"sign"` // +1 or -1
}
type scenePolarEq struct {
	Center string         `json:"center"`
	A      scenePolarTerm `json:"a"`
	B      scenePolarTerm `json:"b"`
}

func compToString(c polarComp) string {
	if c == compPhi {
		return "phi"
	}
	return "theta"
}
func compFromString(s string) polarComp {
	if s == "phi" {
		return compPhi
	}
	return compTheta
}

func toScenePolarEq(eq polarEq) scenePolarEq {
	return scenePolarEq{
		Center: eq.Center,
		A:      scenePolarTerm{Node: eq.A.Node, Comp: compToString(eq.A.Comp), Sign: eq.A.Sign},
		B:      scenePolarTerm{Node: eq.B.Node, Comp: compToString(eq.B.Comp), Sign: eq.B.Sign},
	}
}
func fromScenePolarEq(s scenePolarEq) polarEq {
	return polarEq{
		Center: s.Center,
		A:      polarTerm{Node: s.A.Node, Comp: compFromString(s.A.Comp), Sign: s.A.Sign},
		B:      polarTerm{Node: s.B.Node, Comp: compFromString(s.B.Comp), Sign: s.B.Sign},
	}
}

// writeScenePolarEqs writes the current polarEqs snapshot into scenePath (the resolved
// scene.json path, e.g. from sceneCameraPath), preserving every other field.
func writeScenePolarEqs(scenePath string, eqs []polarEq) error {
	path := scenePath
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()

	obj := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &obj)
	}

	if len(eqs) == 0 {
		delete(obj, "polarLocks")
	} else {
		out := make([]scenePolarEq, len(eqs))
		for i, eq := range eqs {
			out[i] = toScenePolarEq(eq)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		obj["polarLocks"] = raw
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

// polarEqsPersister coalesces rapid rule-authoring into a debounced read-modify-write of
// scene.json's "polarLocks" key. Owned by MoveDispatch (armed by EnableEditPersist),
// mirroring overlaysPersister. path == "" (tests that never arm) → no-op.
type polarEqsPersister struct {
	path     string
	debounce time.Duration
	mu       sync.Mutex
	pending  []polarEq
	has      bool
	timer    *time.Timer
	writes   int
}

// schedule records the latest polarEqs snapshot and (re)arms the debounce timer.
func (p *polarEqsPersister) schedule(eqs []polarEq) {
	if p == nil || p.path == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = append([]polarEq(nil), eqs...)
	p.has = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes the pending polarEqs snapshot to scene.json (read-modify-write) and clears it.
func (p *polarEqsPersister) flush() {
	p.mu.Lock()
	eqs, has := p.pending, p.has
	p.has = false
	p.mu.Unlock()
	if !has {
		return
	}
	if err := writeScenePolarEqs(p.path, eqs); err != nil {
		fmt.Fprintf(os.Stderr, "scene_locks_persist: write %s: %v\n", p.path, err)
		return
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// scenePolarLocksFile is the subset of scene.json the polarEqs loader reads.
type scenePolarLocksFile struct {
	PolarLocks []scenePolarEq `json:"polarLocks"`
}

// loadScenePolarEqs reads the persisted polarEqs snapshot from scenePath. Returns nil, false
// when scene.json is absent/malformed OR carries no "polarLocks" key.
func loadScenePolarEqs(scenePath string) ([]polarEq, bool) {
	raw, err := os.ReadFile(scenePath)
	if err != nil {
		return nil, false
	}
	var sf scenePolarLocksFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, false
	}
	if len(sf.PolarLocks) == 0 {
		return nil, false
	}
	out := make([]polarEq, len(sf.PolarLocks))
	for i, s := range sf.PolarLocks {
		out[i] = fromScenePolarEq(s)
	}
	return out, true
}

// LoadPolarEqs reads the polar-equation locks from scene.json (FILE DATA) into md.polarEqs.
// Call after LoadTopology (which builds MoveDispatch's link graph the equations ride on) and
// before EnableEditPersist so no write-back happens from the load itself.
func (md *MoveDispatch) LoadPolarEqs(topologyPath string) {
	scenePath := sceneCameraPath(topologyPath)
	if eqs, ok := loadScenePolarEqs(scenePath); ok {
		md.polarEqs = eqs
	}
}
