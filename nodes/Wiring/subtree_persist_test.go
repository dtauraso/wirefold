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

// pollLocalPolarRequantized waits until lh's own stored LocalPolar entry to the given
// neighbor id picks up a fresh QuantIR (async moveMsgKindNeighborSetC delivery race) and
// returns the converged entry. Shared by every test in this package that drives a real
// drag and then waits for a neighbor's re-quantize to land, rather than re-deriving the
// same deadline-bounded poll loop at each call site.
func pollLocalPolarRequantized(t *testing.T, lh *LayoutHolder, to string, before LocalPolar) LocalPolar {
	t.Helper()
	var after LocalPolar
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, lp := range lh.LocalPolarsSnapshot() {
			if lp.To == to {
				after = lp
			}
		}
		if after.QuantIR != before.QuantIR {
			return after
		}
		if time.Now().After(deadline) {
			t.Fatalf("local polar to %q never picked up the requantize: before=%+v after=%+v", to, before, after)
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
	srcCenterBefore, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src before drag")
	}

	dstTarget := vec3{X: 60, Y: 20, Z: -10}
	if !md.RootMove("dst", dstTarget) {
		t.Fatal("RootMove(dst) returned false")
	}
	pollDragConverged(t, md, "dst", dstTarget)

	// src is dst's plain (role-free) direct neighbor: per the single-assignment set-c
	// REQUANTIZE model (node_move.go moveMsgKindNeighborSetC / neighborSetCRequantize),
	// src STAYS PUT — only dst moved — and re-quantizes its OWN stored local polar
	// (QuantITheta/QuantIPhi/QuantIR) to dst fresh from the live offset. Poll for src's
	// own LocalPolar entry's QuantIR to change (the async moveMsgKindNeighborSetC
	// delivery race, same shape as rotating_pole_test.go's polls).
	lpAfter := pollLocalPolarRequantized(t, lhSrc, "dst", lpBefore)

	// src's own world center must NOT have moved — only dst moved.
	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after drag")
	}
	if d := srcCenter.sub(srcCenterBefore).length(); d > 1e-9 {
		t.Fatalf("src must stay put on a dst drag: before=%+v after=%+v (moved by %g)", srcCenterBefore, srcCenter, d)
	}

	// src's requantized local polar to dst must match a fresh quantization of the live
	// offset (dst_newcenter - src_center) about src's own pole.
	dstCenter, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after drag")
	}
	offset := dstCenter.sub(srcCenter)
	dd, rr := dirFromOffset(offset)
	cc, psi := azimuthFrom(lhSrc.Pole(), dd)
	st, sp, sr := lpAfter.effectiveSteps()
	wantTheta := int(math.Round(cc / st))
	wantPhi := int(math.Round(psi / sp))
	wantR := int(math.Round(rr / sr))
	if lpAfter.QuantITheta != wantTheta || lpAfter.QuantIPhi != wantPhi || lpAfter.QuantIR != wantR {
		t.Fatalf("src's requantized local polar to dst should match a fresh quantization of the live offset: got=(theta=%d,phi=%d,r=%d) want=(theta=%d,phi=%d,r=%d)",
			lpAfter.QuantITheta, lpAfter.QuantIPhi, lpAfter.QuantIR, wantTheta, wantPhi, wantR)
	}

	md.persist.quantOffset.flush()

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

	// src's persisted meta.json must reflect its UNCHANGED scene-polar position: under
	// the single-assignment set-c REQUANTIZE model, a dst drag never moves src — only
	// src's local-polar edge to dst (its own requantized bearing/distance) changes, not
	// its own scene position. Assert src's persisted scenePolar still matches its
	// pre-drag world center.
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
			t.Fatalf("src %s not persisted: %s", k, srcAfter)
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
	if d := gotCenter.sub(srcCenterBefore).length(); d > 1e-6 {
		t.Fatalf("src's persisted scenePolar should still match its pre-drag (unmoved) world center: persisted=%+v pre-drag=%+v", gotCenter, srcCenterBefore)
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
	md.persist.quantOffset.flush()

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
