package Wiring

// flush_pending_persists_test.go — pins the clean-shutdown flush that guards against silent
// data loss: a drag that lands within the 250ms debounce window of process exit must still
// reach disk. Without MoveDispatch.flushPendingPersists (wired via `defer` in RunStdinReader),
// a pending debounce timer is simply abandoned when the process exits (stdin EOF), and the
// node reverts to its old position on the next load.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestQuantOffsetFlushPendingWritesBeforeDebounceElapses proves flushPending() writes a
// just-scheduled position update SYNCHRONOUSLY, without waiting for the debounce timer —
// i.e. it is safe to call at shutdown, when there is no time left for the timer to fire.
func TestQuantOffsetFlushPendingWritesBeforeDebounceElapses(t *testing.T) {
	root := writeTree(t)
	p := &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}

	newScene := polar{R: 55.5, Theta: 0.4, Phi: -1.1}
	p.schedule("src", quantizedOffset{iTheta: 3, iPhi: 4, iR: 5}, newScene)

	// Flush immediately — the 250ms debounce has NOT elapsed. If flushPending() merely read
	// disk without cancelling+writing, this would still observe the OLD meta.json values.
	p.flushPending()

	raw, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal meta.json: %v", err)
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
		t.Fatalf("flushPending did not synchronously persist pending drag: got (%v,%v,%v) want (%v,%v,%v)",
			gotR, gotTheta, gotPhi, newScene.R, newScene.Theta, newScene.Phi)
	}
}

// TestMoveDispatchFlushPendingPersistsFlushesQuantOffset exercises the aggregate
// MoveDispatch.flushPendingPersists() (the method RunStdinReader defers on every
// clean-shutdown return path) and confirms it reaches the quant-offset persister.
func TestMoveDispatchFlushPendingPersistsFlushesQuantOffset(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)

	newScene := polar{R: 61.0, Theta: 0.2, Phi: 0.9}
	md.persist.quantOffset.schedule("src", quantizedOffset{iTheta: 1, iPhi: 2, iR: 3}, newScene)

	// Simulate a clean shutdown BEFORE the debounce timer would have fired.
	md.flushPendingPersists()

	raw, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal meta.json: %v", err)
	}
	var gotR float64
	if err := json.Unmarshal(obj["scenePolarR"], &gotR); err != nil {
		t.Fatalf("scenePolarR: %v", err)
	}
	if gotR != newScene.R {
		t.Fatalf("MoveDispatch.flushPendingPersists did not flush the pending quant-offset write: got scenePolarR=%v want %v", gotR, newScene.R)
	}
}

// TestFlushPendingPersistsNilSafe confirms a nil MoveDispatch (and nil-fielded dispatch) do
// not panic — RunStdinReader's callers may include headless/test contexts.
func TestFlushPendingPersistsNilSafe(t *testing.T) {
	var md *MoveDispatch
	md.flushPendingPersists() // must not panic

	md2 := &MoveDispatch{}
	md2.flushPendingPersists() // all persister fields nil — must not panic
}
