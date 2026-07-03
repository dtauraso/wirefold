package Wiring

// scene_edit_persist_test.go — round-trip tests for the three FSM-applied edit persisters:
// node-drag position (meta.json x/y/z), ring-move anchor (port json anchorId), and fade
// (scene.json fadedNodes/fadedEdges). Each pins: an FSM edit → the debounced writer persists
// it to disk preserving sibling fields → a reload reads it back.

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// writeTree lays down a minimal directory-tree topology (two nodes + one edge) so
// LoadTopology can build a real MoveDispatch. Positions come from meta.json x/y/z.
func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mk("nodes/src/meta.json", `{"id":"src","type":"FanInSrc","r":100,"x":10,"y":20,"z":30}`)
	mk("nodes/src/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/dst/meta.json", `{"id":"dst","type":"FanInSink","r":100,"x":-40,"y":50,"z":-60}`)
	mk("nodes/dst/inputs/In.json", `{"name":"In"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}`)
	mk("view/nodes/src.json", `{"x":10,"y":20,"z":30}`)
	mk("view/nodes/dst.json", `{"x":-40,"y":50,"z":-60}`)
	return root
}

func loadTreeMD(t *testing.T, root string) *MoveDispatch {
	t.Helper()
	tr := T.New(0)
	_, _, md, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	return md
}

// TestPersistNodePositionRoundTrips: RootMove a node → flush → meta.json x/y/z updated.
func TestPersistNodePositionRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	target := vec3{X: 111, Y: 222, Z: 333}
	if !md.RootMove("src", target) {
		t.Fatalf("RootMove returned false")
	}
	md.posPersist.flush()

	spec, err := parseSpec(root)
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}
	var got *specNode
	for i := range spec.Nodes {
		if spec.Nodes[i].ID == "src" {
			got = &spec.Nodes[i]
		}
	}
	if got == nil {
		t.Fatalf("node src not found after reload")
	}
	if math.Abs(got.X-target.X) > 1e-9 || math.Abs(got.Y-target.Y) > 1e-9 || math.Abs(got.Z-target.Z) > 1e-9 {
		t.Fatalf("reloaded pos=(%v,%v,%v) want (%v,%v,%v)", got.X, got.Y, got.Z, target.X, target.Y, target.Z)
	}
	// Sibling meta fields preserved.
	raw, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("meta.json invalid: %v", err)
	}
	if string(obj["type"]) != `"FanInSrc"` {
		t.Fatalf("type clobbered: %s", obj["type"])
	}
	if string(obj["r"]) != "100" {
		t.Fatalf("r clobbered: %s", obj["r"])
	}
}

// TestPersistAnchorRoundTrips: applyRingAnchor → flush → the port file's anchorId matches
// the snapped index and reloads through loadTree.
func TestPersistAnchorRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	dir := vec3{X: 1, Y: 0, Z: 0}
	want := snapToRingAnchorIndex(md.NodeKind("src"), dir)
	md.applyRingAnchor("src", "Out", false, dir)
	md.anchorPersist.flush()

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

// TestPersistFadeRoundTrips: select a node, toggle fade, flush → scene.json carries the seed;
// a fresh MoveDispatch.SeedFade reads it back into the directly-faded set.
func TestPersistFadeRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	md.selected = "dst"
	md.ToggleFadeSelection(nil)
	md.fadePersist.flush()

	nodes, edges := loadSceneFade(root)
	if len(edges) != 0 {
		t.Fatalf("edges=%v want none", edges)
	}
	if len(nodes) != 1 || nodes[0] != "dst" {
		t.Fatalf("fadedNodes=%v want [dst]", nodes)
	}
	// Seed a fresh dispatch from disk and confirm the set is restored.
	fresh := &MoveDispatch{}
	fresh.SeedFade(root, nil)
	if !fresh.directlyFadedNodes["dst"] {
		t.Fatalf("SeedFade did not restore dst")
	}
}

// TestFadePersistPreservesCameraPolar: a camera write then a fade write both survive in
// scene.json (the three scene.json writers coexist via sceneFileMu).
func TestFadePersistPreservesCameraPolar(t *testing.T) {
	root := writeTree(t)
	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.EnableViewpointPersist(root)
	md.SetViewpoint(vec3{X: 1, Y: 2, Z: 3}, 200, dir{Theta: 0.5, Phi: 1.5}, dir{Theta: 0.05, Phi: 0.15})
	md.EmitViewpoint(nil)
	md.vpPersist.flush()

	md.EnableEditPersist(root)
	md.fadePersist.schedule([]string{"n1"}, []string{"e0"})
	md.fadePersist.flush()

	// Camera survives.
	if _, _, _, _, ok := loadSceneViewpoint(root); !ok {
		t.Fatalf("cameraPolar clobbered by fade write")
	}
	// Fade landed.
	nodes, edges := loadSceneFade(root)
	if len(nodes) != 1 || nodes[0] != "n1" || len(edges) != 1 || edges[0] != "e0" {
		t.Fatalf("fade seeds=%v/%v want [n1]/[e0]", nodes, edges)
	}
}

