package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestToggleFadeSelection verifies the "f" command flips the CURRENTLY-SELECTED entity's
// fade seed (node or edge, resolved from Go's own selection) and emits the full seed sets.
func TestToggleFadeSelection(t *testing.T) {
	md := &MoveDispatch{}

	// Nothing selected → no-op (no panic, no seeds).
	md.ToggleFadeSelection(nil)
	if len(md.directlyFadedNodes) != 0 || len(md.directlyFadedEdges) != 0 {
		t.Fatalf("no selection should leave seeds empty")
	}

	// Select a node, toggle on then off.
	md.selected = "N1"
	md.ToggleFadeSelection(nil)
	if !md.directlyFadedNodes["N1"] {
		t.Fatalf("expected N1 in node fade seeds")
	}
	md.ToggleFadeSelection(nil)
	if md.directlyFadedNodes["N1"] {
		t.Fatalf("second toggle should clear N1")
	}

	// Select an edge (clears node selection) and toggle.
	md.selected = ""
	md.selectedEdge = "e1"
	md.ToggleFadeSelection(nil)
	if !md.directlyFadedEdges["e1"] {
		t.Fatalf("expected e1 in edge fade seeds")
	}

	// Emission carries the current seed sets on KindFade.
	tr := T.New(16)
	md.ToggleFadeSelection(tr) // toggles e1 OFF, edges now empty
	md.selectedEdge = ""
	md.selected = "N2"
	md.ToggleFadeSelection(tr) // N2 ON
	tr.Close()
	var last *T.Event
	for i := range tr.Events() {
		if tr.Events()[i].Kind == T.KindFade {
			last = &tr.Events()[i]
		}
	}
	if last == nil {
		t.Fatalf("expected a KindFade event")
	}
	if len(last.FadedNodes) != 1 || last.FadedNodes[0] != "N2" {
		t.Fatalf("last fade nodes = %v, want [N2]", last.FadedNodes)
	}
	if len(last.FadedEdges) != 0 {
		t.Fatalf("last fade edges = %v, want []", last.FadedEdges)
	}
}
