package Wiring

// pre_split_round_trip_test.go — THE load-bearing test for the one-file-per-goroutine split
//: a topology written in the OLD,
// pre-split on-disk format (meta.json carrying scenePolar/quant/localPolars inline; a single
// shared view/scene.json carrying cameraPolar/overlay-flags/sceneSphere inline — no
// position.json, local-polars.json, camera.json, overlays.json or sphere.json anywhere on
// disk) must still load byte-identically, and a load → drag → save → reload round trip must
// preserve geometry exactly, with the NEW split files now doing the writing.

import (
	"context"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// writePreSplitStar2 lays down a minimal 2-node directory-tree topology using ONLY the
// pre-split on-disk shapes: meta.json holds everything inline, and the shared scene.json
// (not camera.json/overlays.json/sphere.json, which do not exist yet) holds cameraPolar,
// an overlay flag, and sceneSphere all in one document — exactly what a topology saved
// before this split looks like.
func writePreSplitStar2(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) { writeTreeFile(t, root, rel, body) }
	mk("nodes/src/meta.json", `{"id":"src","type":"FanInSrc","r":100,`+
		`"scenePolarR":37.4165738677,"scenePolarTheta":1.00685368543,"scenePolarPhi":1.2490457724,`+
		`"quantITheta":6,"quantIPhi":12,"quantIR":12,`+
		`"stepTheta":0.2617993877991494,"stepPhi":0.2617993877991494,"stepR":20,`+
		`"localPolars":[{"to":"dst","quantITheta":80,"quantIPhi":178,"quantIR":134,`+
		`"stepTheta":0.017453292519943295,"stepPhi":0.017453292519943295,"stepR":2}],`+
		`"localPoleTheta":0,"localPolePhi":0}`)
	mk("nodes/src/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/dst/meta.json", `{"id":"dst","type":"FanInSink","r":100,`+
		`"scenePolarR":87.7496438739,"scenePolarTheta":0.96453035788,"scenePolarPhi":-2.15879893034}`)
	mk("nodes/dst/inputs/In.json", `{"name":"In"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"src","sourceHandle":"Out","target":"dst","targetHandle":"In"}`)
	// The single legacy scene.json — camera + one overlay flag + the scene sphere all
	// sharing one document, the exact pre-split shape.
	mk("view/scene.json", `{`+
		`"cameraPolar":{"pivot":[1,2,3],"r":400,"pos":[0.7,0.8],"up":[0.1,0.2]},`+
		`"sceneToriVisible":false,`+
		`"sceneSphere":{"center":[5,6,7],"radius":123.5}`+
		`}`)
	return root
}

// TestPreSplitTopologyRoundTrips is the load-bearing test: it loads a PRE-SPLIT topology
// (no position.json/local-polars.json/camera.json/overlays.json/sphere.json anywhere on
// disk), drags a node, saves, and reloads — asserting every persisted quantity (node
// geometry, camera pose, overlay flag, scene sphere) round-trips exactly, and that the drag
// landed in the NEW split files rather than mutating the legacy ones in place.
func TestPreSplitTopologyRoundTrips(t *testing.T) {
	root := writePreSplitStar2(t)

	// ---- Load 1: read the pre-split format. ----
	tr := T.New(0)
	_, _, md, _, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology (pre-split): %v", err)
	}
	SeedInitialViewpoint(root, md, nil)
	md.LoadOverlays(root, nil)
	md.LoadSceneSphere(root)
	md.EnableViewpointPersist(root)
	md.EnableEditPersist(root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	srcCenterBefore, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after loading pre-split topology")
	}
	// src's pre-split position is legacy meta.json's inline scenePolar (r=37.4165738677,
	// theta=1.00685368543, phi=1.2490457724). If the reader silently dropped the legacy
	// fallback, src would load HasPos=false and sit at md.sceneSphere.Center instead — this
	// is the check that catches exactly that regression (see the deliberate-failure drill
	// this test's author ran while writing it: removing the legacy-field copy in
	// loader_tree.go turns this into a false pass at md.sceneSphere.Center).
	wantSrcCenter := md.sceneSphere.Center.add(polar2cart(polar{R: 37.4165738677, Theta: 1.00685368543, Phi: 1.2490457724}))
	if d := srcCenterBefore.sub(wantSrcCenter).length(); d > 1e-6 {
		t.Fatalf("src's pre-split legacy meta.json position did not load: got=%+v want=%+v (off by %g)", srcCenterBefore, wantSrcCenter, d)
	}
	dstCenterBefore, ok := md.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after loading pre-split topology")
	}

	// The legacy camera/overlay/sphere fields must have loaded via the fallback.
	if _, r, _, _, ok := loadSceneViewpoint(root); !ok || r != 400 {
		t.Fatalf("legacy cameraPolar did not load via fallback: r=%v ok=%v", r, ok)
	}
	if ov, found := loadSceneOverlays(overlaysFilePath(root), sceneCameraPath(root)); !found || ov.sceneToriVisible {
		t.Fatalf("legacy sceneToriVisible=false did not load via fallback: found=%v ov=%+v", found, ov)
	}
	if s, ok := loadSceneSphere(root); !ok || s.Radius != 123.5 {
		t.Fatalf("legacy sceneSphere did not load via fallback: ok=%v s=%+v", ok, s)
	}

	// ---- Drag src, then save (flush every debounced persister synchronously). ----
	target := srcCenterBefore.add(vec3{X: 40, Y: -25, Z: 10})
	if !md.RootMove("src", target) {
		t.Fatal("RootMove(src) returned false")
	}
	// pollDragConverged only waits for the dragged node's CENTER to publish (applyCenter's
	// atomic snap store) — commitNodeMoveLocal schedules the quantOffset persist write a few
	// lines LATER on that same node-mover goroutine (quantized_move.go), so a single
	// flushPendingPersists() right after convergence can race ahead of that schedule() call
	// and flush an empty pending set. Poll flush+read-back instead of a one-shot flush, the
	// same deadline-bound retry shape pollDragConverged itself uses.
	pollDragConverged(t, md, "src", target)
	pollFlushedPositionFile(t, md, root, "src")

	// ---- Load 2: a completely fresh MoveDispatch over the now-partially-migrated tree. ----
	tr2 := T.New(0)
	_, _, md2, _, err := LoadTopology(context.Background(), root, tr2, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology (reload): %v", err)
	}

	srcCenterAfter, ok := md2.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src after reload")
	}
	if d := srcCenterAfter.sub(target).length(); d > 1e-6 {
		t.Fatalf("src did not round-trip to the drag target: got=%+v want=%+v (off by %g)", srcCenterAfter, target, d)
	}
	dstCenterAfter, ok := md2.centerOfNode("dst")
	if !ok {
		t.Fatal("no center for dst after reload")
	}
	if d := dstCenterAfter.sub(dstCenterBefore).length(); d > 1e-6 {
		t.Fatalf("dst (never dragged) must round-trip unchanged: before=%+v after=%+v", dstCenterBefore, dstCenterAfter)
	}

	// Camera/overlay/sphere still round-trip too (untouched legacy fallback, since this
	// test never persisted a NEW camera/overlay/sphere value).
	if _, r, _, _, ok := loadSceneViewpoint(root); !ok || r != 400 {
		t.Fatalf("camera did not survive the reload: r=%v ok=%v", r, ok)
	}
	if s, ok := loadSceneSphere(root); !ok || s.Radius != 123.5 {
		t.Fatalf("scene sphere did not survive the reload: ok=%v s=%+v", ok, s)
	}
}
