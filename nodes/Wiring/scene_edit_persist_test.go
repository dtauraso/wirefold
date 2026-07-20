package Wiring

// scene_edit_persist_test.go — round-trip tests for the two FSM-applied edit persisters:
// node-drag position (meta.json x/y/z) and ring-move anchor (port json anchorId). Each pins:
// an FSM edit → the debounced writer persists it to disk preserving sibling fields → a
// reload reads it back.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestLoadOverlaysEmitsDefaultsWhenNoPersistedKeys guards the regression where an empty
// scene.json (no overlay keys — everything at its default) made LoadOverlays skip emitting
// entirely, so the buffer streamed all-zero (every overlay OFF) instead of the default-visible
// state. LoadOverlays must ALWAYS stream the resolved state (file data or defaults).
func TestLoadOverlaysEmitsDefaultsWhenNoPersistedKeys(t *testing.T) {
	root := writeTree(t) // no view/scene.json → loadSceneOverlays returns found=false
	md := &MoveDispatch{ov: defaultOverlayState()}
	var kinds []string
	tr := T.NewWithSinkHook(256, io.Discard, func(e T.Event) { kinds = append(kinds, e.Kind) })
	md.LoadOverlays(root, tr)
	tr.Close() // drain the goroutine so all emitted events are recorded before asserting
	// The default-visible overlay flags must have been emitted, not skipped.
	for _, want := range []string{"scene-tori", "overlays-vis"} {
		seen := false
		for _, k := range kinds {
			if k == want {
				seen = true
				break
			}
		}
		if !seen {
			t.Fatalf("LoadOverlays emitted no %q event (emitted: %v) — an empty scene.json must still stream the default overlay state", want, kinds)
		}
	}
}

// writeTree lays down a minimal directory-tree topology (two nodes + one edge) so
// LoadTopology can build a real MoveDispatch. Positions come from meta.json scenePolar.
func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	mk("nodes/src/meta.json", `{"id":"src","type":"FanInSrc","r":100,"scenePolarR":37.4165738677,"scenePolarTheta":1.00685368543,"scenePolarPhi":1.2490457724}`)
	mk("nodes/src/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/dst/meta.json", `{"id":"dst","type":"FanInSink","r":100,"scenePolarR":87.7496438739,"scenePolarTheta":0.96453035788,"scenePolarPhi":-2.15879893034}`)
	mk("nodes/dst/inputs/In.json", `{"name":"In"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}`)
	return root
}

func loadTreeMD(t *testing.T, root string) *MoveDispatch {
	t.Helper()
	tr := T.New(0)
	_, _, md, _, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	return md
}

// TestPersistNodePositionRoundTrips: RootMove a node → flush → meta.json x/y/z updated.
func TestPersistAnchorRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	dir := vec3{X: 1, Y: 0, Z: 0}
	want := snapToRingAnchorIndex(md.NodeKind("src"), dir)
	md.applyRingAnchor("src", "Out", false, dir)
	md.persist.anchor.flush()

	spec, err := parseSpec(root)
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}
	var gotAnchor *int
	for _, n := range spec.Nodes {
		if n.ID != "src" {
			continue
		}
		for _, p := range n.Outputs {
			if p.Name == "Out" {
				gotAnchor = p.AnchorId
			}
		}
	}
	if gotAnchor == nil {
		t.Fatalf("anchorId not persisted to port file")
	}
	if *gotAnchor != want {
		t.Fatalf("reloaded anchorId=%d want %d", *gotAnchor, want)
	}
	// Sibling field (name) preserved.
	raw, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "outputs", "Out.json"))
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	if string(obj["name"]) != `"Out"` {
		t.Fatalf("name clobbered: %s", obj["name"])
	}
}

// TestPersistOverlaysRoundTrips: toggle an overlay flag → debounced flush → scene.json carries
// the (inverted) key; a fresh MoveDispatch.LoadOverlays reads it back into md.ov.
func TestPersistOverlaysRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	// Flip a visible-sense flag off (tori) and the hidden-sense flag on (labelsGlobal off).
	md.ToggleSceneTori(nil)    // sceneToriVisible: true -> false
	md.ToggleLabelsGlobal(nil) // labelsGlobalVisible: true -> false
	md.persist.overlays.schedule(md.ov)
	md.persist.overlays.flush()

	ov, found := loadSceneOverlays(sceneCameraPath(root))
	if !found {
		t.Fatalf("loadSceneOverlays found no overlay keys after flush")
	}
	if ov.sceneToriVisible {
		t.Fatalf("sceneToriVisible not persisted as hidden")
	}
	if ov.labelsGlobalVisible {
		t.Fatalf("labelsGlobalVisible not persisted as hidden")
	}
	// Untouched flag keeps its default (visible).
	if !ov.handholdsVisible {
		t.Fatalf("handholdsVisible should default visible, got hidden")
	}

	// Seed a fresh dispatch from disk and confirm md.ov is restored.
	fresh := &MoveDispatch{ov: defaultOverlayState()}
	fresh.LoadOverlays(root, nil)
	if fresh.ov.sceneToriVisible {
		t.Fatalf("LoadOverlays did not restore sceneToriVisible=false")
	}
	if fresh.ov.labelsGlobalVisible {
		t.Fatalf("LoadOverlays did not restore labelsGlobalVisible=false")
	}
}

