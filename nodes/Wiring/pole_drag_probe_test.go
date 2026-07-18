package Wiring

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestNearPoleDragPersistReload drives a REAL drag of a node to within ~0.006° of the
// scene sphere's +y pole, lets the debounced quant-offset persister flush, reloads the
// tree from disk, and asserts the reloaded world position matches the drop target with
// no drift/blow-up. This probes the SCENE-triple path (the poled (theta,phi) I did NOT
// touch) end-to-end, testing whether a near-pole drag is observably unstable.
func TestNearPoleDragPersistReload(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Scene sphere centered at origin, radius 100 — so the +y pole is world (0,100,0).
	mk("view/scene.json", `{"sceneSphere":{"center":[0,0,0],"radius":100}}`)
	mk("nodes/a/meta.json", `{"id":"a","type":"FanInSrc","scenePolarR":50,"scenePolarTheta":1.2,"scenePolarPhi":0.3}`)
	mk("nodes/a/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/b/meta.json", `{"id":"b","type":"FanInSink","scenePolarR":60,"scenePolarTheta":1.0,"scenePolarPhi":-0.5}`)
	mk("nodes/b/inputs/In.json", `{"name":"In"}`)
	mk("edges/e0.json", `{"label":"e0","kind":"data","source":"a","sourceHandle":"Out","target":"b","targetHandle":"In"}`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := T.New(0)
	_, _, md, err := LoadTopology(ctx, root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)
	md.EnableEditPersist(root)

	// Drag node "a" to within ~0.006 degrees of the +y pole (tiny x/z jitter on a
	// radius-100 offset → angle ≈ atan(0.01/100) ≈ 0.0057°).
	target := vec3{X: 0.01, Y: 100, Z: 0.01}
	if !md.RootMove("a", target) {
		t.Fatal("RootMove returned false")
	}
	// RootMove is async (messages the mover goroutine); wait for the snapshot to settle.
	deadline := time.Now().Add(2 * time.Second)
	var live vec3
	for {
		c, ok := md.centerOfNode("a")
		if ok && c.sub(target).length() <= 1e-9 {
			live = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live position never reached drop target (last=%+v)", c)
		}
		time.Sleep(time.Millisecond)
	}

	// Let the debounced quant-offset persister (250ms) flush to disk, then force it.
	time.Sleep(300 * time.Millisecond)
	md.quantOffsetPersist.flush()

	// Reload the tree from disk into a fresh MoveDispatch.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_, _, md2, err := LoadTopology(ctx2, root, T.New(0), NewRealClock())
	if err != nil {
		t.Fatalf("reload LoadTopology: %v", err)
	}
	reloaded, ok := md2.centerOfNode("a")
	if !ok {
		t.Fatal("centerOfNode(a) missing after reload")
	}
	for _, c := range []float64{reloaded.X, reloaded.Y, reloaded.Z} {
		if math.IsNaN(c) || math.IsInf(c, 0) {
			t.Fatalf("reloaded position non-finite: %+v", reloaded)
		}
	}
	drift := reloaded.sub(target).length()
	t.Logf("near-pole drag: target=%+v live=%+v reloaded=%+v drift=%.3e", target, live, reloaded, drift)
	if drift > 1e-6 {
		t.Fatalf("reloaded position drifted from near-pole drop target by %v — scene-triple pole instability", drift)
	}
}
