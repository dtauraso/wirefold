package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Individual snapping: dragging a node moves and persists ONLY that node (its grid-snapped
// scalar triple, quantITheta/quantIPhi/quantIR — the sole persisted position source under
// the plain-polar model), leaving every other node untouched — no subtree cascade.
func TestIndividualSnap_OnlyDraggedNodePersists(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	srcBefore, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))

	if !md.RootMove("dst", vec3{X: 60, Y: 20, Z: -10}) {
		t.Fatal("RootMove(dst) returned false")
	}
	md.quantOffsetPersist.flush()

	// dst's meta got its grid-snapped scalar triple (integers, by construction); src is
	// byte-for-byte unchanged.
	dstRaw, err := os.ReadFile(filepath.Join(root, "nodes", "dst", "meta.json"))
	if err != nil {
		t.Fatalf("read dst meta: %v", err)
	}
	var dst map[string]json.RawMessage
	_ = json.Unmarshal(dstRaw, &dst)
	if _, ok := dst["quantITheta"]; !ok {
		t.Fatalf("dst quantITheta not persisted: %s", dstRaw)
	}
	if _, ok := dst["quantIPhi"]; !ok {
		t.Fatalf("dst quantIPhi not persisted: %s", dstRaw)
	}
	if _, ok := dst["quantIR"]; !ok {
		t.Fatalf("dst quantIR not persisted: %s", dstRaw)
	}
	if _, ok := dst["scenePolarTheta"]; ok {
		t.Fatalf("dst scenePolarTheta should be deleted (scalars are the sole persisted position): %s", dstRaw)
	}

	// src's SCALAR TRIPLE (scene-center position) must be individually-snap
	// unaffected by a drag of dst — no reference/parent concept, every node is a
	// root for its scene-center position. src's localPolars entry to dst IS
	// expected to change (task/double-link-local-polar: each end of a double
	// link re-quantizes its own local polar to the moved neighbor), so compare
	// everything EXCEPT localPolars.
	srcAfter, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read src meta: %v", err)
	}
	var srcB, srcA map[string]json.RawMessage
	if err := json.Unmarshal(srcBefore, &srcB); err != nil {
		t.Fatalf("unmarshal src before: %v", err)
	}
	if err := json.Unmarshal(srcAfter, &srcA); err != nil {
		t.Fatalf("unmarshal src after: %v", err)
	}
	delete(srcB, "localPolars")
	delete(srcA, "localPolars")
	bJSON, _ := json.Marshal(srcB)
	aJSON, _ := json.Marshal(srcA)
	if string(bJSON) != string(aJSON) {
		t.Fatalf("src's scalar triple changed on a drag of dst (individual snap violated):\nbefore=%s\nafter=%s", bJSON, aJSON)
	}
}
