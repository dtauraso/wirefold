// quantized_layout_phase3_test.go — individual snapping: every node is its own root
// (loader.go computeQuantizedLayout) and a drag snaps ONLY the dragged node to the scene
// grid, moving no one else (node_move.go rootMoveQuantized root branch).

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

// waitCenterClose polls md.centerOfNode(id) until it is within tol of want, or fails after
// a bounded budget — the movers apply a RootMove asynchronously (mover goroutines), so a
// freshly-issued drag's center is not necessarily visible the instant RootMove returns.
func waitCenterClose(t *testing.T, md *MoveDispatch, id string, want vec3, tol float64) vec3 {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, ok := md.centerOfNode(id); ok && c.sub(want).length() <= tol {
			return c
		}
		time.Sleep(time.Millisecond)
	}
	c, _ := md.centerOfNode(id)
	t.Fatalf("center of %s never reached %v (last %v, tol %v)", id, want, c, tol)
	return vec3{}
}

func writeQuantTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	// Nodes carry a scenePolar so they load with a real position (individual snapping keeps
	// each exactly where it loaded until dragged).
	mk("nodes/0/meta.json", `{"id":"0","type":"FanInSrc","scenePolarR":40,"scenePolarTheta":1.2,"scenePolarPhi":0.3}`)
	mk("nodes/0/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/1/meta.json", `{"id":"1","type":"AimedPacer","scenePolarR":80,"scenePolarTheta":1.0,"scenePolarPhi":0.5}`)
	mk("nodes/1/inputs/FromSrc.json", `{"name":"FromSrc"}`)
	mk("nodes/1/outputs/Feedback.json", `{"name":"Feedback"}`)
	mk("nodes/2/meta.json", `{"id":"2","type":"FanInSink","scenePolarR":120,"scenePolarTheta":0.8,"scenePolarPhi":-0.4}`)
	mk("nodes/2/inputs/In.json", `{"name":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("edges/e1.json", `{"label":"e1","kind":"data","source":"0","sourceHandle":"Out","target":"1","targetHandle":"FromSrc"}`)
	mk("edges/e2.json", `{"label":"e2","kind":"data","source":"1","sourceHandle":"Feedback","target":"2","targetHandle":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "view", "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDragSnapsToGridIndividually: dragging node "1" to an arbitrary world target snaps it
// to the scene-sphere grid (r,θ,φ on the grid about the scene center) and moves ONLY "1" —
// its edge-neighbor "2" stays exactly where it was (no chain, no subtree).
func TestDragSnapsToGridIndividually(t *testing.T) {
	root := writeQuantTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, md, err := LoadTopology(ctx, root, T.New(256), NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)

	twoBefore, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("2 has no center before drag")
	}

	target := vec3{X: 330, Y: 510, Z: -200}
	if !md.RootMove("1", target) {
		t.Fatal("RootMove(1) returned false")
	}

	// "1" landed on the grid: r,θ,φ about the scene center are integer multiples of the steps.
	p := cart2polar(target.sub(md.sceneSphere.Center))
	want := md.sceneSphere.Center.add(polar2cart(polar{
		R:     math.Round(p.R/stepR) * stepR,
		Theta: math.Round(p.Theta/stepTheta) * stepTheta,
		Phi:   math.Round(p.Phi/stepPhi) * stepPhi,
	}))
	waitCenterClose(t, md, "1", want, 1e-6)

	// "2" did not move — individual snapping, no subtree.
	twoAfter, _ := md.centerOfNode("2")
	if twoAfter.sub(twoBefore).length() > 1e-9 {
		t.Fatalf("2 moved when only 1 was dragged: before=%v after=%v", twoBefore, twoAfter)
	}
}

// TestLoadIsIndividualRoots: every node loads as its own root (no parent), positioned at
// its scenePolar — no spanning tree, no chained compose.
func TestLoadIsIndividualRoots(t *testing.T) {
	root := writeQuantTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, md, err := LoadTopology(ctx, root, T.New(64), NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	for _, id := range []string{"0", "1", "2"} {
		if off := md.quantizedOffsets[id]; off.parent != "" {
			t.Fatalf("node %s has parent %q — individual snapping means all roots", id, off.parent)
		}
	}
	want := md.sceneSphere.Center.add(polar2cart(polar{R: 80, Theta: 1.0, Phi: 0.5}))
	if c, _ := md.centerOfNode("1"); c.sub(want).length() > 1e-6 {
		t.Fatalf("node 1 not at its loaded scenePolar: got %v want %v", c, want)
	}
}
