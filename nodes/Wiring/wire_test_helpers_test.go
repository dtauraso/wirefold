package Wiring

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeTreeFile writes body to <root>/<rel>, creating any missing parent directories.
// It is the package's single fixture-file writer for directory-tree topology test
// fixtures (nodes/<id>/meta.json, nodes/<id>/inputs|outputs/<name>.json,
// edges/<label>.json); every test that builds an ad hoc tree fixture should call this
// rather than redeclaring its own local mk(rel, body) closure.
func writeTreeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// cascadeSettle is the fixed wall-clock window some drag/neighbor tests sleep after a
// polled convergence, to let any (unwanted) further cascade land before asserting
// absence. It is a widening window, NOT the proof of absence — the proof is an
// SetMsgTap forbidden-kind check (see neighbor_setc_test.go / drag_persist_e2e_test.go);
// a fixed sleep alone can silently pass for the wrong reason under load.
const cascadeSettle = 20 * time.Millisecond

// placeAndDrive places a bead WITHOUT a walker and drives it to delivery on a
// background goroutine that StepOnceAts on a short wall-clock poll, matching the
// production per-cycle StepOnceAt delivery path (no blocking delivery loop). The
// background goroutine owns its own clock copy (docs/planning/visual-editor/
// per-goroutine-clock.md) and re-reads its Tick() each poll, so each StepOnceAt
// observes a later tick until the bead's deadline is crossed and it lands in
// the delivered FIFO.
func placeAndDrive(pw *PacedWire, val int, bp beadPlacement, clk Clock) bool {
	gen, ok := pw.placeBeadNoWalkerAt(val, bp, clk.Tick())
	if !ok {
		return false
	}
	go driveGenToDelivery(pw, gen, clk.Copy())
	return true
}

// driveGenToDelivery repeatedly StepOnceAts pw until the bead identified by gen is
// no longer in flight (delivered or torn down). It polls on a short wall-clock
// sleep; each StepOnceAt reads clk's live Tick(), so the bead advances as real
// time carries the tick forward. clk must be a copy this goroutine owns
// exclusively (see placeAndDrive).
func driveGenToDelivery(pw *PacedWire, gen uint64, clk Clock) {
	ctx := context.Background()
	for {
		pw.mu.Lock()
		idx := pw.findInflightLocked(gen)
		pw.mu.Unlock()
		if idx < 0 {
			return
		}
		pw.StepOnceAt(ctx, clk.Tick())
		time.Sleep(time.Millisecond)
	}
}

// approxEq is the float tolerance used by geometry/position wire tests.
func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
