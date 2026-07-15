package Wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestReadPortsParsesPortR verifies that a port file's optional "portR" field is
// parsed into specPort.PortR and, via specPortsToGeom → portGeom, drives
// portWorldPos placement (the materialized on-disk value is authoritative).
func TestReadPortsParsesPortR(t *testing.T) {
	dir := t.TempDir()
	portFile := filepath.Join(dir, "In.json")
	if err := os.WriteFile(portFile, []byte(`{"name":"In","anchorId":0,"portR":33.5}`), 0o644); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	ports, err := readPorts(dir)
	if err != nil {
		t.Fatalf("readPorts: %v", err)
	}
	if len(ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(ports))
	}
	if ports[0].PortR == nil || *ports[0].PortR != 33.5 {
		t.Fatalf("PortR = %v, want 33.5", ports[0].PortR)
	}

	geom := specPortsToGeom(ports)
	g := nodeGeom{Kind: "HoldFlip", Inputs: geom}
	center := nodeWorldPos(g)
	dir0 := ringAnchorDir(nodeRadius(g.Kind), 0)
	want := center.add(dir0.scale(33.5))
	got := portWorldPos(g, "In", true)
	if got != want {
		t.Fatalf("portWorldPos = %v, want %v (portR=33.5 authoritative)", got, want)
	}
}

func TestLoadTreeRoundTrip(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	spec, err := loadTree(filepath.Join(repoRoot, "topology"))
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}

	if len(spec.Nodes) != 9 {
		t.Fatalf("expected 9 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 10 {
		t.Fatalf("expected 10 edges, got %d", len(spec.Edges))
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

	n5, ok := nodeByID["5"]
	if !ok {
		t.Fatal("node \"5\" not found")
	}
	if n5.Type != "HoldNewSendOld" {
		t.Errorf("node \"5\" type: got %q, want \"HoldNewSendOld\"", n5.Type)
	}

	n7, ok := nodeByID["7"]
	if !ok {
		t.Fatal("node \"7\" not found")
	}
	if n7.Type != "Hold" {
		t.Errorf("node \"7\" type: got %q, want \"Hold\"", n7.Type)
	}

	n6, ok := nodeByID["6"]
	if !ok {
		t.Fatal("node \"6\" not found")
	}
	if n6.Type != "Pulse" {
		t.Errorf("node \"6\" type: got %q, want \"Pulse\"", n6.Type)
	}

	n8, ok := nodeByID["8"]
	if !ok {
		t.Fatal("node \"8\" not found")
	}
	if n8.Type != "Pulse" {
		t.Errorf("node \"8\" type: got %q, want \"Pulse\"", n8.Type)
	}

	edgeByLabel := map[string]specEdge{}
	for _, e := range spec.Edges {
		edgeByLabel[e.Label] = e
	}

	// Node 1 now feeds node 2 (the chain head); node 2 fans to its children 5 and 6.
	e1to2, ok := edgeByLabel["1To2"]
	if !ok {
		t.Fatal("edge \"1To2\" not found")
	}
	if e1to2.SourceHandle != "ToHoldNewSendOld" {
		t.Errorf("edge 1To2 sourceHandle: got %q, want \"ToHoldNewSendOld\"", e1to2.SourceHandle)
	}
	if e1to2.TargetHandle != "FromPrevHoldNewSendOldNode" {
		t.Errorf("edge 1To2 targetHandle: got %q, want \"FromPrevHoldNewSendOldNode\"", e1to2.TargetHandle)
	}
	if _, ok := edgeByLabel["2To5"]; !ok {
		t.Fatal("edge \"2To5\" not found")
	}

	if _, ok := edgeByLabel["5To7"]; !ok {
		t.Fatal("edge \"5To7\" not found")
	}
	if _, ok := edgeByLabel["5FeedbackTo1"]; ok {
		t.Error("edge \"5FeedbackTo1\" should have been removed")
	}

	e2to6, ok := edgeByLabel["2To6"]
	if !ok {
		t.Fatal("edge \"2To6\" not found")
	}
	if e2to6.SourceHandle != "ToNext0" {
		t.Errorf("edge 2To6 sourceHandle: got %q, want \"ToNext0\"", e2to6.SourceHandle)
	}
	if e2to6.TargetHandle != "FromInput" {
		t.Errorf("edge 2To6 targetHandle: got %q, want \"FromInput\"", e2to6.TargetHandle)
	}

	e6to10, ok := edgeByLabel["6To10"]
	if !ok {
		t.Fatal("edge \"6To10\" not found")
	}
	if e6to10.TargetHandle != "FromLeft" {
		t.Errorf("edge 6To10 targetHandle: got %q, want \"FromLeft\"", e6to10.TargetHandle)
	}
	if _, ok := edgeByLabel["5To10"]; ok {
		t.Error("edge \"5To10\" should have been removed")
	}

	// Node 8 (Pulse) is wired 5 -> 8 -> 10; node 4 no longer exists.
	e5to8, ok := edgeByLabel["5To8"]
	if !ok {
		t.Fatal("edge \"5To8\" not found")
	}
	if e5to8.SourceHandle != "ToNext1" {
		t.Errorf("edge 5To8 sourceHandle: got %q, want \"ToNext1\"", e5to8.SourceHandle)
	}
	if e5to8.TargetHandle != "FromInput" {
		t.Errorf("edge 5To8 targetHandle: got %q, want \"FromInput\"", e5to8.TargetHandle)
	}

	e8to10, ok := edgeByLabel["8To10"]
	if !ok {
		t.Fatal("edge \"8To10\" not found")
	}
	if e8to10.Source != "8" {
		t.Errorf("edge 8To10 source: got %q, want \"8\"", e8to10.Source)
	}
	if e8to10.SourceHandle != "Out" {
		t.Errorf("edge 8To10 sourceHandle: got %q, want \"Out\"", e8to10.SourceHandle)
	}
	if e8to10.Target != "10" {
		t.Errorf("edge 8To10 target: got %q, want \"10\"", e8to10.Target)
	}
	if e8to10.TargetHandle != "FromRight" {
		t.Errorf("edge 8To10 targetHandle: got %q, want \"FromRight\"", e8to10.TargetHandle)
	}

	// All nodes should have at least 1 input OR at least 1 output port
	for _, n := range spec.Nodes {
		if len(n.Inputs) == 0 && len(n.Outputs) == 0 {
			t.Errorf("node %q has no input or output ports", n.ID)
		}
	}
}
