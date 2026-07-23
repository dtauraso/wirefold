// node_move_row_table_test.go — pins that MoveDispatch's row-identity tables (node/edge/
// port hit-test resolution + the mover-side row lookups) are built ONCE at load, from the
// SAME stable seed order (md.nodeSeeds/md.edgeSeeds) that Buffer.SnapshotState's row order
// used to be independently discovered in via its first-geometry-event bookkeeping. This is
// the MoveDispatch-side analogue of Buffer/row_order_test.go: proof that the row tables
// this package now owns produce the identical row indices for a representative graph.

package Wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

func TestMoveDispatchRowTablesMatchSeedOrder(t *testing.T) {
	// Three nodes in JSON SPEC ORDER (z-node, a-node, m-node — deliberately not
	// alphabetical, proving row order tracks spec order, not a sort), one edge, so both
	// node and edge row order can be pinned against known seed order, and z-node's two
	// ports (AimedSrc: Out then FeedbackIn, its struct field order) exercise the
	// flattened port-row table's per-node port ordering.
	const topo = `{
	  "nodes": [
	    {"id":"z-node","type":"AimedSrc","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0},
	    {"id":"a-node","type":"AimedSink","scenePolarR":50,"scenePolarTheta":1.5707963267948966,"scenePolarPhi":0},
	    {"id":"m-node","type":"AimedSink","scenePolarR":50,"scenePolarTheta":1.5707963267948966,"scenePolarPhi":3.14159}
	  ],
	  "edges": [
	    {"label":"e0","kind":"data","source":"z-node","sourceHandle":"Out","target":"a-node","targetHandle":"In"}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	clk := NewRealClock()
	_, _, md, _, err := LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Node rows: spec (directory-sorted) order — z-node, a-node, m-node.
	wantNodes := []string{"z-node", "a-node", "m-node"}
	for row, id := range wantNodes {
		got, ok := md.LookupNodeRow(row)
		if !ok || got != id {
			t.Fatalf("LookupNodeRow(%d)=(%q,%v) want (%q,true)", row, got, ok, id)
		}
		if r, ok := md.NodeRowFor(id); !ok || r != int32(row) {
			t.Fatalf("NodeRowFor(%q)=(%d,%v) want (%d,true)", id, r, ok, row)
		}
	}
	if _, ok := md.LookupNodeRow(len(wantNodes)); ok {
		t.Fatalf("LookupNodeRow(%d) out of range: want ok=false", len(wantNodes))
	}

	// Edge rows: one edge, row 0.
	if l, ok := md.LookupEdgeRow(0); !ok || l != "e0" {
		t.Fatalf("LookupEdgeRow(0)=(%q,%v) want (e0,true)", l, ok)
	}
	if _, ok := md.LookupEdgeRow(1); ok {
		t.Fatalf("LookupEdgeRow(1) out of range: want ok=false")
	}
	if r, ok := md.EdgeRowForPair("a-node", "z-node"); !ok || r != 0 {
		t.Fatalf("EdgeRowForPair(a-node,z-node)=(%d,%v) want (0,true)", r, ok)
	}
	if r, ok := md.EdgeRowForPair("z-node", "a-node"); !ok || r != 0 {
		t.Fatalf("EdgeRowForPair(z-node,a-node)=(%d,%v) want (0,true) (order-independent)", r, ok)
	}
	if _, ok := md.EdgeRowForPair("a-node", "m-node"); ok {
		t.Fatalf("EdgeRowForPair(a-node,m-node): want ok=false (no such edge)")
	}

	// Port rows: flattened node-row order × each node's Ports order — z-node (AimedSrc)'s
	// FeedbackIn then Out (inputs-before-outputs port ordering, rows 0,1), then a-node's
	// one In (row 2), then m-node's one In (row 3).
	wantPorts := []struct {
		row     int
		node    string
		port    string
		isInput bool
	}{
		{0, "z-node", "FeedbackIn", true},
		{1, "z-node", "Out", false},
		{2, "a-node", "In", true},
		{3, "m-node", "In", true},
	}
	for _, c := range wantPorts {
		node, port, isInput, ok := md.LookupPortRow(c.row)
		if !ok || node != c.node || port != c.port || isInput != c.isInput {
			t.Fatalf("LookupPortRow(%d)=(%q,%q,%v,%v) want (%q,%q,%v,true)",
				c.row, node, port, isInput, ok, c.node, c.port, c.isInput)
		}
		if r, ok := md.PortRowFor(c.node, c.port, c.isInput); !ok || r != int32(c.row) {
			t.Fatalf("PortRowFor(%q,%q,%v)=(%d,%v) want (%d,true)", c.node, c.port, c.isInput, r, ok, c.row)
		}
	}
	if _, _, _, ok := md.LookupPortRow(len(wantPorts)); ok {
		t.Fatalf("LookupPortRow(%d) out of range: want ok=false", len(wantPorts))
	}
}
