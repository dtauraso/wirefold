package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
)

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
	path := filepath.Join(root, "view", "nodes", nodeID+".json")
	return writeJSONAtomic(path, pos)
}

// writeMetaPos sets the absolute world center on a node's meta.json, preserving its
// id/type/r. Persisting x/y/z makes a node-drag (non-rooted layout) durable across reload.
func writeMetaPos(root, nodeID string, x, y, z float64) error {
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
func writeScene(root string, raw json.RawMessage) error {
	path := filepath.Join(root, "view", "scene.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
