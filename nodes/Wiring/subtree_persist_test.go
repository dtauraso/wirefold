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

	// dst's meta got its EXACT scene-polar position (the lossless source of truth loaded
	// verbatim on reload) plus the quantized scalar triple as a self-describing cache; src
	// is byte-for-byte unchanged.
	dstRaw, err := os.ReadFile(filepath.Join(root, "nodes", "dst", "meta.json"))
	if err != nil {
		t.Fatalf("read dst meta: %v", err)
	}
	var dst map[string]json.RawMessage
	_ = json.Unmarshal(dstRaw, &dst)
	for _, k := range []string{"scenePolarR", "scenePolarTheta", "scenePolarPhi"} {
		if _, ok := dst[k]; !ok {
			t.Fatalf("dst %s not persisted (exact position is the source of truth): %s", k, dstRaw)
		}
	}
	if _, ok := dst["quantITheta"]; !ok {
		t.Fatalf("dst quantITheta cache not persisted: %s", dstRaw)
	}
	if _, ok := dst["quantIR"]; !ok {
		t.Fatalf("dst quantIR cache not persisted: %s", dstRaw)
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

// TestDragPositionRoundTripsExactly: dragging a node to an arbitrary continuous target,
// persisting, and RELOADING from disk must place the node at EXACTLY that target — the
// exact scene-polar position is the lossless source of truth (not the coarse quantized
// triple, which would round the drag away).
func TestDragPositionRoundTripsExactly(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	target := vec3{X: 63.7, Y: -21.3, Z: 44.9}
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	md.quantOffsetPersist.flush()

	// Reload from disk into a fresh MoveDispatch and read dst's center back.
	md2 := loadTreeMD(t, root)
	got, ok := md2.centerOfNode("dst")
	if !ok {
		t.Fatal("dst missing after reload")
	}
	const eps = 1e-6
	if d := got.sub(target).length(); d > eps {
		t.Fatalf("dst did not round-trip: dragged to %+v, reloaded at %+v (off by %g)", target, got, d)
	}
}
