package pseudo

import (
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

	view, err := FromInput(goSrc, spec)
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

	view, err := FromInput(goSrc, spec)
	if err != nil {
		t.Fatalf("FromInput: %v", err)
	}

	// Mutate spec-origin tokens only: change values, ensure repeatedly is present.
	// Original rendered: "repeatedly send each of [0, 1] to ToReadGate"
	// Mutate to: "repeatedly send each of [2, 3, 5] to ToReadGate"
	mutated := "repeatedly send each of [2, 3, 5] to ToReadGate"
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

// TestInputRoundTrip_GoEditOnly: mutate pseudo to rename the output field;
// assert spec is deep-equal to fixture, Go has the field renamed in both places.
func TestInputRoundTrip_GoEditOnly(t *testing.T) {
	goSrc := loadInputNodeGo(t)
	spec := fixtureSpec()

	view, err := FromInput(goSrc, spec)
	if err != nil {
		t.Fatalf("FromInput: %v", err)
	}

	// Change output field name from ToReadGate → ToFanout.
	mutated := "repeatedly send each of [0, 1] to ToFanout"
	view2, err := ParseInput(mutated, view)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}

	newGoSrc, newSpec, err := ToInput(view2)
	if err != nil {
		t.Fatalf("ToInput: %v", err)
	}

	// Spec must equal the fixture (unchanged).
	wantSpec := map[string]any{
		"init":   []any{0, 1},
		"repeat": true,
		"x":      "keep-me",
	}
	if !specEqual(newSpec, wantSpec) {
		t.Errorf("spec not preserved after Go-only edit.\nwant: %v\ngot:  %v", wantSpec, newSpec)
	}

	// Go source must contain the new field name.
	newGoStr := string(newGoSrc)
	if !strings.Contains(newGoStr, "ToFanout") {
		t.Errorf("renamed field ToFanout not found in new Go source:\n%s", newGoStr)
	}
	if strings.Contains(newGoStr, "ToReadGate") {
		t.Errorf("old field name ToReadGate still present in new Go source:\n%s", newGoStr)
	}

	// Re-parse the new Go source with FromInput to verify it's structurally valid.
	view3, err := FromInput(newGoSrc, spec)
	if err != nil {
		t.Fatalf("FromInput on renamed source: %v", err)
	}
	if view3.OutputField != "ToFanout" {
		t.Errorf("after re-parse, OutputField = %q; want ToFanout", view3.OutputField)
	}
}

// TestInputParse_RejectsExtraTrailingTokens: trailing tokens must cause error.
func TestInputParse_RejectsExtraTrailingTokens(t *testing.T) {
	prior := InputView{OutputField: "ToReadGate"}
	_, err := ParseInput("send each of [0, 1] to ToReadGate boom", prior)
	if err == nil {
		t.Fatal("expected error for trailing token, got nil")
	}
	t.Logf("got expected error: %v", err)
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
	_, err := FromInput(twoOutputs, spec)
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
