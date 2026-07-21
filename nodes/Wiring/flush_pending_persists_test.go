package Wiring

// flush_pending_persists_test.go — pins that a quant-offset persist write lands on disk
// SYNCHRONOUSLY, with no clean-shutdown flush needed to guard against loss.
//
// This used to pin MoveDispatch.flushPendingPersists, a `defer` in RunStdinReader that
// flushed every persister's pending debounced value on process exit — without it, a drag
// landing within the 250ms debounce window of exit was silently abandoned. That whole
// mechanism (debouncedPersister, flushPending, flushPendingPersists) was removed: each
// persister now writes the moment its value changes, so there is nothing left pending at
// exit to lose. See scene_persist.go's header comment for the reasoning.

import (
	"encoding/json"
	"os"
	"testing"
)

// TestQuantOffsetScheduleWritesSynchronously proves schedule() writes a position update
// to disk immediately, with no timer/flush step required.
func TestQuantOffsetScheduleWritesSynchronously(t *testing.T) {
	root := writeTree(t)
	p := &quantOffsetPersister{root: root}

	newScene := polar{R: 55.5, Theta: 0.4, Phi: -1.1}
	p.schedule("src", quantizedOffset{iTheta: 3, iPhi: 4, iR: 5}, newScene)

	raw, err := os.ReadFile(positionFilePath(root, "src"))
	if err != nil {
		t.Fatalf("read position.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal position.json: %v", err)
	}
	var gotR, gotTheta, gotPhi float64
	if err := json.Unmarshal(obj["scenePolarR"], &gotR); err != nil {
		t.Fatalf("scenePolarR: %v", err)
	}
	if err := json.Unmarshal(obj["scenePolarTheta"], &gotTheta); err != nil {
		t.Fatalf("scenePolarTheta: %v", err)
	}
	if err := json.Unmarshal(obj["scenePolarPhi"], &gotPhi); err != nil {
		t.Fatalf("scenePolarPhi: %v", err)
	}
	if gotR != newScene.R || gotTheta != newScene.Theta || gotPhi != newScene.Phi {
		t.Fatalf("schedule did not synchronously persist the drag: got (%v,%v,%v) want (%v,%v,%v)",
			gotR, gotTheta, gotPhi, newScene.R, newScene.Theta, newScene.Phi)
	}
	if p.writes != 1 {
		t.Fatalf("writes=%d want 1", p.writes)
	}
}

// TestMoveDispatchQuantOffsetScheduleWritesThroughEnableEditPersist exercises the
// MoveDispatch-owned persister (the path EnableEditPersist wires up) and confirms it
// reaches disk.
func TestMoveDispatchQuantOffsetScheduleWritesThroughEnableEditPersist(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	newScene := polar{R: 61.0, Theta: 0.2, Phi: 0.9}
	md.persist.quantOffset.schedule("src", quantizedOffset{iTheta: 1, iPhi: 2, iR: 3}, newScene)

	raw, err := os.ReadFile(positionFilePath(root, "src"))
	if err != nil {
		t.Fatalf("read position.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal position.json: %v", err)
	}
	var gotR float64
	if err := json.Unmarshal(obj["scenePolarR"], &gotR); err != nil {
		t.Fatalf("scenePolarR: %v", err)
	}
	if gotR != newScene.R {
		t.Fatalf("quant-offset schedule did not write: got scenePolarR=%v want %v", gotR, newScene.R)
	}
}

// TestQuantOffsetScheduleNilSafe confirms a nil-rooted (unarmed) persister does not panic —
// tests/headless contexts construct a MoveDispatch without EnableEditPersist.
func TestQuantOffsetScheduleNilSafe(t *testing.T) {
	var p *quantOffsetPersister
	p.schedule("x", quantizedOffset{}, polar{}) // must not panic

	p2 := &quantOffsetPersister{}                // root == "" — unarmed
	p2.schedule("x", quantizedOffset{}, polar{}) // must not panic
}
