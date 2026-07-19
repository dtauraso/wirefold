package Wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeLoaderTreeFixture lays down a small, self-contained directory-tree topology
// (independent of the production topology/ dir) that exercises the loadTree shapes
// TestLoadTreeRoundTrip asserts on: a node with only an output port, a node with both
// input and output ports, distinct source/target handles per edge, and one edge label
// that is deliberately NEVER written to the fixture (so an absence assertion on it is a
// genuine proof, not a tautology about a string nobody could produce).
func writeLoaderTreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTreeFile(t, root, "nodes/n1/meta.json", `{"id":"n1","type":"Input"}`)
	writeTreeFile(t, root, "nodes/n1/outputs/Out.json", `{"name":"Out"}`)
	writeTreeFile(t, root, "nodes/n2/meta.json", `{"id":"n2","type":"HoldNewSendOld"}`)
	writeTreeFile(t, root, "nodes/n2/inputs/FromPrev.json", `{"name":"FromPrev"}`)
	writeTreeFile(t, root, "nodes/n2/outputs/ToNext.json", `{"name":"ToNext"}`)
	writeTreeFile(t, root, "nodes/n3/meta.json", `{"id":"n3","type":"Hold"}`)
	writeTreeFile(t, root, "nodes/n3/inputs/FromInput.json", `{"name":"FromInput"}`)
	writeTreeFile(t, root, "edges/n1Ton2.json", `{"label":"n1Ton2","kind":"data","source":"n1","sourceHandle":"Out","target":"n2","targetHandle":"FromPrev"}`)
	writeTreeFile(t, root, "edges/n2Ton3.json", `{"label":"n2Ton3","kind":"data","source":"n2","sourceHandle":"ToNext","target":"n3","targetHandle":"FromInput"}`)
	return root
}

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
	g := nodeGeom{nodeIdentity: nodeIdentity{Kind: "HoldFlip"}, Inputs: geom}
	center := nodeWorldPos(g)
	dir0 := ringAnchorDir(nodeRadius(g.Kind), 0)
	want := center.add(dir0.scale(33.5))
	got := portWorldPos(g, "In", true)
	if got != want {
		t.Fatalf("portWorldPos = %v, want %v (portR=33.5 authoritative)", got, want)
	}
}

// TestLoadTreeRoundTrip drives loadTree against a small, self-contained fixture (NOT
// the live production topology/ dir — that dir is the visual editor's OWN save target,
// so a change-detector pinned to its exact node/edge count would break on every
// legitimate editor edit with no loader bug involved) and asserts the genuinely general
// loader behaviors: node id/type round-trip, edge source/target/handle round-trip, and
// absence of a label that was deliberately never written to the fixture.
func TestLoadTreeRoundTrip(t *testing.T) {
	root := writeLoaderTreeFixture(t)

	spec, err := loadTree(root)
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}

	if len(spec.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(spec.Nodes))
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(spec.Edges))
	}

	nodeByID := map[string]specNode{}
	for _, n := range spec.Nodes {
		nodeByID[n.ID] = n
	}

	n1, ok := nodeByID["n1"]
	if !ok {
		t.Fatal("node \"n1\" not found")
	}
	if n1.Type != "Input" {
		t.Errorf("node \"n1\" type: got %q, want \"Input\"", n1.Type)
	}

	n2, ok := nodeByID["n2"]
	if !ok {
		t.Fatal("node \"n2\" not found")
	}
	if n2.Type != "HoldNewSendOld" {
		t.Errorf("node \"n2\" type: got %q, want \"HoldNewSendOld\"", n2.Type)
	}

	n3, ok := nodeByID["n3"]
	if !ok {
		t.Fatal("node \"n3\" not found")
	}
	if n3.Type != "Hold" {
		t.Errorf("node \"n3\" type: got %q, want \"Hold\"", n3.Type)
	}

	edgeByLabel := map[string]specEdge{}
	for _, e := range spec.Edges {
		edgeByLabel[e.Label] = e
	}

	e1, ok := edgeByLabel["n1Ton2"]
	if !ok {
		t.Fatal("edge \"n1Ton2\" not found")
	}
	if e1.Source != "n1" {
		t.Errorf("edge n1Ton2 source: got %q, want \"n1\"", e1.Source)
	}
	if e1.SourceHandle != "Out" {
		t.Errorf("edge n1Ton2 sourceHandle: got %q, want \"Out\"", e1.SourceHandle)
	}
	if e1.Target != "n2" {
		t.Errorf("edge n1Ton2 target: got %q, want \"n2\"", e1.Target)
	}
	if e1.TargetHandle != "FromPrev" {
		t.Errorf("edge n1Ton2 targetHandle: got %q, want \"FromPrev\"", e1.TargetHandle)
	}

	e2, ok := edgeByLabel["n2Ton3"]
	if !ok {
		t.Fatal("edge \"n2Ton3\" not found")
	}
	if e2.SourceHandle != "ToNext" {
		t.Errorf("edge n2Ton3 sourceHandle: got %q, want \"ToNext\"", e2.SourceHandle)
	}
	if e2.TargetHandle != "FromInput" {
		t.Errorf("edge n2Ton3 targetHandle: got %q, want \"FromInput\"", e2.TargetHandle)
	}

	// A label never written to the fixture must genuinely be absent — this is a real
	// proof here (unlike the deleted production-pinned version, which asserted absence
	// of a string that no code path could ever produce again).
	if _, ok := edgeByLabel["n1Ton3"]; ok {
		t.Error("edge \"n1Ton3\" was never written to the fixture and should not exist")
	}
}

// TestProductionTopologyIsWellFormed asserts the one loadTree invariant that survives
// any legitimate edit to the live production topology/ dir (the visual editor's save
// target): every node declares at least one input or output port. Unlike
// TestLoadTreeRoundTrip's fixture-based assertions, this one is NOT pinned to exact
// node/edge counts or specific ids, so it does not need updating when the topology
// changes shape.
func TestProductionTopologyIsWellFormed(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	spec, err := loadTree(filepath.Join(repoRoot, "topology"))
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}

	for _, n := range spec.Nodes {
		if len(n.Inputs) == 0 && len(n.Outputs) == 0 {
			t.Errorf("node %q has no input or output ports", n.ID)
		}
	}
}
