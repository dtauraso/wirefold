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

	v, err := FromChainInhibitor(goSrc, []string{"i0"})
	if err != nil {
		t.Fatalf("FromChainInhibitor: %v", err)
	}

	got := RenderChainInhibitor(v)
	want := "send held -> i0\nkeep input\n"
	if got != want {
		t.Errorf("RenderChainInhibitor output mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestChainInhibitor_ParseRenderIdentity: Render → Parse → identity (OutNeighbors unchanged).
func TestChainInhibitor_ParseRenderIdentity(t *testing.T) {
	goSrc := loadChainInhibitorNodeGo(t)

	orig, err := FromChainInhibitor(goSrc, []string{"i0"})
	if err != nil {
		t.Fatalf("FromChainInhibitor: %v", err)
	}

	rendered := RenderChainInhibitor(orig)
	t.Logf("rendered: %q", rendered)

	parsed, err := ParseChainInhibitor(rendered, orig)
	if err != nil {
		t.Fatalf("ParseChainInhibitor: %v", err)
	}

	if len(parsed.OutNeighbors) != len(orig.OutNeighbors) {
		t.Errorf("OutNeighbors length: got %d want %d", len(parsed.OutNeighbors), len(orig.OutNeighbors))
	} else if parsed.OutNeighbors[0] != orig.OutNeighbors[0] {
		t.Errorf("OutNeighbors[0]: got %q want %q", parsed.OutNeighbors[0], orig.OutNeighbors[0])
	}
}

// TestChainInhibitor_MalformedInput: garbage must produce *ParseChainInhibitorError
// with non-empty Suggestion().
func TestChainInhibitor_MalformedInput(t *testing.T) {
	prior := ChainInhibitorView{OutNeighbors: []string{"i0"}}
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
	v := ChainInhibitorView{OutNeighbors: []string{"i0"}}
	src, outNeighbors, removedPorts, err := ToChainInhibitor(v)
	if err != nil {
		t.Fatalf("ToChainInhibitor: %v", err)
	}
	if len(outNeighbors) != 1 || outNeighbors[0] != "i0" {
		t.Errorf("newOutNeighbors: got %v want [i0]", outNeighbors)
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
	v2, err := FromChainInhibitor(src, []string{"i0"})
	if err != nil {
		t.Fatalf("FromChainInhibitor on ToChainInhibitor output: %v", err)
	}
	if len(v2.OutNeighbors) != 1 || v2.OutNeighbors[0] != "i0" {
		t.Errorf("re-parsed OutNeighbors: got %v want [i0]", v2.OutNeighbors)
	}
}

// TestChainInhibitor_OutNeighborChange: parsing with a different neighbor name
// reflects in the view.
func TestChainInhibitor_OutNeighborChange(t *testing.T) {
	prior := ChainInhibitorView{OutNeighbors: []string{"i0"}}
	text := "send held -> i1\nkeep input"
	v, err := ParseChainInhibitor(text, prior)
	if err != nil {
		t.Fatalf("ParseChainInhibitor: %v", err)
	}
	if len(v.OutNeighbors) != 1 || v.OutNeighbors[0] != "i1" {
		t.Errorf("OutNeighbors: got %v want [i1]", v.OutNeighbors)
	}
}

// TestChainInhibitor_MultipleOutNeighbors: per-line neighbors render and parse correctly.
func TestChainInhibitor_MultipleOutNeighbors(t *testing.T) {
	goSrc := loadChainInhibitorNodeGo(t)

	// FromChainInhibitor with multiple neighbors.
	v, err := FromChainInhibitor(goSrc, []string{"a0", "b1", "c2"})
	if err != nil {
		t.Fatalf("FromChainInhibitor: %v", err)
	}
	if len(v.OutNeighbors) != 3 {
		t.Fatalf("OutNeighbors length: got %d want 3", len(v.OutNeighbors))
	}

	rendered := RenderChainInhibitor(v)
	want := "send held -> a0\nsend held -> b1\nsend held -> c2\nkeep input\n"
	if rendered != want {
		t.Errorf("RenderChainInhibitor: got %q want %q", rendered, want)
	}

	// Parse back — must recover all three neighbors.
	parsed, err := ParseChainInhibitor(rendered, v)
	if err != nil {
		t.Fatalf("ParseChainInhibitor multi: %v", err)
	}
	if len(parsed.OutNeighbors) != 3 {
		t.Fatalf("parsed OutNeighbors length: got %d want 3", len(parsed.OutNeighbors))
	}
	for i, want := range []string{"a0", "b1", "c2"} {
		if parsed.OutNeighbors[i] != want {
			t.Errorf("OutNeighbors[%d]: got %q want %q", i, parsed.OutNeighbors[i], want)
		}
	}
}

// TestChainInhibitor_SuggestionMultiNeighbor: suggestion contains all neighbor ids.
func TestChainInhibitor_SuggestionMultiNeighbor(t *testing.T) {
	prior := ChainInhibitorView{OutNeighbors: []string{"x0", "y1"}}
	_, err := ParseChainInhibitor("not valid pseudo", prior)
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *ParseChainInhibitorError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseChainInhibitorError, got %T", err)
	}
	sug := pe.Suggestion()
	if !strings.Contains(sug, "x0") || !strings.Contains(sug, "y1") {
		t.Errorf("Suggestion() should mention all neighbors: %q", sug)
	}
}
