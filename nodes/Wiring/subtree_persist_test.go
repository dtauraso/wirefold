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

	srcAfter, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if string(srcBefore) != string(srcAfter) {
		t.Fatalf("src changed on a drag of dst (individual snap violated):\nbefore=%s\nafter=%s", srcBefore, srcAfter)
	}
}
