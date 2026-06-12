package Wiring

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func copyFixtureTree(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	fixtureRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "topology")
	tmpDir := t.TempDir()
	src := os.DirFS(fixtureRoot)
	if err := os.CopyFS(tmpDir, src); err != nil {
		t.Fatalf("copyFixtureTree: %v", err)
	}
	return tmpDir
}

func TestWriteViewNodeRoundTrip(t *testing.T) {
	root := copyFixtureTree(t)
	// Pick the first node from the loaded topology
	spec, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}
	if len(spec.Nodes) == 0 {
		t.Skip("no nodes in fixture")
	}
	nodeID := spec.Nodes[0].ID
	want := specPosition{X: 99, Y: 88, Z: 77}
	if err := writeViewNode(root, nodeID, want); err != nil {
		t.Fatalf("writeViewNode: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "view", "nodes", nodeID+".json"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if bytes.ContainsRune(raw, '\n') {
		t.Errorf("written file is not compact (contains newline): %q", raw)
	}
	spec2, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree after write: %v", err)
	}
	for _, n := range spec2.Nodes {
		if n.ID == nodeID {
			if n.Position.X != want.X || n.Position.Y != want.Y || n.Position.Z != want.Z {
				t.Errorf("position mismatch: got %+v want %+v", n.Position, want)
			}
			return
		}
	}
	t.Errorf("node %s not found after write", nodeID)
}

func TestWritePortAnchorRoundTrip(t *testing.T) {
	root := copyFixtureTree(t)
	spec, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}
	// Find a node with at least one input
	var nodeID, portName string
	for _, n := range spec.Nodes {
		if len(n.Inputs) > 0 {
			nodeID = n.ID
			portName = n.Inputs[0].Name
			break
		}
	}
	if nodeID == "" {
		t.Skip("no node with inputs in fixture")
	}
	anchor := &specVec3{X: 1.5, Y: 2.5, Z: 0}
	p := specPort{Name: portName, Anchor: anchor}
	if err := writePort(root, nodeID, portName, true, p); err != nil {
		t.Fatalf("writePort: %v", err)
	}
	spec2, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree after write: %v", err)
	}
	for _, n := range spec2.Nodes {
		if n.ID == nodeID {
			for _, inp := range n.Inputs {
				if inp.Name == portName {
					if inp.Anchor == nil {
						t.Fatal("anchor is nil after write")
					}
					if inp.Anchor.X != anchor.X || inp.Anchor.Y != anchor.Y {
						t.Errorf("anchor mismatch: got %+v want %+v", inp.Anchor, anchor)
					}
					return
				}
			}
		}
	}
	t.Errorf("port %s on node %s not found", portName, nodeID)
}

func TestWriteFadesRoundTrip(t *testing.T) {
	root := t.TempDir()
	fades := map[string]bool{"edge-a->b": true, "edge-b->c": false}
	if err := writeFades(root, fades); err != nil {
		t.Fatalf("writeFades: %v", err)
	}
	got := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, "view", "fades.json"))
	if err != nil {
		t.Fatalf("read fades.json: %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k, v := range fades {
		if got[k] != v {
			t.Errorf("fades[%q] = %v, want %v", k, got[k], v)
		}
	}
}
