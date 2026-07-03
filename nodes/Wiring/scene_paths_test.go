package Wiring

// scene_paths_test.go — guards the single-source-of-truth path resolution contract.
//
// The core invariant: for a given tree, BOTH the directory-form and the file-form
// topologyPath must resolve to the SAME sceneTreeRoot and the SAME sceneJSONPath —
// and ALL FIVE persisters armed via EnableEditPersist / EnableViewpointPersist must
// target that location. This test is the guard that would have caught the original
// three-recurrence bug (file-form topologyPath → node-pos/anchor persister root "").

import (
	"os"
	"path/filepath"
	"testing"
)

// buildDualFormFixture creates a minimal tree fixture that has a nodes/ subdir
// (so the file-form resolves to the parent, not "").
// Returns (treeRoot, topoFile) where topoFile is a file inside treeRoot.
func buildDualFormFixture(t *testing.T) (treeRoot, topoFile string) {
	t.Helper()
	root := t.TempDir()
	// Minimal nodes/ subdir so sceneTreeRoot recognises the parent.
	if err := os.MkdirAll(filepath.Join(root, "nodes"), 0o755); err != nil {
		t.Fatalf("mkdir nodes: %v", err)
	}
	topoFile = filepath.Join(root, "topology.json")
	if err := os.WriteFile(topoFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write topology.json: %v", err)
	}
	return root, topoFile
}

// TestSceneTreeRootBothForms asserts that for the same underlying tree, both the
// directory form and the file form of topologyPath yield the same sceneTreeRoot.
func TestSceneTreeRootBothForms(t *testing.T) {
	root, topoFile := buildDualFormFixture(t)

	gotDir := sceneTreeRoot(root)
	gotFile := sceneTreeRoot(topoFile)

	if gotDir == "" {
		t.Fatalf("sceneTreeRoot(%q) = %q, want non-empty (dir form)", root, gotDir)
	}
	if gotFile == "" {
		t.Fatalf("sceneTreeRoot(%q) = %q, want non-empty (file form)", topoFile, gotFile)
	}
	if gotDir != gotFile {
		t.Fatalf("sceneTreeRoot diverges: dir form=%q file form=%q (must be identical)", gotDir, gotFile)
	}
}

// TestSceneJSONPathBothForms asserts that both forms of topologyPath resolve to
// the SAME view/scene.json path.
func TestSceneJSONPathBothForms(t *testing.T) {
	root, topoFile := buildDualFormFixture(t)

	wantPath := filepath.Join(root, "view", "scene.json")

	gotDir := sceneJSONPath(root)
	gotFile := sceneJSONPath(topoFile)

	if gotDir != wantPath {
		t.Fatalf("sceneJSONPath(%q) = %q, want %q (dir form)", root, gotDir, wantPath)
	}
	if gotFile != wantPath {
		t.Fatalf("sceneJSONPath(%q) = %q, want %q (file form)", topoFile, gotFile, wantPath)
	}
}

// TestAllFivePersistersConsistentBothForms arms all five persisters via the public
// API (EnableEditPersist + EnableViewpointPersist) for BOTH the directory form and
// the file form of topologyPath, and asserts each persister's stored root/path is
// non-empty and resolves to the same concrete location in both cases.
//
// This is the test that would have caught the original bug: the file-form
// topologyPath left node-pos / anchor persister root == "" (no-op) while camera
// + fade + overlays worked correctly.
func TestAllFivePersistersConsistentBothForms(t *testing.T) {
	wantSceneJSON := func(root string) string {
		return filepath.Join(root, "view", "scene.json")
	}

	run := func(t *testing.T, label, topologyPath, expectedRoot string) {
		t.Helper()
		md := &MoveDispatch{
			nodeMovers:         map[string]*nodeMover{},
			ov:                 defaultOverlayState(),
			directlyFadedNodes: map[string]bool{},
			directlyFadedEdges: map[string]bool{},
		}
		md.EnableViewpointPersist(topologyPath)
		md.EnableEditPersist(topologyPath)

		want := wantSceneJSON(expectedRoot)

		// 1. viewpoint persister (camera)
		if md.vpPersist == nil {
			t.Fatalf("[%s] vpPersist nil", label)
		}
		if md.vpPersist.path != want {
			t.Fatalf("[%s] vpPersist.path=%q want %q", label, md.vpPersist.path, want)
		}

		// 2. fade persister
		if md.fadePersist == nil {
			t.Fatalf("[%s] fadePersist nil", label)
		}
		if md.fadePersist.path != want {
			t.Fatalf("[%s] fadePersist.path=%q want %q", label, md.fadePersist.path, want)
		}

		// 3. overlays persister
		if md.overlaysPersist == nil {
			t.Fatalf("[%s] overlaysPersist nil", label)
		}
		if md.overlaysPersist.path != want {
			t.Fatalf("[%s] overlaysPersist.path=%q want %q", label, md.overlaysPersist.path, want)
		}

		// 4. node-pos persister (root, not path)
		if md.posPersist == nil {
			t.Fatalf("[%s] posPersist nil", label)
		}
		if md.posPersist.root != expectedRoot {
			t.Fatalf("[%s] posPersist.root=%q want %q", label, md.posPersist.root, expectedRoot)
		}

		// 5. anchor persister (root, not path)
		if md.anchorPersist == nil {
			t.Fatalf("[%s] anchorPersist nil", label)
		}
		if md.anchorPersist.root != expectedRoot {
			t.Fatalf("[%s] anchorPersist.root=%q want %q", label, md.anchorPersist.root, expectedRoot)
		}
	}

	root, topoFile := buildDualFormFixture(t)
	run(t, "dir-form", root, root)
	run(t, "file-form", topoFile, root)

	// Cross-check: the dir-form and file-form must agree on both the root and the scene path.
	{
		mdDir := &MoveDispatch{nodeMovers: map[string]*nodeMover{}, ov: defaultOverlayState(), directlyFadedNodes: map[string]bool{}, directlyFadedEdges: map[string]bool{}}
		mdDir.EnableViewpointPersist(root)
		mdDir.EnableEditPersist(root)

		mdFile := &MoveDispatch{nodeMovers: map[string]*nodeMover{}, ov: defaultOverlayState(), directlyFadedNodes: map[string]bool{}, directlyFadedEdges: map[string]bool{}}
		mdFile.EnableViewpointPersist(topoFile)
		mdFile.EnableEditPersist(topoFile)

		if mdDir.vpPersist.path != mdFile.vpPersist.path {
			t.Fatalf("vpPersist.path diverges: dir=%q file=%q", mdDir.vpPersist.path, mdFile.vpPersist.path)
		}
		if mdDir.fadePersist.path != mdFile.fadePersist.path {
			t.Fatalf("fadePersist.path diverges: dir=%q file=%q", mdDir.fadePersist.path, mdFile.fadePersist.path)
		}
		if mdDir.overlaysPersist.path != mdFile.overlaysPersist.path {
			t.Fatalf("overlaysPersist.path diverges: dir=%q file=%q", mdDir.overlaysPersist.path, mdFile.overlaysPersist.path)
		}
		if mdDir.posPersist.root != mdFile.posPersist.root {
			t.Fatalf("posPersist.root diverges: dir=%q file=%q", mdDir.posPersist.root, mdFile.posPersist.root)
		}
		if mdDir.anchorPersist.root != mdFile.anchorPersist.root {
			t.Fatalf("anchorPersist.root diverges: dir=%q file=%q", mdDir.anchorPersist.root, mdFile.anchorPersist.root)
		}
	}
}
