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

	if len(spec.Nodes) != 7 {
		t.Fatalf("expected 7 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 8 {
		t.Fatalf("expected 8 edges, got %d", len(spec.Edges))
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

	n6, ok := nodeByID["6"]
	if !ok {
		t.Fatal("node \"6\" not found")
	}
	if n6.Type != "Excitatory" {
		t.Errorf("node \"6\" type: got %q, want \"Excitatory\"", n6.Type)
	}

	n7, ok := nodeByID["7"]
	if !ok {
		t.Fatal("node \"7\" not found")
	}
	if n7.Type != "Excitatory" {
		t.Errorf("node \"7\" type: got %q, want \"Excitatory\"", n7.Type)
	}

	// View positions should be populated for all nodes (view.nodes is kept as
	// auxiliary view data; node centers no longer derive from it — the lattice
	// cell is the only node-position model).
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
	if e1to2.SourceHandle != "ToChainInhibitor" {
		t.Errorf("edge 1To2 sourceHandle: got %q, want \"ToChainInhibitor\"", e1to2.SourceHandle)
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

	e1to6, ok := edgeByLabel["1To6"]
	if !ok {
		t.Fatal("edge \"1To6\" not found")
	}
	if e1to6.SourceHandle != "ToExcitatory" {
		t.Errorf("edge 1To6 sourceHandle: got %q, want \"ToExcitatory\"", e1to6.SourceHandle)
	}
	if e1to6.TargetHandle != "FromInput" {
		t.Errorf("edge 1To6 targetHandle: got %q, want \"FromInput\"", e1to6.TargetHandle)
	}

	e6to5, ok := edgeByLabel["6To5"]
	if !ok {
		t.Fatal("edge \"6To5\" not found")
	}
	if e6to5.TargetHandle != "FromLeft" {
		t.Errorf("edge 6To5 targetHandle: got %q, want \"FromLeft\"", e6to5.TargetHandle)
	}
	if _, ok := edgeByLabel["2To5"]; ok {
		t.Error("edge \"2To5\" should have been removed")
	}

	// Node 7 (Excitatory) is wired 2 -> 7 -> 4; node 3 no longer feeds node 4.
	e2to7, ok := edgeByLabel["2To7"]
	if !ok {
		t.Fatal("edge \"2To7\" not found")
	}
	if e2to7.SourceHandle != "ToNext1" {
		t.Errorf("edge 2To7 sourceHandle: got %q, want \"ToNext1\"", e2to7.SourceHandle)
	}
	if e2to7.TargetHandle != "FromInput" {
		t.Errorf("edge 2To7 targetHandle: got %q, want \"FromInput\"", e2to7.TargetHandle)
	}

	e7to4, ok := edgeByLabel["7To4"]
	if !ok {
		t.Fatal("edge \"7To4\" not found")
	}
	if e7to4.Source != "7" {
		t.Errorf("edge 7To4 source: got %q, want \"7\"", e7to4.Source)
	}
	if e7to4.SourceHandle != "Out" {
		t.Errorf("edge 7To4 sourceHandle: got %q, want \"Out\"", e7to4.SourceHandle)
	}
	if e7to4.TargetHandle != "In" {
		t.Errorf("edge 7To4 targetHandle: got %q, want \"In\"", e7to4.TargetHandle)
	}

	// Node 4's input now comes from node 7, not node 3.
	if _, ok := edgeByLabel["3To4"]; ok {
		t.Error("edge \"3To4\" should have been removed")
	}

	// All nodes should have at least 1 input OR at least 1 output port
	for _, n := range spec.Nodes {
		if len(n.Inputs) == 0 && len(n.Outputs) == 0 {
			t.Errorf("node %q has no input or output ports", n.ID)
		}
	}
}
