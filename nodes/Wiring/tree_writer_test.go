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
	// view.nodes is kept as auxiliary view data; assert the written position
	// round-trips through view.nodes (node centers no longer derive from it).
	got, ok := spec2.View.Nodes[nodeID]
	if !ok {
		t.Fatalf("node %s missing from view.nodes after write", nodeID)
	}
	if got.X != want.X || got.Y != want.Y || got.Z != want.Z {
		t.Errorf("position mismatch: got %+v want %+v", got, want)
	}
}

func TestWritePortAnchorRoundTrip(t *testing.T) {
	root := copyFixtureTree(t)
	spec, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}
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
	anchorId := 3
	p := specPort{Name: portName, AnchorId: &anchorId}
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
					if inp.AnchorId == nil {
						t.Fatal("anchorId is nil after write")
					}
					if *inp.AnchorId != anchorId {
						t.Errorf("anchorId mismatch: got %d want %d", *inp.AnchorId, anchorId)
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
