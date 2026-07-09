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

// TestLockCompRAboutOffCenterNode is the regression guard for the eqNodeNode compR fix:
// `(n2,r) = (n3,r)` about Center "n1" must equalize |n2-n1| == |n3-n1| (edge length about
// n1), NOT scene-R (distance from the scene sphere's origin) — those only agree when n1
// sits at the scene center. Here n1 is placed away from the scene origin so the two
// notions diverge; before the fix, dragging n2 would copy n2's scene-R onto n3's scene-R,
// leaving n3's distance to n1 unequal to n2's distance to n1.
func TestLockCompRAboutOffCenterNode(t *testing.T) {
	// n1 off the scene origin; n2, n3 elsewhere. sceneSphere defaults to origin/0 radius
	// (no scene.json alongside topo.json), so scene-R == distance-from-origin.
	const topo = `{
	  "nodes": [
	    {"id":"n1","type":"AimedSink","scenePolarR":30,"scenePolarTheta":1.0,"scenePolarPhi":0.3,"inputs":[{"name":"In"}]},
	    {"id":"n2","type":"AimedSink","scenePolarR":60,"scenePolarTheta":1.2,"scenePolarPhi":0.5,"inputs":[{"name":"In"}]},
	    {"id":"n3","type":"AimedSink","scenePolarR":45,"scenePolarTheta":0.8,"scenePolarPhi":-0.4,"inputs":[{"name":"In"}]}
	  ],
	  "edges": []
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(1024)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Phase 3: exercise the free-drag lock cascade, not quantized-layout compose.
	md.quantizedLayout = false
	md.setPolarEqs([]polarEq{
		{Center: "n1", A: polarTerm{"n2", compR, 1}, B: polarTerm{"n3", compR, 1}, Active: true},
	})
	md.Start(ctx)

	n1Before, ok := md.centerOfNode("n1")
	if !ok {
		t.Fatalf("n1 has no center before move")
	}
	n3DirBefore, ok := md.centerOfNode("n3")
	if !ok {
		t.Fatalf("n3 has no center before move")
	}
	dirBefore := n3DirBefore.sub(n1Before).normalize()

	// Drag n2 to a new position (still away from n1) and let the cascade settle.
	md.RootMove("n2", vec3{X: 20, Y: 55, Z: -10})
	time.Sleep(80 * time.Millisecond)

	n1, ok := md.centerOfNode("n1")
	if !ok {
		t.Fatalf("n1 has no center after move")
	}
	n2, ok := md.centerOfNode("n2")
	if !ok {
		t.Fatalf("n2 has no center after move")
	}
	n3, ok := md.centerOfNode("n3")
	if !ok {
		t.Fatalf("n3 has no center after move")
	}

	distN2 := n2.sub(n1).length()
	distN3 := n3.sub(n1).length()
	if math.Abs(distN2-distN3) > 1e-6 {
		t.Fatalf("edge length about n1 not equalized: |n2-n1|=%g |n3-n1|=%g (diff=%g)", distN2, distN3, distN2-distN3)
	}

	// Direction from n1 to n3 must be preserved (radial move only), not replaced by a
	// direction implied by copying n2's scene-R.
	dirAfter := n3.sub(n1).normalize()
	dot := dirAfter.dot(dirBefore)
	if dot < 1-1e-6 {
		t.Fatalf("n3's direction from n1 changed: dot=%g (want ~1)", dot)
	}
}
