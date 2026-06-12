package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// writeJSONAtomic marshals v with indent, writes to path+".tmp", then renames to path.
// Creates parent directories as needed.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
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
