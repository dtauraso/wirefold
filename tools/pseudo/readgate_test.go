package pseudo

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// loadReadGateNodeGo reads nodes/readgate/node.go relative to repo root.
func loadReadGateNodeGo(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../nodes/readgate/node.go")
	if err != nil {
		t.Fatalf("could not read nodes/readgate/node.go: %v", err)
	}
	return data
}

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
	want := "if value and signal\n   value -> i0\n"
	if got != want {
		t.Errorf("RenderReadGate output mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestReadGate_ParseRenderIdentity: Render → Parse → identity (guard terms and
// OutNeighbor unchanged).
func TestReadGate_ParseRenderIdentity(t *testing.T) {
	goSrc := loadReadGateNodeGo(t)

	orig, err := FromReadGate(goSrc, "i0")
	if err != nil {
		t.Fatalf("FromReadGate: %v", err)
	}

	rendered := RenderReadGate(orig)
	t.Logf("rendered: %q", rendered)

	parsed, err := ParseReadGate(rendered, orig)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}

	if len(parsed.GuardTerms) != len(orig.GuardTerms) {
		t.Fatalf("GuardTerms length: got %d want %d", len(parsed.GuardTerms), len(orig.GuardTerms))
	}
	for i := range orig.GuardTerms {
		if parsed.GuardTerms[i] != orig.GuardTerms[i] {
			t.Errorf("GuardTerms[%d]: got %q want %q", i, parsed.GuardTerms[i], orig.GuardTerms[i])
		}
	}
	if parsed.OutNeighbor != orig.OutNeighbor {
		t.Errorf("OutNeighbor: got %q want %q", parsed.OutNeighbor, orig.OutNeighbor)
	}
}

// TestReadGate_GuardDrop: parse "if value\n   value -> i0" produces
// a single-term guard, and ToReadGate emits an Update body gated only on HasValue.
func TestReadGate_GuardDrop(t *testing.T) {
	prior := ReadGateView{GuardTerms: []string{"value", "signal"}, OutNeighbor: "i0"}
	text := "if value\n   value -> i0"

	v, err := ParseReadGate(text, prior)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}
	if len(v.GuardTerms) != 1 || v.GuardTerms[0] != "value" {
		t.Errorf("GuardTerms: got %v want [value]", v.GuardTerms)
	}
	if v.OutNeighbor != "i0" {
		t.Errorf("OutNeighbor: got %q want i0", v.OutNeighbor)
	}

	newSrc, newOut, removedPorts, err := ToReadGate(v)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	if newOut != "i0" {
		t.Errorf("newOutNeighbor: got %q want i0", newOut)
	}

	// removedPorts must be ["FromChainInhibitor"] for a 1-term guard.
	if len(removedPorts) != 1 || removedPorts[0] != "FromChainInhibitor" {
		t.Errorf("removedPorts: got %v want [FromChainInhibitor]", removedPorts)
	}

	srcStr := string(newSrc)
	// Single-gate Update body must gate on HasValue alone — no && HasChainInhibitor.
	if strings.Contains(srcStr, "HasValue && HasChainInhibitor") {
		t.Errorf("single-gate source must not use HasValue && HasChainInhibitor:\n%s", srcStr)
	}
	if !strings.Contains(srcStr, "HasValue") {
		t.Errorf("single-gate source must reference HasValue:\n%s", srcStr)
	}
	// Struct must not contain the removed port fields.
	if strings.Contains(srcStr, "FromChainInhibitor") {
		t.Errorf("single-gate source must not contain FromChainInhibitor field:\n%s", srcStr)
	}
	if strings.Contains(srcStr, "HasChainInhibitor") {
		t.Errorf("single-gate source must not contain HasChainInhibitor field:\n%s", srcStr)
	}
}

// TestReadGate_MalformedInput: a garbage string must produce *ParseReadGateError
// with non-empty Suggestion().
func TestReadGate_MalformedInput(t *testing.T) {
	prior := ReadGateView{GuardTerms: []string{"value", "signal"}, OutNeighbor: "i0"}
	_, err := ParseReadGate("not valid pseudo", prior)
	if err == nil {
		t.Fatal("expected error for malformed input, got nil")
	}
	var pe *ParseReadGateError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseReadGateError, got %T: %v", err, err)
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

// TestToReadGate_TwoTermCompiles: ToReadGate with two guard terms produces valid
// Go source containing the expected guard tokens.
func TestToReadGate_TwoTermCompiles(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"value", "signal"}, OutNeighbor: "i0"}
	src, outNeighbor, removedPorts, err := ToReadGate(v)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	if outNeighbor != "i0" {
		t.Errorf("newOutNeighbor: got %q want i0", outNeighbor)
	}
	if len(removedPorts) != 0 {
		t.Errorf("removedPorts: got %v want [] for 2-term guard", removedPorts)
	}
	srcStr := string(src)
	for _, tok := range []string{"HasValue", "HasChainInhibitor", "ToChainInhibitor"} {
		if !strings.Contains(srcStr, tok) {
			t.Errorf("two-term source must contain %q:\n%s", tok, srcStr)
		}
	}
	// The source must be valid Go (format.Source already ran in ToReadGate; reparse
	// via FromReadGate to confirm structural validity).
	v2, err := FromReadGate(src, "i0")
	if err != nil {
		t.Fatalf("FromReadGate on ToReadGate output: %v", err)
	}
	if len(v2.GuardTerms) != 2 {
		t.Errorf("re-parsed GuardTerms length: got %d want 2", len(v2.GuardTerms))
	}
}
