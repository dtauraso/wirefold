package Wiring

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pollDragConverged waits until the named node's committed center matches target — a
// drag now always runs asynchronously on the node's OWN mover goroutine (moveMsgKindDrag,
// node6-drag-decentralized.md generalized to every node), so RootMove returning true only
// means the message was ENQUEUED, not that commitLocal (and its quantOffsetPersist.schedule
// call) has run yet. Tests that read persisted state right after RootMove must wait for
// this convergence first, exactly as the node_move_test.go cascade tests already do.
func pollDragConverged(t *testing.T, md *MoveDispatch, nodeID string, target vec3) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, ok := md.centerOfNode(nodeID)
		if ok && math.Abs(c.X-target.X) <= eps && math.Abs(c.Y-target.Y) <= eps && math.Abs(c.Z-target.Z) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node %s drag never converged to target %+v", nodeID, target)
		}
		time.Sleep(time.Millisecond)
	}
}

// Individual snapping: dragging a node moves and persists ONLY that node (its grid-snapped
// scalar triple, quantITheta/quantIPhi/quantIR — the sole persisted position source under
// the plain-polar model), leaving every other node untouched — no subtree cascade.
func TestIndividualSnap_OnlyDraggedNodePersists(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	// Every drag (moveMsgKindDrag, node6-drag-decentralized.md generalized to every
	// node) commits on the dragged node's OWN mover goroutine — Start the movers so
	// something drains dst's inbox.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	lhSrc, ok := md.layoutHolders["src"]
	if !ok {
		t.Fatal("no LayoutHolder for src")
	}
	var lpBefore LocalPolar
	for _, lp := range lhSrc.LocalPolarsSnapshot() {
		if lp.To == "dst" {
			lpBefore = lp
		}
	}

	dstTarget := vec3{X: 60, Y: 20, Z: -10}
	if !md.RootMove("dst", dstTarget) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", dstTarget)

	// src is dst's plain (role-free) direct neighbor: per the single-assignment set-c
	// model (node_move.go moveMsgKindNeighborSetC / neighborSetCReposition), src keeps
	// its OWN stored bearing (QuantITheta/QuantIPhi) to dst and is REPOSITIONED — it
	// slides along that unchanged viewing direction to the new (quantized) distance,
	// dst held fixed. Poll for src's own LocalPolar entry's QuantIR to change (the
	// async moveMsgKindNeighborSetC delivery race, same shape as
	// rotating_pole_test.go's moveMsgKindRequantize polls).
	var lpAfter LocalPolar
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, lp := range lhSrc.LocalPolarsSnapshot() {
			if lp.To == "dst" {
				lpAfter = lp
			}
		}
		if lpAfter.QuantIR != lpBefore.QuantIR {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("src's local polar to dst never picked up the new set-c: before=%+v after=%+v", lpBefore, lpAfter)
		}
		time.Sleep(time.Millisecond)
	}
	if lpAfter.QuantITheta != lpBefore.QuantITheta || lpAfter.QuantIPhi != lpBefore.QuantIPhi {
		t.Fatalf("src's stored bearing to dst must be KEPT exactly (single-assignment set-c, no re-derivation): before=%+v after=%+v", lpBefore, lpAfter)
	}

	// src's own world center must have followed: dst's fresh center minus the KEPT
	// bearing direction scaled to the new radius.
	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after drag")
	}
	st, sp, sr := lpAfter.effectiveSteps()
	wantDir := fromAxisFrame(lhSrc.Pole(), float64(lpAfter.QuantITheta)*st, float64(lpAfter.QuantIPhi)*sp)
	wantCenter := dstCenter.sub(dirToVec3(wantDir).scale(float64(lpAfter.QuantIR) * sr))
	if d := srcCenter.sub(wantCenter).length(); d > 1e-6 {
		t.Fatalf("src did not reposition to dst_center - dir(kept bearing)*newR: got=%+v want=%+v (off by %g)", srcCenter, wantCenter, d)
	}

	md.quantOffsetPersist.flush()

	// dst's meta got its EXACT scene-polar position (the lossless source of truth loaded
	// verbatim on reload) plus the quantized scalar triple as a self-describing cache; src
	// is byte-for-byte unchanged.
	dstRaw, err := os.ReadFile(filepath.Join(root, "nodes", "dst", "meta.json"))
	if err != nil {
		t.Fatalf("read dst meta: %v", err)
	}
	var dst map[string]json.RawMessage
	_ = json.Unmarshal(dstRaw, &dst)
	for _, k := range []string{"scenePolarR", "scenePolarTheta", "scenePolarPhi"} {
		if _, ok := dst[k]; !ok {
			t.Fatalf("dst %s not persisted (exact position is the source of truth): %s", k, dstRaw)
		}
	}
	if _, ok := dst["quantITheta"]; !ok {
		t.Fatalf("dst quantITheta cache not persisted: %s", dstRaw)
	}
	if _, ok := dst["quantIR"]; !ok {
		t.Fatalf("dst quantIR cache not persisted: %s", dstRaw)
	}

	// src's persisted meta.json must reflect its NEW repositioned scene-polar (the
	// plain-neighbor set-c model intentionally moves a direct neighbor on a drag of the
	// node it's attached to — the individual-snap invariant this test used to assert
	// applied only under the retired re-derive-only requantize; it does not survive the
	// single-assignment set-c redraw model). Assert src's persisted scenePolar matches
	// its live (repositioned) world center, not that it is untouched.
	srcAfter, err := os.ReadFile(filepath.Join(root, "nodes", "src", "meta.json"))
	if err != nil {
		t.Fatalf("read src meta: %v", err)
	}
	var srcA map[string]json.RawMessage
	if err := json.Unmarshal(srcAfter, &srcA); err != nil {
		t.Fatalf("unmarshal src after: %v", err)
	}
	for _, k := range []string{"scenePolarR", "scenePolarTheta", "scenePolarPhi"} {
		if _, ok := srcA[k]; !ok {
			t.Fatalf("src %s not persisted after repositioning: %s", k, srcAfter)
		}
	}
	var gotP polar
	if err := json.Unmarshal(srcA["scenePolarR"], &gotP.R); err != nil {
		t.Fatalf("unmarshal src scenePolarR: %v", err)
	}
	if err := json.Unmarshal(srcA["scenePolarTheta"], &gotP.Theta); err != nil {
		t.Fatalf("unmarshal src scenePolarTheta: %v", err)
	}
	if err := json.Unmarshal(srcA["scenePolarPhi"], &gotP.Phi); err != nil {
		t.Fatalf("unmarshal src scenePolarPhi: %v", err)
	}
	gotCenter := md.sceneSphere.Center.add(polar2cart(gotP))
	if d := gotCenter.sub(srcCenter).length(); d > 1e-6 {
		t.Fatalf("src's persisted scenePolar does not match its repositioned world center: persisted=%+v live=%+v", gotCenter, srcCenter)
	}
}

// TestDragPositionRoundTripsExactly: dragging a node to an arbitrary continuous target,
// persisting, and RELOADING from disk must place the node at EXACTLY that target — the
// exact scene-polar position is the lossless source of truth (not the coarse quantized
// triple, which would round the drag away).
func TestDragPositionRoundTripsExactly(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	md.EnableEditPersist(root)
	// Every drag commits on the dragged node's OWN mover goroutine — Start the movers
	// so something drains dst's inbox (node6-drag-decentralized.md, generalized).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	target := vec3{X: 63.7, Y: -21.3, Z: 44.9}
	if !md.RootMove("dst", target) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", target)
	md.quantOffsetPersist.flush()

	// Reload from disk into a fresh MoveDispatch and read dst's center back.
	md2 := loadTreeMD(t, root)
	got, ok := md2.centerOfNode("dst")
	if !ok {
		t.Fatal("dst missing after reload")
	}
	const eps = 1e-6
	if d := got.sub(target).length(); d > eps {
		t.Fatalf("dst did not round-trip: dragged to %+v, reloaded at %+v (off by %g)", target, got, d)
	}
}
