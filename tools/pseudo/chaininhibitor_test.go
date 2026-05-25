package pseudo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// loadChainInhibitorNodeGo reads nodes/chaininhibitor/node.go relative to repo root.
func loadChainInhibitorNodeGo(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../nodes/chaininhibitor/node.go")
	if err != nil {
		t.Fatalf("could not read nodes/chaininhibitor/node.go: %v", err)
	}
	return data
}

func TestRenderChainInhibitor_RoundTrip(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	nodePath := filepath.Join(repoRoot, "nodes", "chaininhibitor", "node.go")

	goSrc, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatalf("read nodes/chaininhibitor/node.go: %v", err)
	}

	v, err := FromChainInhibitor(goSrc, "i0")
	if err != nil {
		t.Fatalf("FromChainInhibitor: %v", err)
	}

	got := RenderChainInhibitor(v)
	want := "send held -> i0\nkeep input\n"
	if got != want {
		t.Errorf("RenderChainInhibitor output mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestChainInhibitor_ParseRenderIdentity: Render → Parse → identity (OutNeighbor unchanged).
func TestChainInhibitor_ParseRenderIdentity(t *testing.T) {
	goSrc := loadChainInhibitorNodeGo(t)

	orig, err := FromChainInhibitor(goSrc, "i0")
	if err != nil {
		t.Fatalf("FromChainInhibitor: %v", err)
	}

	rendered := RenderChainInhibitor(orig)
	t.Logf("rendered: %q", rendered)

	parsed, err := ParseChainInhibitor(rendered, orig)
	if err != nil {
		t.Fatalf("ParseChainInhibitor: %v", err)
	}

	if parsed.OutNeighbor != orig.OutNeighbor {
		t.Errorf("OutNeighbor: got %q want %q", parsed.OutNeighbor, orig.OutNeighbor)
	}
}

// TestChainInhibitor_MalformedInput: garbage must produce *ParseChainInhibitorError
// with non-empty Suggestion().
func TestChainInhibitor_MalformedInput(t *testing.T) {
	prior := ChainInhibitorView{OutNeighbor: "i0"}
	_, err := ParseChainInhibitor("not valid pseudo", prior)
	if err == nil {
		t.Fatal("expected error for malformed input, got nil")
	}
	var pe *ParseChainInhibitorError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseChainInhibitorError, got %T: %v", err, err)
	}
	if pe.Error() == "" {
		t.Error("Error() returned empty string")
	}
	sug := pe.Suggestion()
	if sug == "" {
		t.Error("Suggestion() returned empty string")
	}
	if !strings.Contains(sug, "i0") {
		t.Errorf("Suggestion() should mention OutNeighbor: %q", sug)
	}
	t.Logf("error: %s", pe.Error())
	t.Logf("suggestion: %s", sug)
}

// TestToChainInhibitor_Compiles: ToChainInhibitor produces valid Go source.
func TestToChainInhibitor_Compiles(t *testing.T) {
	v := ChainInhibitorView{OutNeighbor: "i0"}
	src, outNeighbor, removedPorts, err := ToChainInhibitor(v)
	if err != nil {
		t.Fatalf("ToChainInhibitor: %v", err)
	}
	if outNeighbor != "i0" {
		t.Errorf("newOutNeighbor: got %q want i0", outNeighbor)
	}
	if len(removedPorts) != 0 {
		t.Errorf("removedPorts: got %v want []", removedPorts)
	}
	srcStr := string(src)
	for _, tok := range []string{"FromPrevChainInhibitorNode", "ToNext", "Held"} {
		if !strings.Contains(srcStr, tok) {
			t.Errorf("generated source must contain %q:\n%s", tok, srcStr)
		}
	}
	// Confirm the generated source round-trips through FromChainInhibitor.
	v2, err := FromChainInhibitor(src, "i0")
	if err != nil {
		t.Fatalf("FromChainInhibitor on ToChainInhibitor output: %v", err)
	}
	if v2.OutNeighbor != "i0" {
		t.Errorf("re-parsed OutNeighbor: got %q want i0", v2.OutNeighbor)
	}
}

// TestChainInhibitor_OutNeighborChange: parsing with a different neighbor name
// reflects in the view.
func TestChainInhibitor_OutNeighborChange(t *testing.T) {
	prior := ChainInhibitorView{OutNeighbor: "i0"}
	text := "send held -> i1\nkeep input"
	v, err := ParseChainInhibitor(text, prior)
	if err != nil {
		t.Fatalf("ParseChainInhibitor: %v", err)
	}
	if v.OutNeighbor != "i1" {
		t.Errorf("OutNeighbor: got %q want i1", v.OutNeighbor)
	}
}