// TestOverlaysPersistPreservesCamera: camera + overlays coexist in scene.json — the writers
// must not clobber each other (sceneFileMu).
func TestOverlaysPersistPreservesCamera(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableViewpointPersist(root)
	md.SetViewpoint(vec3{X: 1, Y: 2, Z: 3}, 200, dir{Theta: 0.5, Phi: 1.5}, dir{Theta: 0.05, Phi: 0.15})
	md.EmitViewpoint(nil)
	md.persist.vp.flush()

	md.EnableEditPersist(root)

	md.ToggleSceneTori(nil)
	md.persist.overlays.schedule(md.ov)
	md.persist.overlays.flush()

	// Camera survives.
	if _, _, _, _, ok := loadSceneViewpoint(root); !ok {
		t.Fatalf("cameraPolar clobbered by overlay write")
	}
	// Overlay landed.
	ov, found := loadSceneOverlays(sceneCameraPath(root))
	if !found || ov.sceneToriVisible {
		t.Fatalf("overlay not persisted alongside camera (found=%v ov=%+v)", found, ov)
	}
}

// TestPersistFileTopologyPathInTree pins BUG 1: the live editor passes the topology.json
// FILE inside the tree dir (not the dir itself), so os.Stat(topologyPath).IsDir() is false.
// EnableEditPersist must still resolve the tree root to the file's PARENT dir (which contains
// nodes/), or posPersist/anchorPersist no-op and node-drag / ring-move never reach disk.
// This exercises the full EnableEditPersist wiring with a FILE topologyPath + a real tree.
func TestEnableEditPersistTrueMonolithicNoTree(t *testing.T) {
	dir := t.TempDir()
	topoFile := filepath.Join(dir, "topology.json")
	if err := os.WriteFile(topoFile, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	md := &MoveDispatch{ov: defaultOverlayState()}
	md.EnableEditPersist(topoFile)
	if md.persist.pos.root != "" {
		t.Fatalf("posPersist.root=%q want empty (no nodes/ subdir → true monolithic)", md.persist.pos.root)
	}
	if md.persist.anchor.root != "" {
		t.Fatalf("anchorPersist.root=%q want empty", md.persist.anchor.root)
	}
}

// TestOverlaysPersistMonolithicForm: overlays persist correctly when topologyPath is a
// monolithic file (not a directory), the form that caused the original treeRoot="" no-op bug.
// sceneCameraPath resolves to the sibling view/scene.json; EnableEditPersist + LoadOverlays
// must both land on that same path.
func TestOverlaysPersistMonolithicForm(t *testing.T) {
	// Build a tmp directory that looks like a monolithic topology: the "topology file"
	// is a file inside the dir; view/scene.json is a sibling of that file.
	dir := t.TempDir()
	topoFile := dir + "/topology.json"
	if err := os.WriteFile(topoFile, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	md := &MoveDispatch{ov: defaultOverlayState()}
	md.EnableEditPersist(topoFile) // topologyPath is a FILE, not a dir

	// overlaysPersist.path must be non-empty (the sceneCameraPath sibling).
	if md.persist.overlays.path == "" {
		t.Fatal("overlaysPersist.path is empty for monolithic topologyPath — EnableEditPersist bug")
	}

	// Toggle an overlay and flush.
	md.ToggleSceneTori(nil) // sceneToriVisible: true -> false
	md.persist.overlays.schedule(md.ov)
	md.persist.overlays.flush()

	// Load back via sceneCameraPath.
	ov, found := loadSceneOverlays(sceneCameraPath(topoFile))
	if !found {
		t.Fatal("loadSceneOverlays found no overlay keys after flush on monolithic form")
	}
	if ov.sceneToriVisible {
		t.Fatal("sceneToriVisible not persisted on monolithic form")
	}

	// LoadOverlays must restore into a fresh dispatch.
	fresh := &MoveDispatch{ov: defaultOverlayState()}
	fresh.LoadOverlays(topoFile, nil)
	if fresh.ov.sceneToriVisible {
		t.Fatal("LoadOverlays did not restore sceneToriVisible=false on monolithic form")
	}
}
