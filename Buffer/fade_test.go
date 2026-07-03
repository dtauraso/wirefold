package Buffer

import (
	"sort"
	"testing"
)

// setOf builds a membership set from ids.
func setOf(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestComputeFade ports the pre-branch fade.test.ts fixpoint cases VERBATIM (the 4 rules:
// faded node → incident edges; auto-fade a node with no live edge; single live edge is NOT
// auto-faded; a no-edge node only fades when directly faded; multi-hop cascade to fixpoint).
func TestComputeFade(t *testing.T) {
	t.Run("directly-faded node cascades to its incident edges", func(t *testing.T) {
		fn, fe := computeFade(
			[]string{"A", "B"},
			[]FadeEdge{{ID: "e1", Source: "A", Target: "B"}},
			setOf("A"), setOf())
		if !fn["A"] || !fe["e1"] {
			t.Fatalf("want A + e1 faded, got nodes=%v edges=%v", keys(fn), keys(fe))
		}
	})

	t.Run("auto-fades node B when its last live edge is faded (cascade from A)", func(t *testing.T) {
		fn, _ := computeFade(
			[]string{"A", "B"},
			[]FadeEdge{{ID: "e1", Source: "A", Target: "B"}},
			setOf("A"), setOf())
		if !fn["B"] {
			t.Fatalf("want B auto-faded, got %v", keys(fn))
		}
	})

	t.Run("node with one live edge is NOT auto-faded", func(t *testing.T) {
		fn, _ := computeFade(
			[]string{"A", "B", "C"},
			[]FadeEdge{
				{ID: "e1", Source: "A", Target: "B"},
				{ID: "e2", Source: "A", Target: "C"},
			},
			setOf(), setOf("e1"))
		if !fn["B"] {
			t.Fatalf("B should auto-fade (all edges faded)")
		}
		if fn["C"] {
			t.Fatalf("C should stay unfaded (e2 live)")
		}
		if fn["A"] {
			t.Fatalf("A should stay unfaded (e2 live)")
		}
	})

	t.Run("node with no edges is only faded when directly faded", func(t *testing.T) {
		fn1, _ := computeFade([]string{"lonely"}, nil, setOf(), setOf())
		if fn1["lonely"] {
			t.Fatalf("lonely should not fade without a direct seed")
		}
		fn2, _ := computeFade([]string{"lonely"}, nil, setOf("lonely"), setOf())
		if !fn2["lonely"] {
			t.Fatalf("lonely should fade when directly faded")
		}
	})

	t.Run("unfade: empty seeds restore everything", func(t *testing.T) {
		fn, fe := computeFade(
			[]string{"A", "B"},
			[]FadeEdge{{ID: "e1", Source: "A", Target: "B"}},
			setOf(), setOf())
		if len(fn) != 0 || len(fe) != 0 {
			t.Fatalf("want nothing faded, got nodes=%v edges=%v", keys(fn), keys(fe))
		}
	})

	t.Run("cascade propagates across multiple hops to fixpoint when both ends are faded", func(t *testing.T) {
		fn, fe := computeFade(
			[]string{"A", "B", "C"},
			[]FadeEdge{
				{ID: "e1", Source: "A", Target: "B"},
				{ID: "e2", Source: "B", Target: "C"},
			},
			setOf("A", "C"), setOf())
		for _, n := range []string{"A", "B", "C"} {
			if !fn[n] {
				t.Fatalf("want %s faded, got %v", n, keys(fn))
			}
		}
		if !fe["e1"] || !fe["e2"] {
			t.Fatalf("want e1+e2 faded, got %v", keys(fe))
		}
	})
}
