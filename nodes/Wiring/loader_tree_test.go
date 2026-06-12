package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestLoadTreeRoundTrip(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	treeSpec, err := loadTree(filepath.Join(repoRoot, "topology"))
	if err != nil {
		t.Fatalf("loadTree: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot, "topology.json"))
	if err != nil {
		t.Fatalf("read topology.json: %v", err)
	}
	var monoSpec topoSpec
	if err := json.Unmarshal(raw, &monoSpec); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// populate positions (as LoadTopology does)
	for i := range monoSpec.Nodes {
		if pos, ok := monoSpec.View.Nodes[monoSpec.Nodes[i].ID]; ok {
			monoSpec.Nodes[i].Position = pos
		}
	}

	sortNodes := func(nodes []specNode) {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	}
	normPorts := func(ports []specPort) []specPort {
		sorted := make([]specPort, len(ports))
		copy(sorted, ports)
		sort.Slice(sorted, func(i, j int) bool {
			si, sj := sorted[i].Slot, sorted[j].Slot
			if si == nil && sj == nil {
				return sorted[i].Name < sorted[j].Name
			}
			if si == nil {
				return false
			}
			if sj == nil {
				return true
			}
			if *si != *sj {
				return *si < *sj
			}
			return sorted[i].Name < sorted[j].Name
		})
		return sorted
	}

	sortNodes(treeSpec.Nodes)
	sortNodes(monoSpec.Nodes)

	if len(treeSpec.Nodes) != len(monoSpec.Nodes) {
		t.Fatalf("node count: tree=%d mono=%d", len(treeSpec.Nodes), len(monoSpec.Nodes))
	}
	for i, tn := range treeSpec.Nodes {
		mn := monoSpec.Nodes[i]
		if tn.ID != mn.ID {
			t.Errorf("node[%d] id: tree=%q mono=%q", i, tn.ID, mn.ID)
		}
		if tn.Type != mn.Type {
			t.Errorf("node %q type: tree=%q mono=%q", tn.ID, tn.Type, mn.Type)
		}
		if tn.Position != mn.Position {
			t.Errorf("node %q position: tree=%v mono=%v", tn.ID, tn.Position, mn.Position)
		}
		tInputs := normPorts(tn.Inputs)
		mInputs := normPorts(mn.Inputs)
		comparePortLists(t, tn.ID, "inputs", tInputs, mInputs)
		tOutputs := normPorts(tn.Outputs)
		mOutputs := normPorts(mn.Outputs)
		comparePortLists(t, tn.ID, "outputs", tOutputs, mOutputs)
	}

	sortEdges := func(edges []specEdge) {
		sort.Slice(edges, func(i, j int) bool { return edges[i].Label < edges[j].Label })
	}
	sortEdges(treeSpec.Edges)
	sortEdges(monoSpec.Edges)
	if len(treeSpec.Edges) != len(monoSpec.Edges) {
		t.Fatalf("edge count: tree=%d mono=%d", len(treeSpec.Edges), len(monoSpec.Edges))
	}
	for i, te := range treeSpec.Edges {
		me := monoSpec.Edges[i]
		if te != me {
			t.Errorf("edge[%d]: tree=%+v mono=%+v", i, te, me)
		}
	}

	for id, tp := range treeSpec.View.Nodes {
		mp, ok := monoSpec.View.Nodes[id]
		if !ok {
			t.Errorf("view.nodes: tree has %q, mono does not", id)
		}
		if tp != mp {
			t.Errorf("view.nodes[%q]: tree=%v mono=%v", id, tp, mp)
		}
	}
	for id := range monoSpec.View.Nodes {
		if _, ok := treeSpec.View.Nodes[id]; !ok {
			t.Errorf("view.nodes: mono has %q, tree does not", id)
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
