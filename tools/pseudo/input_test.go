package pseudo

import (
	"errors"
	"go/format"
	"os"
	"reflect"
	"strings"
	"testing"
)

// loadInputNodeGo reads nodes/Input/node.go relative to repo root.
// The test binary runs from the package directory; we walk up two levels.
func loadInputNodeGo(t *testing.T) []byte {
	t.Helper()
	// tools/pseudo/ -> tools/ -> repo root
	data, err := os.ReadFile("../../nodes/Input/node.go")
	if err != nil {
		t.Fatalf("could not read nodes/Input/node.go: %v", err)
	}
	return data
}

func gofmtBytes(t *testing.T, src []byte) []byte {
	t.Helper()
	out, err := format.Source(src)
	if err != nil {
		t.Fatalf("gofmt failed: %v\nsource:\n%s", err, src)
	}
	return out
}

// fixtureSpec returns a fresh fixture spec for each test.
func fixtureSpec() map[string]any {
	return map[string]any{
		"init":   []any{0, 1},
		"repeat": true,
		"x":      "keep-me",
	}
}

// TestInputRoundTrip_FullCycle: read real node.go + fixture spec, round-trip
// without edits, assert byte-identical Go and deep-equal spec.
func TestInputRoundTrip_FullCycle(t *testing.T) {
	goSrc := loadInputNodeGo(t)
	spec := fixtureSpec()

	view, err := FromInput(goSrc, spec, "readGate1")
	if err != nil {
		t.Fatalf("FromInput: %v", err)
	}

	rendered := RenderInput(view)
	t.Logf("rendered: %s", rendered)

	view2, err := ParseInput(rendered, view)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}

	newGoSrc, newSpec, err := ToInput(view2)
	if err != nil {
		t.Fatalf("ToInput: %v", err)
	}

	// Go bytes must be byte-identical (compare post-gofmt on both sides).
	wantGo := gofmtBytes(t, goSrc)
	gotGo := gofmtBytes(t, newGoSrc)
	if string(wantGo) != string(gotGo) {
		t.Errorf("Go source not byte-identical after full round-trip.\nwant:\n%s\ngot:\n%s", wantGo, gotGo)
	}

	// Spec must deep-equal the fixture, including the "x" key.
	wantSpec := map[string]any{
		"init":   []any{0, 1},
		"repeat": true,
		"x":      "keep-me",
	}
	if !specEqual(newSpec, wantSpec) {
		t.Errorf("spec mismatch after full round-trip.\nwant: %v\ngot:  %v", wantSpec, newSpec)
	}
}

// TestInputRoundTrip_SpecEditOnly: mutate pseudo to change values and add
// repeatedly; assert Go is byte-identical to original, spec updated correctly.
func TestInputRoundTrip_SpecEditOnly(t *testing.T) {
	goSrc := loadInputNodeGo(t)
	spec := fixtureSpec()

	view, err := FromInput(goSrc, spec, "readGate1")
	if err != nil {
		t.Fatalf("FromInput: %v", err)
	}

	// Mutate spec-origin tokens only: change values, ensure repeatedly is present.
	// Original rendered: "each of [0, 1] -> readGate1 repeatedly"
	// Mutate to: "each of [2, 3, 5] -> readGate1 repeatedly"
	mutated := "each of [2, 3, 5] -> readGate1 repeatedly"
	view2, err := ParseInput(mutated, view)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}

	newGoSrc, newSpec, err := ToInput(view2)
	if err != nil {
		t.Fatalf("ToInput: %v", err)
	}

	// Go bytes must be byte-identical.
	wantGo := gofmtBytes(t, goSrc)
	gotGo := gofmtBytes(t, newGoSrc)
	if string(wantGo) != string(gotGo) {
		t.Errorf("Go source not byte-identical after spec-only edit.\nwant:\n%s\ngot:\n%s", wantGo, gotGo)
	}

	// Spec: init=[2,3,5], repeat=true, x preserved.
	wantSpec := map[string]any{
		"init":   []any{2, 3, 5},
		"repeat": true,
		"x":      "keep-me",
	}
	if !specEqual(newSpec, wantSpec) {
		t.Errorf("spec mismatch after spec-only edit.\nwant: %v\ngot:  %v", wantSpec, newSpec)
	}
}

