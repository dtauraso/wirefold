package Wiring

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadTreeRoundTrip(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	spec, err := loadTree(filepath.Join(repoRoot, "topology"))
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}

	if len(spec.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(spec.Edges))
	}

	nodeByID := map[string]specNode{}
	for _, n := range spec.Nodes {
		nodeByID[n.ID] = n
	}

	n1, ok := nodeByID["1"]
	if !ok {
		t.Fatal("node \"1\" not found")
	}
	if n1.Type != "Input" {
		t.Errorf("node \"1\" type: got %q, want \"Input\"", n1.Type)
	}

	n2, ok := nodeByID["2"]
	if !ok {
		t.Fatal("node \"2\" not found")
	}
	if n2.Type != "ChainInhibitor" {
		t.Errorf("node \"2\" type: got %q, want \"ChainInhibitor\"", n2.Type)
	}

	n3, ok := nodeByID["3"]
	if !ok {
		t.Fatal("node \"3\" not found")
	}
	if n3.Type != "ChainInhibitor" {
		t.Errorf("node \"3\" type: got %q, want \"ChainInhibitor\"", n3.Type)
	}

	// All nodes should have non-zero position (view positions exist in topology/view/)
	for _, n := range spec.Nodes {
		if n.Position.X == 0 && n.Position.Y == 0 {
			t.Errorf("node %q has zero position", n.ID)
		}
	}

	// View positions should be populated for all nodes
	for _, n := range spec.Nodes {
		if _, ok := spec.View.Nodes[n.ID]; !ok {
			t.Errorf("node %q missing from view.nodes", n.ID)
		}
	}

	edgeByLabel := map[string]specEdge{}
	for _, e := range spec.Edges {
		edgeByLabel[e.Label] = e
	}

	e1to2, ok := edgeByLabel["1To2"]
	if !ok {
		t.Fatal("edge \"1To2\" not found")
	}
	if e1to2.SourceHandle != "ToReadGate" {
		t.Errorf("edge 1To2 sourceHandle: got %q, want \"ToReadGate\"", e1to2.SourceHandle)
	}
	if e1to2.TargetHandle != "FromPrevChainInhibitorNode" {
		t.Errorf("edge 1To2 targetHandle: got %q, want \"FromPrevChainInhibitorNode\"", e1to2.TargetHandle)
	}

	if _, ok := edgeByLabel["2To3"]; !ok {
		t.Fatal("edge \"2To3\" not found")
	}
	if _, ok := edgeByLabel["2FeedbackTo1"]; !ok {
		t.Fatal("edge \"2FeedbackTo1\" not found")
	}

	// All nodes should have at least 1 input OR at least 1 output port
	for _, n := range spec.Nodes {
		if len(n.Inputs) == 0 && len(n.Outputs) == 0 {
			t.Errorf("node %q has no input or output ports", n.ID)
		}
	}
}

func comparePortLists(t *testing.T, nodeID, dir string, a, b []specPort) {
	t.Helper()
	if len(a) != len(b) {
		t.Errorf("node %q %s: len tree=%d mono=%d", nodeID, dir, len(a), len(b))
		return
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Errorf("node %q %s[%d] name: %q vs %q", nodeID, dir, i, a[i].Name, b[i].Name)
		}
		if a[i].Side != b[i].Side {
			t.Errorf("node %q %s[%d] side: %q vs %q", nodeID, dir, i, a[i].Side, b[i].Side)
		}
		as, bs := a[i].Slot, b[i].Slot
		slotMismatch := (as == nil) != (bs == nil) || (as != nil && bs != nil && *as != *bs)
		if slotMismatch {
			t.Errorf("node %q %s[%d] slot mismatch", nodeID, dir, i)
		}
	}
}
