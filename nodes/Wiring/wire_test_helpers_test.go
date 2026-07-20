package Wiring

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTreeFile writes body to <root>/<rel>, creating any missing parent directories.
// It is the package's single fixture-file writer for directory-tree topology test
// fixtures (nodes/<id>/meta.json, nodes/<id>/inputs|outputs/<name>.json,
// edges/<label>.json); every test that builds an ad hoc tree fixture should call this
// rather than redeclaring its own local mk(rel, body) closure.
func writeTreeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// cascadeSettle is the fixed wall-clock window some drag/neighbor tests sleep after a
// polled convergence, to let any (unwanted) further cascade land before asserting
// absence. It is a widening window, NOT the proof of absence — the proof is an
// SetMsgTap forbidden-kind check (see neighbor_setc_test.go / drag_persist_e2e_test.go);
// a fixed sleep alone can silently pass for the wrong reason under load.
const cascadeSettle = 20 * time.Millisecond

// approxEq is the float tolerance used by geometry/position wire tests.
func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