// TestInputRoundTrip_NeighborEdit: mutate pseudo to change the out-neighbor id;
// assert Go is byte-identical, spec unchanged, and OutNeighbor updated.
func TestInputRoundTrip_NeighborEdit(t *testing.T) {
	goSrc := loadInputNodeGo(t)
	spec := fixtureSpec()

	view, err := FromInput(goSrc, spec, "readGate1")
	if err != nil {
		t.Fatalf("FromInput: %v", err)
	}

	// Change out-neighbor from readGate1 → fanout1.
	mutated := "each of [0, 1] -> fanout1 repeatedly"
	view2, err := ParseInput(mutated, view)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}

	if view2.OutNeighbor != "fanout1" {
		t.Errorf("OutNeighbor = %q; want fanout1", view2.OutNeighbor)
	}

	newGoSrc, newSpec, err := ToInput(view2)
	if err != nil {
		t.Fatalf("ToInput: %v", err)
	}

	// Go bytes must be byte-identical (neighbor edit does not touch Go source).
	wantGo := gofmtBytes(t, goSrc)
	gotGo := gofmtBytes(t, newGoSrc)
	if string(wantGo) != string(gotGo) {
		t.Errorf("Go source changed after neighbor-only edit.\nwant:\n%s\ngot:\n%s", wantGo, gotGo)
	}

	// Spec must equal the fixture (unchanged).
	wantSpec := map[string]any{
		"init":   []any{0, 1},
		"repeat": true,
		"x":      "keep-me",
	}
	if !specEqual(newSpec, wantSpec) {
		t.Errorf("spec not preserved after neighbor edit.\nwant: %v\ngot:  %v", wantSpec, newSpec)
	}
}

// TestInputParse_RejectsExtraTrailingTokens: trailing tokens must cause a
// human-readable error mentioning the offending token.
func TestInputParse_RejectsExtraTrailingTokens(t *testing.T) {
	prior := InputView{OutNeighbor: "readGate1"}
	_, err := ParseInput("each of [0, 1] -> readGate1 boom", prior)
	if err == nil {
		t.Fatal("expected error for trailing token, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Couldn't parse") {
		t.Errorf("error message does not start with human-readable prefix: %q", msg)
	}
	if !strings.Contains(msg, "boom") {
		t.Errorf("error message does not mention offending token %q: %q", "boom", msg)
	}
	t.Logf("got expected error: %v", err)
}

// TestInputParse_SuggestionOnError: a parse failure must return a *ParseInputError
// whose Error() is human-readable and whose Suggestion() includes the canonical form.
func TestInputParse_SuggestionOnError(t *testing.T) {
	prior := InputView{OutNeighbor: "readGate1", InitValues: []int{0, 1}}
	_, err := ParseInput("not valid pseudo text", prior)
	if err == nil {
		t.Fatal("expected error for invalid pseudo text, got nil")
	}
	var pe *ParseInputError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseInputError, got %T: %v", err, err)
	}
	// Error message must be human-readable.
	msg := pe.Error()
	if !strings.Contains(msg, "Couldn't parse") {
		t.Errorf("Error() does not use human-readable prefix: %q", msg)
	}
	// The offending token "not" should appear in the message.
	if !strings.Contains(msg, `"not"`) {
		t.Errorf("Error() does not mention offending token: %q", msg)
	}
	// Message should explain what's expected.
	if !strings.Contains(msg, "each") {
		t.Errorf("Error() does not mention expected start keyword: %q", msg)
	}
	// Suggestion must mention the canonical form and prior OutNeighbor.
	sug := pe.Suggestion()
	if sug == "" {
		t.Fatal("Suggestion() returned empty string")
	}
	if !strings.Contains(sug, "readGate1") {
		t.Errorf("Suggestion() does not mention OutNeighbor: %q", sug)
	}
	if !strings.Contains(sug, "each of") {
		t.Errorf("Suggestion() does not contain canonical form: %q", sug)
	}
	t.Logf("error: %s", msg)
	t.Logf("suggestion: %s", sug)
}

// TestInputFromGo_RejectsMultipleOutputs: two *Wiring.Out fields → error.
func TestInputFromGo_RejectsMultipleOutputs(t *testing.T) {
	twoOutputs := []byte(`package input

import (
	"context"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire    func()
	OutA    *Wiring.Out
	OutB    *Wiring.Out
}

func (n *Node) Update(ctx context.Context) {}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
`)
	spec := map[string]any{"init": []any{}, "repeat": false}
	_, err := FromInput(twoOutputs, spec, "")
	if err == nil {
		t.Fatal("expected error for multiple *Wiring.Out fields, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// specEqual deep-compares two spec maps, normalising init slices to []int for
// comparison (the round-trip may return []any{int} while fixtures use []any{int}).
func specEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		switch k {
		case "init":
			ai := toIntSlice(av)
			bi := toIntSlice(bv)
			if !reflect.DeepEqual(ai, bi) {
				return false
			}
		default:
			if !reflect.DeepEqual(av, bv) {
				return false
			}
		}
	}
	return true
}

func toIntSlice(v any) []int {
	switch s := v.(type) {
	case []int:
		return s
	case []any:
		out := make([]int, len(s))
		for i, elem := range s {
			switch n := elem.(type) {
			case int:
				out[i] = n
			case float64:
				out[i] = int(n)
			}
		}
		return out
	}
	return nil
}
