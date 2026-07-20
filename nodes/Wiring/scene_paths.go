package Wiring

// scene_paths.go — ONE shared source of truth for topology-path resolution.
//
// Four persisters (camera, overlays, node-pos, anchor) and two loaders
// (loadSceneViewpoint, loadSceneOverlays) all need to resolve a
// topologyPath — which may be EITHER the directory form (a tree root containing
// nodes/ and view/) OR the file form (a topology.json FILE inside that tree) — to
// one of two derived locations:
//
//   - sceneTreeRoot: the directory that CONTAINS nodes/ and view/.
//   - sceneJSONPath: the view/scene.json file within that tree.
//
// Centralising the logic here makes the bug class "two forms diverge" unrepresentable:
// every persister and loader calls these helpers; no file hand-rolls os.Stat/IsDir.
//
// Guard: tools/check-scene-path-resolution.sh rejects any os.Stat+IsDir that appears
// in nodes/Wiring/*.go outside this file — a new persister cannot hand-roll resolution.
// Mark genuinely-unrelated IsDir calls with a trailing `// path-resolution-ok:` comment
// to exempt them from the guard.

import (
	"os"
	"path/filepath"
)

// sceneTreeRoot returns the directory that CONTAINS the tree's nodes/ and view/
// subdirs. Both forms of topologyPath resolve to the SAME root:
//
//   - Directory form: topologyPath IS the tree root.
//   - File form:      topologyPath is a file (e.g. topology.json) INSIDE the tree;
//     the root is filepath.Dir(topologyPath), but ONLY when a nodes/ or view/ subdir
//     exists there (to distinguish a true monolithic file with no tree from the
//     file-inside-tree case).
//   - True monolithic: no tree → returns "" (pos/anchor persisters no-op).
func sceneTreeRoot(topologyPath string) string {
	info, err := os.Stat(topologyPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return topologyPath
	}
	// File form: check whether parent has a tree.
	parent := filepath.Dir(topologyPath)
	for _, sub := range []string{"nodes", "view"} {
		if si, err := os.Stat(filepath.Join(parent, sub)); err == nil && si.IsDir() {
			return parent
		}
	}
	return ""
}

// sceneJSONPath returns the path to the scene sidecar file (view/scene.json) for the
// given topologyPath. For both forms it is <sceneTreeRoot>/view/scene.json. When
// sceneTreeRoot returns "" (true monolithic with no tree) it falls back to
// filepath.Dir(topologyPath)/view/scene.json — consistent with the original
// sceneCameraPath behaviour for the rare case where the editor writes a monolithic file
// with no nodes/ sibling, ensuring the camera still persists.
func sceneJSONPath(topologyPath string) string {
	root := sceneTreeRoot(topologyPath)
	if root == "" {
		// Fall back: resolve relative to the file's parent (or the path itself if dir).
		base := topologyPath
		if info, err := os.Stat(topologyPath); err == nil && !info.IsDir() {
			base = filepath.Dir(topologyPath)
		}
		return filepath.Join(base, "view", "scene.json")
	}
	return filepath.Join(root, "view", "scene.json")
}

// sceneCameraPath is a backwards-compatibility alias for sceneJSONPath.
// All call sites have been migrated to sceneJSONPath; this alias is kept so
// that any lingering direct call (e.g. in tests) still compiles and behaves
// identically. Do not add new call sites — use sceneJSONPath.
func sceneCameraPath(topologyPath string) string {
	return sceneJSONPath(topologyPath)
}

// sceneViewFilePath resolves <sceneTreeRoot>/view/<name> for topologyPath, using the same
// root-resolution (and true-monolithic fallback) as sceneJSONPath. Backs the one-file-per-
// writer split (docs/planning/visual-editor/one-file-per-goroutine.md): camera.json,
// overlays.json and sphere.json each replace one of the three writers that used to share
// scene.json, and each resolves its path through this one shared helper.
func sceneViewFilePath(topologyPath, name string) string {
	root := sceneTreeRoot(topologyPath)
	if root == "" {
		base := topologyPath
		if info, err := os.Stat(topologyPath); err == nil && !info.IsDir() {
			base = filepath.Dir(topologyPath)
		}
		return filepath.Join(base, "view", name)
	}
	return filepath.Join(root, "view", name)
}

// cameraFilePath is the WRITE-side location of the persisted camera pose — the sole
// successor to scene.json's cameraPolar key. writeSceneCameraPolar is its only writer.
func cameraFilePath(topologyPath string) string {
	return sceneViewFilePath(topologyPath, "camera.json")
}

// overlaysFilePath is the WRITE-side location of the persisted overlay-visibility flags —
// the sole successor to scene.json's overlay keys. writeSceneOverlays is its only writer.
func overlaysFilePath(topologyPath string) string {
	return sceneViewFilePath(topologyPath, "overlays.json")
}

// sphereFilePath is the WRITE-side location of the persisted scene sphere — the sole
// successor to scene.json's sceneSphere key. writeSceneSphere is its only writer.
func sphereFilePath(topologyPath string) string {
	return sceneViewFilePath(topologyPath, "sphere.json")
}
