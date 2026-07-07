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
	"os"
	"time"
)

// scenePolarTerm / scenePolarEq are the on-disk JSON shape for one polarTerm / polarEq.
// Comp is written as its name ("theta"/"phi") rather than the raw int so the file stays
// self-describing.
type scenePolarTerm struct {
	Node string  `json:"node"`
	Comp string  `json:"comp"` // "theta" | "phi" | "r"
	Sign float64 `json:"sign"` // +1 or -1
}
type scenePolarEq struct {
	Center string         `json:"center"`
	A      scenePolarTerm `json:"a"`
	B      scenePolarTerm `json:"b"`
	Active *bool          `json:"active,omitempty"` // nil on load = back-compat default true
	// Kind discriminates the equation: absent/"" (back-compat default) or "nodeNode" = today's
	// (node,comp)=(node,comp) equation using Center/A/B above; "portTorus" = a `port ∈ torus`
	// membership lock using the fields below (Center/A/B unused for that kind).
	Kind string `json:"kind,omitempty"`
	// portTorus fields (Kind=="portTorus"). Inert this stage — no geometric effect.
	PortNode    string `json:"portNode,omitempty"`
	PortName    string `json:"portName,omitempty"`
	PortIsInput bool   `json:"portIsInput,omitempty"`
	TorusNode   string `json:"torusNode,omitempty"`
}

func compToString(c polarComp) string {
	switch c {
	case compPhi:
		return "phi"
	case compR:
		return "r"
	default:
		return "theta"
	}
}
func compFromString(s string) polarComp {
	switch s {
	case "phi":
		return compPhi
	case "r":
		return compR
	default:
		return compTheta
	}
}

func toScenePolarEq(eq polarEq) scenePolarEq {
	active := eq.Active
	if eq.Kind == eqPortTorus {
		return scenePolarEq{
			Active:      &active,
			Kind:        "portTorus",
			PortNode:    eq.PortNode,
			PortName:    eq.PortName,
			PortIsInput: eq.PortIsInput,
			TorusNode:   eq.TorusNode,
		}
	}
	return scenePolarEq{
		Center: eq.Center,
		A:      scenePolarTerm{Node: eq.A.Node, Comp: compToString(eq.A.Comp), Sign: eq.A.Sign},
		B:      scenePolarTerm{Node: eq.B.Node, Comp: compToString(eq.B.Comp), Sign: eq.B.Sign},
		Active: &active,
		Kind:   "nodeNode",
	}
}
func fromScenePolarEq(s scenePolarEq) polarEq {
	active := true // back-compat: absent key on already-saved locks defaults to active
	if s.Active != nil {
		active = *s.Active
	}
	if s.Kind == "portTorus" {
		return polarEq{
			Kind:        eqPortTorus,
			Active:      active,
			PortNode:    s.PortNode,
			PortName:    s.PortName,
			PortIsInput: s.PortIsInput,
			TorusNode:   s.TorusNode,
		}
	}
	return polarEq{
		Kind:   eqNodeNode,
		Center: s.Center,
		A:      polarTerm{Node: s.A.Node, Comp: compFromString(s.A.Comp), Sign: s.A.Sign},
		B:      polarTerm{Node: s.B.Node, Comp: compFromString(s.B.Comp), Sign: s.B.Sign},
		Active: active,
	}
}

// writeScenePolarEqs writes the current polarEqs snapshot into scenePath (the resolved
// scene.json path, e.g. from sceneCameraPath), preserving every other field.
func writeScenePolarEqs(scenePath string, eqs []polarEq) error {
	// Marshal BEFORE entering the read-modify-write so a marshal failure and a file-write
	// failure can't both arise inside the callback (which forced an ambiguous error priority).
	var raw []byte
	if len(eqs) > 0 {
		out := make([]scenePolarEq, len(eqs))
		for i, eq := range eqs {
			out[i] = toScenePolarEq(eq)
		}
		var err error
		if raw, err = json.Marshal(out); err != nil {
			return err
		}
	}
	return sceneReadModifyWrite(scenePath, func(obj map[string]json.RawMessage) {
		if len(eqs) == 0 {
			delete(obj, "polarLocks")
			return
		}
		obj["polarLocks"] = raw
	})
}

// polarEqsPersister coalesces rapid rule-authoring into a debounced read-modify-write of
// scene.json's "polarLocks" key. Owned by MoveDispatch (armed by EnableEditPersist),
// mirroring overlaysPersister. path == "" (tests that never arm) → no-op.
type polarEqsPersister struct {
	path     string
	debounce time.Duration
	debouncedPersister[[]polarEq]
}

// schedule records the latest polarEqs snapshot and (re)arms the debounce timer.
func (p *polarEqsPersister) schedule(eqs []polarEq) {
	if p == nil || p.path == "" {
		return
	}
	p.arm(p.debounce, append([]polarEq(nil), eqs...), p.flush)
}

// flush writes the pending polarEqs snapshot to scene.json (read-modify-write) and clears it.
func (p *polarEqsPersister) flush() {
	eqs, has := p.take()
	if !has {
		return
	}
	if err := writeScenePolarEqs(p.path, eqs); err != nil {
		logPersistErr("scene_locks_persist", p.path, err)
		return
	}
	p.recordWrite()
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
		md.setPolarEqs(eqs)
		md.emitPolarLocks(md.tr)
		// A loaded ACTIVE `port ∈ torus` lock geometrically constrains its (edgeless)
		// port to its ring placement — re-emit each such port's geometry now so it
		// starts ON the ring rather than waiting for the first unrelated node move.
		for _, eq := range eqs {
			if eq.Kind == eqPortTorus && eq.Active {
				md.reemitPortTorusGeometry(eq.PortNode)
			}
		}
	}
}
