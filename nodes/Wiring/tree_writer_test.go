package Wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
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

// TestWriteRejectsPathTraversal feeds a node id / port containing "../" to the
// tree writers and asserts nothing is written outside the tree and the writers
// return an error (the segment is rejected before any path is constructed).
func TestWriteRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	escape := filepath.Join(root, "..", "evil.json") // where a "../evil" id would land

	if err := writeViewNode(root, "../evil", specPosition{X: 1}); err == nil {
		t.Error("writeViewNode accepted a traversal node id")
	}
	if err := writeMetaPos(root, "../../evil", 1, 2, 3); err == nil {
		t.Error("writeMetaPos accepted a traversal node id")
	}
	if err := writePort(root, "../evil", "in", true, specPort{}); err == nil {
		t.Error("writePort accepted a traversal node id")
	}
	if err := writePort(root, "n1", "../evil", false, specPort{}); err == nil {
		t.Error("writePort accepted a traversal port")
	}

	if _, err := os.Stat(escape); err == nil {
		t.Fatalf("a file escaped the tree at %s", escape)
	}
	// A legitimate segment still writes.
	if err := writeViewNode(root, "n1", specPosition{X: 1}); err != nil {
		t.Fatalf("writeViewNode rejected a legit node id: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "view", "nodes", "n1.json")); err != nil {
		t.Fatalf("legit write missing: %v", err)
	}
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

// TestDragWritesBothPositionStores locks that a node-drag keeps the two on-disk
// position stores consistent: meta.json (canonical for Go node geometry) and
// view/nodes/<id>.json (the auxiliary store the TS spec-emit reads). They diverged
// when applyUpdate wrote only meta.json; this asserts both reflect the same center.
func TestDragWritesBothPositionStores(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"dst","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "src": {"x": 100, "y": 0, "z": 0},
	    "dst": {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	root := t.TempDir()
	path := filepath.Join(root, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)

	// A node-drag, delivered through the same applyUpdate path the live bridge uses,
	// with treeRoot set so both position stores are persisted.
	msg := stdinMsg{Op: "update", Kind: "node", Attr: "move"}
	msg.Entries = map[string]moveEntry{
		"src": {NodeId: "src", X: 400, Y: 250, Z: 30},
	}
	applyUpdate(msg, md, tr, root)

	// Read the two on-disk stores directly and assert they agree for the dragged node.
	readJSON := func(p string, v any) {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
	}
	var meta jsonMeta
	readJSON(filepath.Join(root, "nodes", "src", "meta.json"), &meta)
	var view specPosition
	readJSON(filepath.Join(root, "view", "nodes", "src.json"), &view)

	if meta.X != view.X || meta.Y != view.Y || meta.Z != view.Z {
		t.Fatalf("position stores diverged after drag: meta=(%v,%v,%v) view=%+v",
			meta.X, meta.Y, meta.Z, view)
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
