package Wiring

// scene_path_safety_test.go — verifies safeTreePathComponent rejects path-traversal
// values and that the two write sinks (writeQuantOffset, writePortAnchor) reject
// unsafe ids/ports rather than escaping the tree root (see quant_offset_persist.go,
// scene_anchor_persist.go).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeTreePathComponent(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"", false},
		{".", false},
		{"..", false},
		{"../x", false},
		{"a/b", false},
		{"/abs", false},
		{`a\b`, false},
		{"n1", true},
		{"portOut", true},
		{"node_2", true},
	}
	for _, c := range cases {
		if got := safeTreePathComponent(c.s); got != c.want {
			t.Errorf("safeTreePathComponent(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestWriteQuantOffsetRejectsTraversalID(t *testing.T) {
	root := t.TempDir()
	err := writeQuantOffset(root, "../../evil", quantizedOffset{}, polar{})
	if err == nil {
		t.Fatal("expected error for traversal node id, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(root, "..", "..", "evil", "meta.json")); statErr == nil {
		t.Fatal("traversal write unexpectedly created a file outside the tree root")
	}
}

func TestWritePortAnchorRejectsTraversalNames(t *testing.T) {
	root := t.TempDir()
	if err := writePortAnchor(root, "../../evil", "port", true, 0); err == nil {
		t.Fatal("expected error for traversal node name, got nil")
	}
	if err := writePortAnchor(root, "node", "../../evil", true, 0); err == nil {
		t.Fatal("expected error for traversal port name, got nil")
	}
}
