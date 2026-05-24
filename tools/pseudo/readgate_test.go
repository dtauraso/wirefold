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
	want := "if input value and signal\n   input value -> i0\n"
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

// TestReadGate_GuardDrop: parse "if input value\n   input value -> i0" produces
// a single-term guard, and ToReadGate emits an Update body gated only on HasValue.
func TestReadGate_GuardDrop(t *testing.T) {
	prior := ReadGateView{GuardTerms: []string{"input value", "signal"}, OutNeighbor: "i0"}
	text := "if input value\n   input value -> i0"

	v, err := ParseReadGate(text, prior)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}
	if len(v.GuardTerms) != 1 || v.GuardTerms[0] != "input value" {
		t.Errorf("GuardTerms: got %v want [input value]", v.GuardTerms)
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
	prior := ReadGateView{GuardTerms: []string{"input value", "signal"}, OutNeighbor: "i0"}
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

// TestReadGate_OrGate_RoundTrip: a view with Gate="or" renders with "or",
// parses back, ToReadGate emits ||, and FromReadGate re-detects it as "or".
func TestReadGate_OrGate_RoundTrip(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"input value", "signal"}, Gate: "or", OutNeighbor: "i0"}

	rendered := RenderReadGate(v)
	want := "if input value or signal\n   input value -> i0\n"
	if rendered != want {
		t.Errorf("RenderReadGate: got %q want %q", rendered, want)
	}

	parsed, err := ParseReadGate(rendered, v)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}
	if parsed.Gate != "or" {
		t.Errorf("parsed Gate: got %q want \"or\"", parsed.Gate)
	}
	if len(parsed.GuardTerms) != 2 || parsed.GuardTerms[1] != "signal" {
		t.Errorf("parsed GuardTerms: got %v", parsed.GuardTerms)
	}

	src, outNeighbor, removedPorts, err := ToReadGate(parsed)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	if outNeighbor != "i0" {
		t.Errorf("outNeighbor: got %q want i0", outNeighbor)
	}
	if len(removedPorts) != 0 {
		t.Errorf("removedPorts: got %v want []", removedPorts)
	}
	srcStr := string(src)
	if !strings.Contains(srcStr, "||") {
		t.Errorf("or-gate source must contain ||:\n%s", srcStr)
	}
	if strings.Contains(srcStr, "&&") {
		t.Errorf("or-gate source must not contain &&:\n%s", srcStr)
	}

	// Re-parse via FromReadGate; should detect Gate="or".
	v2, err := FromReadGate(src, "i0")
	if err != nil {
		t.Fatalf("FromReadGate on or-gate source: %v", err)
	}
	if v2.Gate != "or" {
		t.Errorf("FromReadGate Gate: got %q want \"or\"", v2.Gate)
	}
}

// TestReadGate_OrGate_Parse: parsing "if input value or signal\n..." stores Gate="or".
func TestReadGate_OrGate_Parse(t *testing.T) {
	prior := ReadGateView{GuardTerms: []string{"input value", "signal"}, Gate: "or", OutNeighbor: "i0"}
	text := "if input value or signal\n   input value -> i0"
	parsed, err := ParseReadGate(text, prior)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}
	if parsed.Gate != "or" {
		t.Errorf("Gate: got %q want \"or\"", parsed.Gate)
	}
	if parsed.OutNeighbor != "i0" {
		t.Errorf("OutNeighbor: got %q want i0", parsed.OutNeighbor)
	}
}

// TestReadGate_AndGate_ExplicitDefault: parsing "if input value and signal\n..." stores Gate="and".
func TestReadGate_AndGate_ExplicitDefault(t *testing.T) {
	prior := ReadGateView{GuardTerms: []string{"input value", "signal"}, Gate: "and", OutNeighbor: "i0"}
	text := "if input value and signal\n   input value -> i0"
	parsed, err := ParseReadGate(text, prior)
	if err != nil {
		t.Fatalf("ParseReadGate: %v", err)
	}
	if parsed.Gate != "and" {
		t.Errorf("Gate: got %q want \"and\"", parsed.Gate)
	}
}

// TestToReadGate_TwoTermCompiles: ToReadGate with two guard terms produces valid
// Go source containing the expected guard tokens.
func TestToReadGate_TwoTermCompiles(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"input value", "signal"}, OutNeighbor: "i0"}
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

// TestToReadGate_OrGate_GuardedDone: OR gate must guard each Done call by its flag.
func TestToReadGate_OrGate_GuardedDone(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"input value", "signal"}, Gate: "or", OutNeighbor: "i0"}
	src, _, _, err := ToReadGate(v)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	srcStr := string(src)

	// Must use || operator.
	if !strings.Contains(srcStr, "||") {
		t.Errorf("or-gate source must contain ||")
	}

	// Must have guarded HasValue block.
	if !strings.Contains(srcStr, "if g.HasValue {") {
		t.Errorf("or-gate source must contain guarded 'if g.HasValue {'")
	}
	// Must have guarded HasChainInhibitor block.
	if !strings.Contains(srcStr, "if g.HasChainInhibitor {") {
		t.Errorf("or-gate source must contain guarded 'if g.HasChainInhibitor {'")
	}

	// Must NOT have bare unconditional consecutive Done calls (AND pattern).
	// Check that "g.FromInput.Done()" is not immediately followed by "g.FromChainInhibitor.Done()"
	// with only whitespace/newline in between (i.e., no guard between them).
	if strings.Contains(srcStr, "g.FromInput.Done()\n\t\t\tg.FromChainInhibitor.Done()") {
		t.Errorf("or-gate source must not call Done() on both inputs unconditionally back-to-back")
	}
}

// TestToReadGate_AndGate_UnconditionalDone: AND gate must keep unconditional Done calls.
func TestToReadGate_AndGate_UnconditionalDone(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"input value", "signal"}, Gate: "and", OutNeighbor: "i0"}
	src, _, _, err := ToReadGate(v)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	srcStr := string(src)

	// Must use && operator.
	if !strings.Contains(srcStr, "&&") {
		t.Errorf("and-gate source must contain &&")
	}

	// Must have unconditional back-to-back Done calls.
	if !strings.Contains(srcStr, "g.FromInput.Done()\n\t\t\tg.FromChainInhibitor.Done()") {
		t.Errorf("and-gate source must call Done() on both inputs unconditionally back-to-back")
	}
}

// TestReadGate_PortNamesInGeneratedSource guards that the port-name constants
// in the const block and the emit templates cannot silently diverge: the source
// produced by ToReadGate for a 2-term view must contain all three port names.
func TestReadGate_PortNamesInGeneratedSource(t *testing.T) {
	v := ReadGateView{GuardTerms: []string{"input value", "signal"}, OutNeighbor: "i0"}
	src, _, _, err := ToReadGate(v)
	if err != nil {
		t.Fatalf("ToReadGate: %v", err)
	}
	srcStr := string(src)
	for _, name := range []string{portFromInput, portFromChainInhibitor, portToChainInhibitor} {
		if !strings.Contains(srcStr, name) {
			t.Errorf("generated source must contain port name %q", name)
		}
	}
}
