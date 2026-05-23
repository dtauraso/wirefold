package pseudo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRenderReadGate_RoundTrip(t *testing.T) {
	// Locate nodes/readgate/node.go relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	nodePath := filepath.Join(repoRoot, "nodes", "readgate", "node.go")

	goSrc, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatalf("read nodes/readgate/node.go: %v", err)
	}

	v, err := FromReadGate(goSrc, "i0")
	if err != nil {
		t.Fatalf("FromReadGate: %v", err)
	}

	got := RenderReadGate(v)
	want := "if value and signal\n   send value -> edge to i0\n"
	if got != want {
		t.Errorf("RenderReadGate output mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
