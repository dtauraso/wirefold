package Wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Dragging a node must persist the WHOLE moved subtree, not just the dragged node — else
// descendants (which re-aim with it) drift back from stale scenePolar on reload. writeTree
// makes dst the spanning-tree root and src its child; dragging the root must write src's
// offset too.
func TestPersistDragSavesWholeSubtree(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	if !md.RootMove("dst", vec3{X: 60, Y: 20, Z: -10}) {
		t.Fatal("RootMove(dst) returned false")
	}
	md.posPersist.flush()
	md.quantOffsetPersist.flush()

	// Descendant src must now carry a quantized offset in its meta.json.
	raw, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read src meta: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("parse src meta: %v", err)
	}
	for _, k := range []string{"quantITheta", "quantIPhi", "quantIR"} {
		if _, ok := obj[k]; !ok {
			t.Fatalf("descendant src missing %s after root drag — subtree not persisted: %s", k, raw)
		}
	}
}
