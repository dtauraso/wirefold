package Wiring

// scene_locks_persist_test.go — round-trip test for the polar rule-builder equation
// persister (scene_locks_persist.go): write → read back preserving sibling scene.json
// fields (mirrors scene_edit_persist_test.go's pattern; writeTree lives there).

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPolarEqsRoundTrip(t *testing.T) {
	root := writeTree(t)
	scenePath := sceneCameraPath(root)

	// A sibling field (overlay visibility) must survive the polarEqs write untouched.
	if err := writeSceneOverlays(scenePath, overlayState{sceneToriVisible: false}); err != nil {
		t.Fatalf("writeSceneOverlays: %v", err)
	}

	eqs := []polarEq{
		{
			Center: "Center1",
			A:      polarTerm{Node: "A", Comp: compTheta, Sign: 1},
			B:      polarTerm{Node: "B", Comp: compTheta, Sign: -1},
		},
		{
			Center: "Center1",
			A:      polarTerm{Node: "A", Comp: compPhi, Sign: 1},
			B:      polarTerm{Node: "B", Comp: compPhi, Sign: 1},
		},
	}
	if err := writeScenePolarEqs(scenePath, eqs); err != nil {
		t.Fatalf("writeScenePolarEqs: %v", err)
	}

	got, ok := loadScenePolarEqs(scenePath)
	if !ok {
		t.Fatalf("loadScenePolarEqs: found=false, want true")
	}
	if len(got) != len(eqs) {
		t.Fatalf("loadScenePolarEqs len=%d want %d (%+v)", len(got), len(eqs), got)
	}
	for i, want := range eqs {
		if got[i] != want {
			t.Fatalf("polarEqs[%d]=%+v want %+v", i, got[i], want)
		}
	}

	// The sibling overlay field must be untouched by the polarEqs write.
	ov, found := loadSceneOverlays(scenePath)
	if !found || ov.sceneToriVisible {
		t.Fatalf("sibling overlay field clobbered: found=%v sceneToriVisible=%v", found, ov.sceneToriVisible)
	}

	// Full MoveDispatch.LoadPolarEqs path.
	md := &MoveDispatch{}
	md.LoadPolarEqs(root)
	if len(md.polarEqs) != len(eqs) {
		t.Fatalf("md.polarEqs len=%d want %d", len(md.polarEqs), len(eqs))
	}
}

// TestPolarEqsClearedOnEmpty guards that writing an empty polarEqs list removes the
// "polarLocks" key rather than leaving a stale array behind.
func TestPolarEqsClearedOnEmpty(t *testing.T) {
	root := writeTree(t)
	scenePath := sceneCameraPath(root)
	eqs := []polarEq{{Center: "C", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: -1}}}
	if err := writeScenePolarEqs(scenePath, eqs); err != nil {
		t.Fatalf("writeScenePolarEqs: %v", err)
	}
	if err := writeScenePolarEqs(scenePath, nil); err != nil {
		t.Fatalf("writeScenePolarEqs(nil): %v", err)
	}
	if _, ok := loadScenePolarEqs(scenePath); ok {
		t.Fatalf("loadScenePolarEqs found=true after clearing, want false")
	}
}

// TestPolarEqsActiveRoundTrip verifies the Active flag survives write → read for both
// values.
func TestPolarEqsActiveRoundTrip(t *testing.T) {
	root := writeTree(t)
	scenePath := sceneCameraPath(root)
	eqs := []polarEq{
		{Center: "C", A: polarTerm{Node: "A", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "B", Comp: compTheta, Sign: -1}, Active: true},
		{Center: "C", A: polarTerm{Node: "A", Comp: compPhi, Sign: 1}, B: polarTerm{Node: "B", Comp: compPhi, Sign: 1}, Active: false},
	}
	if err := writeScenePolarEqs(scenePath, eqs); err != nil {
		t.Fatalf("writeScenePolarEqs: %v", err)
	}
	got, ok := loadScenePolarEqs(scenePath)
	if !ok || len(got) != 2 {
		t.Fatalf("loadScenePolarEqs: ok=%v got=%+v", ok, got)
	}
	if got[0].Active != true || got[1].Active != false {
		t.Fatalf("Active round-trip mismatch: got=%+v", got)
	}
}

// TestPolarEqsBackCompatDefaultActive verifies that an eq JSON object with no "active" key
// (an already-saved lock from before this field existed) defaults to Active=true on load.
func TestPolarEqsBackCompatDefaultActive(t *testing.T) {
	root := writeTree(t)
	scenePath := sceneCameraPath(root)
	// Hand-write scene.json with a polarLocks entry lacking "active".
	doc := map[string]any{
		"polarLocks": []map[string]any{
			{
				"center": "C",
				"a":      map[string]any{"node": "A", "comp": "theta", "sign": 1.0},
				"b":      map[string]any{"node": "B", "comp": "theta", "sign": -1.0},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(scenePath, raw, 0644); err != nil {
		t.Fatalf("write scene.json: %v", err)
	}
	got, ok := loadScenePolarEqs(scenePath)
	if !ok || len(got) != 1 {
		t.Fatalf("loadScenePolarEqs: ok=%v got=%+v", ok, got)
	}
	if !got[0].Active {
		t.Fatalf("back-compat default: Active=%v want true", got[0].Active)
	}
}