// TestPersistOverlaysRoundTrips: toggle an overlay flag → debounced flush → scene.json carries
// the (inverted) key; a fresh MoveDispatch.SeedOverlays reads it back into md.ov.
func TestPersistOverlaysRoundTrips(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	// Flip a visible-sense flag off (tori) and the hidden-sense flag on (labelsGlobal off).
	md.ToggleSceneTori(nil)    // sceneToriVisible: true -> false
	md.ToggleLabelsGlobal(nil) // labelsGlobalVisible: true -> false
	md.overlaysPersist.schedule(md.ov)
	md.overlaysPersist.flush()

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
	fresh.SeedOverlays(root, nil)
	if fresh.ov.sceneToriVisible {
		t.Fatalf("SeedOverlays did not restore sceneToriVisible=false")
	}
	if fresh.ov.labelsGlobalVisible {
		t.Fatalf("SeedOverlays did not restore labelsGlobalVisible=false")
	}
}

// TestOverlaysPersistPreservesCameraAndFade: camera + fade + overlays all coexist in
// scene.json — the three scene.json writers must not clobber each other (sceneFileMu).
func TestOverlaysPersistPreservesCameraAndFade(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableViewpointPersist(root)
	md.SetViewpoint(vec3{X: 1, Y: 2, Z: 3}, 200, dir{Theta: 0.5, Phi: 1.5}, dir{Theta: 0.05, Phi: 0.15})
	md.EmitViewpoint(nil)
	md.vpPersist.flush()

	md.EnableEditPersist(root)
	md.fadePersist.schedule([]string{"src"}, []string{"e0"})
	md.fadePersist.flush()

	md.ToggleSceneTori(nil)
	md.overlaysPersist.schedule(md.ov)
	md.overlaysPersist.flush()

	// Camera survives.
	if _, _, _, _, ok := loadSceneViewpoint(root); !ok {
		t.Fatalf("cameraPolar clobbered by overlay write")
	}
	// Fade survives.
	nodes, edges := loadSceneFade(root)
	if len(nodes) != 1 || nodes[0] != "src" || len(edges) != 1 || edges[0] != "e0" {
		t.Fatalf("fade seeds clobbered: %v/%v", nodes, edges)
	}
	// Overlay landed.
	ov, found := loadSceneOverlays(sceneCameraPath(root))
	if !found || ov.sceneToriVisible {
		t.Fatalf("overlay not persisted alongside camera+fade (found=%v ov=%+v)", found, ov)
	}
}

// TestOverlaysPersistMonolithicForm: overlays persist correctly when topologyPath is a
// monolithic file (not a directory), the form that caused the original treeRoot="" no-op bug.
// sceneCameraPath resolves to the sibling view/scene.json; EnableEditPersist + SeedOverlays
// must both land on that same path.
func TestOverlaysPersistMonolithicForm(t *testing.T) {
	// Build a tmp directory that looks like a monolithic topology: the "topology file"
	// is a file inside the dir; view/scene.json is a sibling of that file.
	dir := t.TempDir()
	topoFile := dir + "/topology.json"
	if err := os.WriteFile(topoFile, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	md := &MoveDispatch{ov: defaultOverlayState(), directlyFadedNodes: map[string]bool{}, directlyFadedEdges: map[string]bool{}}
	md.EnableEditPersist(topoFile) // topologyPath is a FILE, not a dir

	// overlaysPersist.path must be non-empty (the sceneCameraPath sibling).
	if md.overlaysPersist.path == "" {
		t.Fatal("overlaysPersist.path is empty for monolithic topologyPath — EnableEditPersist bug")
	}

	// Toggle an overlay and flush.
	md.ToggleSceneTori(nil) // sceneToriVisible: true -> false
	md.overlaysPersist.schedule(md.ov)
	md.overlaysPersist.flush()

	// Load back via sceneCameraPath.
	ov, found := loadSceneOverlays(sceneCameraPath(topoFile))
	if !found {
		t.Fatal("loadSceneOverlays found no overlay keys after flush on monolithic form")
	}
	if ov.sceneToriVisible {
		t.Fatal("sceneToriVisible not persisted on monolithic form")
	}

	// SeedOverlays must restore into a fresh dispatch.
	fresh := &MoveDispatch{ov: defaultOverlayState()}
	fresh.SeedOverlays(topoFile, nil)
	if fresh.ov.sceneToriVisible {
		t.Fatal("SeedOverlays did not restore sceneToriVisible=false on monolithic form")
	}
}
