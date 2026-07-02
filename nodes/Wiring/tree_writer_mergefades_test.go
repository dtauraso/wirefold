package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// tree_writer_mergefades_test.go — mergeFades reads the existing view/fades.json, overlays
// the delta (adding new keys, overwriting existing ones), and writes the union back. Keys
// not in the delta are preserved.

func readFades(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "view", "fades.json"))
	if err != nil {
		t.Fatalf("read fades.json: %v", err)
	}
	m := map[string]bool{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal fades.json: %v", err)
	}
	return m
}

func TestMergeFadesOverlaysAndPreserves(t *testing.T) {
	root := t.TempDir()

	// Seed an initial fades set.
	if err := writeFades(root, map[string]bool{"e1": true, "e2": false}); err != nil {
		t.Fatalf("seed writeFades: %v", err)
	}

	// Merge a delta: add e3, flip e2, leave e1 untouched.
	if err := mergeFades(root, map[string]bool{"e2": true, "e3": true}); err != nil {
		t.Fatalf("mergeFades: %v", err)
	}

	got := readFades(t, root)
	want := map[string]bool{"e1": true, "e2": true, "e3": true}
	if len(got) != len(want) {
		t.Fatalf("merged set size = %d, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("merged[%q] = %v, want %v (full: %v)", k, got[k], v, got)
		}
	}
}

// TestMergeFadesFromEmpty: with no existing fades.json, the delta becomes the whole set.
func TestMergeFadesFromEmpty(t *testing.T) {
	root := t.TempDir()
	if err := mergeFades(root, map[string]bool{"x": true}); err != nil {
		t.Fatalf("mergeFades on empty root: %v", err)
	}
	got := readFades(t, root)
	if len(got) != 1 || !got["x"] {
		t.Fatalf("merged from empty = %v, want {x:true}", got)
	}
}
