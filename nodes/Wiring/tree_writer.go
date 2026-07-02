package Wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// safeSegment reports whether s is safe to use as a single path segment (a node id
// or port name) in a filesystem path built under the topology tree. node ids and
// ports arrive over stdin from TS; filepath.Join cleans lexically but does NOT
// prevent traversal (e.g. "../../evil" escapes the tree), so any segment that is
// empty, contains a path separator, or contains ".." is rejected before a write
// path is constructed.
func safeSegment(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, `/\`) || strings.ContainsRune(s, filepath.Separator) {
		return false
	}
	if s == ".." || strings.Contains(s, "..") {
		return false
	}
	return true
}

// writeJSONAtomic marshals v compactly, writes to path+".tmp", then renames to path.
// Output is single-line, no trailing newline, matching the fixture style.
// Creates parent directories as needed.
func writeJSONAtomic(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeViewNode(root, nodeID string, pos specPosition) error {
	if !safeSegment(nodeID) {
		return fmt.Errorf("writeViewNode: unsafe node id %q", nodeID)
	}
	path := filepath.Join(root, "view", "nodes", nodeID+".json")
	return writeJSONAtomic(path, pos)
}

// writeMetaPos sets the absolute world center on a node's meta.json, preserving its
// id/type/r. Persisting x/y/z makes a node-drag (polar layout) durable across reload.
func writeMetaPos(root, nodeID string, x, y, z float64) error {
	if !safeSegment(nodeID) {
		return fmt.Errorf("writeMetaPos: unsafe node id %q", nodeID)
	}
	path := filepath.Join(root, "nodes", nodeID, "meta.json")
	var meta jsonMeta
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	meta.ID = nodeID
	meta.X = x
	meta.Y = y
	meta.Z = z
	return writeJSONAtomic(path, meta)
}

func writePort(root, nodeID, port string, isInput bool, p specPort) error {
	if !safeSegment(nodeID) {
		return fmt.Errorf("writePort: unsafe node id %q", nodeID)
	}
	if !safeSegment(port) {
		return fmt.Errorf("writePort: unsafe port %q", port)
	}
	side := "inputs"
	if !isInput {
		side = "outputs"
	}
	path := filepath.Join(root, "nodes", nodeID, side, port+".json")
	return writeJSONAtomic(path, p)
}

func writeFades(root string, fades map[string]bool) error {
	path := filepath.Join(root, "view", "fades.json")
	return writeJSONAtomic(path, fades)
}

// writeScene writes raw scene JSON to <root>/view/scene.json atomically.
// The raw bytes are written verbatim (no re-marshal) so the scene's field order is preserved.
//
// Go owns cameraPolar (scene_camera_persist.go), so this write must NOT clobber the
// Go-persisted camera with the stale value a TS scene-save (fades/overlays) carries. We
// merge: the on-disk cameraPolar is preserved and all other fields come from the TS blob.
// Serialized against the camera persister via sceneFileMu.
func writeScene(root string, raw json.RawMessage) error {
	path := filepath.Join(root, "view", "scene.json")
	sceneFileMu.Lock()
	defer sceneFileMu.Unlock()
	out := preserveSceneCameraPolar(path, raw)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// preserveSceneCameraPolar returns the incoming scene blob with its cameraPolar replaced by
// the value currently on disk (the Go-owned camera). If there is no on-disk cameraPolar, or
// either blob cannot be parsed as an object, the incoming blob is returned unchanged.
func preserveSceneCameraPolar(path string, incoming json.RawMessage) json.RawMessage {
	diskRaw, err := os.ReadFile(path)
	if err != nil {
		return incoming
	}
	var disk map[string]json.RawMessage
	if json.Unmarshal(diskRaw, &disk) != nil {
		return incoming
	}
	cam, ok := disk["cameraPolar"]
	if !ok {
		return incoming
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(incoming, &in) != nil {
		return incoming
	}
	in["cameraPolar"] = cam
	merged, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	return merged
}

func mergeFades(root string, delta map[string]bool) error {
	path := filepath.Join(root, "view", "fades.json")
	current := map[string]bool{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &current)
	}
	for k, v := range delta {
		current[k] = v
	}
	return writeFades(root, current)
}
