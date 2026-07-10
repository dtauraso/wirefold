package Wiring

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// Individual snapping: dragging a node moves and persists ONLY that node (grid-snapped
// scene-polar), leaving every other node untouched — no subtree cascade.
func TestIndividualSnap_OnlyDraggedNodePersists(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	srcBefore, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))

	if !md.RootMove("dst", vec3{X: 60, Y: 20, Z: -10}) {
		t.Fatal("RootMove(dst) returned false")
	}
	md.posPersist.flush()
	md.quantOffsetPersist.flush()

	// dst's meta got a grid-snapped scene-polar; src is byte-for-byte unchanged.
	dstRaw, err := os.ReadFile(filepath.Join(root, "nodes", "dst", "meta.json"))
	if err != nil {
		t.Fatalf("read dst meta: %v", err)
	}
	var dst map[string]float64
	_ = json.Unmarshal(dstRaw, &dst)
	// θ must sit on the grid.
	if th, ok := dst["scenePolarTheta"]; ok {
		if r := th / stepTheta; math.Abs(r-math.Round(r)) > 1e-9 {
			t.Fatalf("dst θ not grid-snapped: %v", th)
		}
	} else {
		t.Fatalf("dst scenePolarTheta not persisted: %s", dstRaw)
	}

	srcAfter, _ := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if string(srcBefore) != string(srcAfter) {
		t.Fatalf("src changed on a drag of dst (individual snap violated):\nbefore=%s\nafter=%s", srcBefore, srcAfter)
	}
}
