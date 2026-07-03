package Wiring

// scene_locks_persist_test.go — round-trip test for the polar rule-builder equation
// persister (scene_locks_persist.go): write → read back preserving sibling scene.json
// fields (mirrors scene_edit_persist_test.go's pattern; writeTree lives there).

import "testing"

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
