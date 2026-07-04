// lock_chain_propagation_test.go — transitive (multi-hop) colinearity lock propagation:
// a chain of two equations sharing a middle node must fully resolve in RootMove, not just
// the equation directly touching the dragged node.

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

// chainTestMD loads a center "c" with three satellites a, b, d and starts the movers.
// Returns md plus a waitCenter helper (mirrors locks_apply_integration_test.go).
func chainTestMD(t *testing.T) (*MoveDispatch, func(id string, want vec3)) {
	t.Helper()
	const topo = `{
	  "nodes": [
	    {"id":"c","type":"FanInSink","inputs":[{"name":"In"}]},
	    {"id":"a","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"b","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"d","type":"FanInSrc","outputs":[{"name":"Out"}]}
	  ],
	  "edges": [
	    {"label":"ea","kind":"data","source":"a","sourceHandle":"Out","target":"c","targetHandle":"In"},
	    {"label":"eb","kind":"data","source":"b","sourceHandle":"Out","target":"c","targetHandle":"In"},
	    {"label":"ed","kind":"data","source":"d","sourceHandle":"Out","target":"c","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "c": {"x": 0,  "y": 0,  "z": 0},
	    "a": {"x": 10, "y": 10, "z": 0},
	    "b": {"x": 10, "y": 2,  "z": 0},
	    "d": {"x": 10, "y": -6, "z": 0}
	  }}
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tr := T.New(1024)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)

	waitCenter := func(id string, want vec3) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if c, ok := md.centerOfNode(id); ok && c.sub(want).length() < 1e-6 {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		got, _ := md.centerOfNode(id)
		t.Fatalf("node %s never reached %v (last %v)", id, want, got)
	}

	md.RootMove("c", vec3{0, 0, 0})
	md.RootMove("a", vec3{10, 10, 0})
	md.RootMove("b", vec3{10, 2, 0})
	md.RootMove("d", vec3{10, -6, 0})
	waitCenter("c", vec3{0, 0, 0})
	waitCenter("a", vec3{10, 10, 0})
	waitCenter("b", vec3{10, 2, 0})
	waitCenter("d", vec3{10, -6, 0})
	return md, waitCenter
}

func thetaAboutC(md *MoveDispatch, node string) float64 {
	c, _ := md.centerOfNode("c")
	p, _ := md.centerOfNode(node)
	return cart2polar(p.sub(c)).Theta
}

// TestChainPropagationMovingEndpoint: chain a—eq—b—eq—d about c. Dragging the ENDPOINT a
// must transitively re-solve through b to reach d (the second hop), not just b.
func TestChainPropagationMovingEndpoint(t *testing.T) {
	md, waitCenter := chainTestMD(t)

	eqAB := polarEq{
		Center: "c",
		A:      polarTerm{Node: "a", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		Active: true,
	}
	eqBD := polarEq{
		Center: "c",
		A:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "d", Comp: compTheta, Sign: 1},
		Active: true,
	}
	md.polarEqs = append(md.polarEqs, eqAB, eqBD)
	md.ensureEqLinks(eqAB)
	md.ensureEqLinks(eqBD)

	thetaBBefore := thetaAboutC(md, "b")
	thetaDBefore := thetaAboutC(md, "d")

	// Drag the endpoint "a" to a new θ. b must snap to match (direct hop), and d must
	// ALSO snap to match b's new θ (the transitive second hop) — the bug this test guards.
	target := vec3{10, 20, 0}
	md.RootMove("a", target)
	waitCenter("a", target)

	thetaA := thetaAboutC(md, "a")

	deadline := time.Now().Add(500 * time.Millisecond)
	var thetaB, thetaD float64
	for {
		thetaB = thetaAboutC(md, "b")
		thetaD = thetaAboutC(md, "d")
		if math.Abs(thetaB-thetaA) <= 1e-3 && math.Abs(thetaD-thetaA) <= 1e-3 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if math.Abs(thetaB-thetaA) > 1e-3 {
		t.Fatalf("direct hop failed: θ(b)=%.4f, want θ(a)=%.4f (before=%.4f)", thetaB, thetaA, thetaBBefore)
	}
	if math.Abs(thetaD-thetaA) > 1e-3 {
		t.Fatalf("transitive (second) hop failed: θ(d)=%.4f, want θ(a)=%.4f (before=%.4f)", thetaD, thetaA, thetaDBefore)
	}
}

// TestChainPropagationMovingMiddle: dragging the MIDDLE node b of the chain must resolve
// both a and d in the same frame.
func TestChainPropagationMovingMiddle(t *testing.T) {
	md, waitCenter := chainTestMD(t)

	eqAB := polarEq{
		Center: "c",
		A:      polarTerm{Node: "a", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		Active: true,
	}
	eqBD := polarEq{
		Center: "c",
		A:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "d", Comp: compTheta, Sign: 1},
		Active: true,
	}
	md.polarEqs = append(md.polarEqs, eqAB, eqBD)
	md.ensureEqLinks(eqAB)
	md.ensureEqLinks(eqBD)

	target := vec3{10, 30, 0}
	md.RootMove("b", target)
	waitCenter("b", target)
	thetaB := thetaAboutC(md, "b")

	deadline := time.Now().Add(500 * time.Millisecond)
	var thetaA, thetaD float64
	for {
		thetaA = thetaAboutC(md, "a")
		thetaD = thetaAboutC(md, "d")
		if math.Abs(thetaA-thetaB) <= 1e-3 && math.Abs(thetaD-thetaB) <= 1e-3 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if math.Abs(thetaA-thetaB) > 1e-3 {
		t.Fatalf("θ(a)=%.4f, want θ(b)=%.4f", thetaA, thetaB)
	}
	if math.Abs(thetaD-thetaB) > 1e-3 {
		t.Fatalf("θ(d)=%.4f, want θ(b)=%.4f", thetaD, thetaB)
	}
}

// TestChainPropagationPinsDraggedNode: propagation must never overwrite the dragged node's
// own final center — even though b's equations reference a on one side.
func TestChainPropagationPinsDraggedNode(t *testing.T) {
	md, waitCenter := chainTestMD(t)

	eqAB := polarEq{
		Center: "c",
		A:      polarTerm{Node: "a", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		Active: true,
	}
	eqBD := polarEq{
		Center: "c",
		A:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "d", Comp: compTheta, Sign: 1},
		Active: true,
	}
	md.polarEqs = append(md.polarEqs, eqAB, eqBD)
	md.ensureEqLinks(eqAB)
	md.ensureEqLinks(eqBD)

	target := vec3{10, 20, 0}
	md.RootMove("b", target)
	waitCenter("b", target)

	if got, ok := md.centerOfNode("b"); !ok || got.sub(target).length() > 1e-6 {
		t.Fatalf("dragged node b center=%v, want pinned at target=%v", got, target)
	}
}

// TestChainPropagationCycleTerminates: a cycle of equations (a==b, b==d, d==a) must not
// hang RootMove — the iteration cap + eps gate must terminate it, and every resulting
// center must be finite.
func TestChainPropagationCycleTerminates(t *testing.T) {
	md, waitCenter := chainTestMD(t)

	eqAB := polarEq{Center: "c", A: polarTerm{Node: "a", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "b", Comp: compTheta, Sign: 1}, Active: true}
	eqBD := polarEq{Center: "c", A: polarTerm{Node: "b", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "d", Comp: compTheta, Sign: 1}, Active: true}
	eqDA := polarEq{Center: "c", A: polarTerm{Node: "d", Comp: compTheta, Sign: 1}, B: polarTerm{Node: "a", Comp: compTheta, Sign: 1}, Active: true}
	md.polarEqs = append(md.polarEqs, eqAB, eqBD, eqDA)
	md.ensureEqLinks(eqAB)
	md.ensureEqLinks(eqBD)
	md.ensureEqLinks(eqDA)

	done := make(chan bool, 1)
	target := vec3{10, 25, 0}
	go func() {
		md.RootMove("a", target)
		done <- true
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RootMove did not return: cycle-safety worklist cap failed to terminate")
	}
	waitCenter("a", target)

	for _, id := range []string{"a", "b", "d"} {
		c, ok := md.centerOfNode(id)
		if !ok {
			t.Fatalf("node %s has no center after cyclic RootMove", id)
		}
		if math.IsNaN(c.X) || math.IsNaN(c.Y) || math.IsNaN(c.Z) ||
			math.IsInf(c.X, 0) || math.IsInf(c.Y, 0) || math.IsInf(c.Z, 0) {
			t.Fatalf("node %s center=%v is not finite after cyclic RootMove", id, c)
		}
	}
}

// TestChainPropagationNoOpWithoutEquations: moving a node with no equations referencing it
// only fans itself — emit must not spread to unrelated nodes.
func TestChainPropagationNoOpWithoutEquations(t *testing.T) {
	md, waitCenter := chainTestMD(t)
	// No polarEqs registered at all.

	before := map[string]vec3{}
	for _, id := range []string{"a", "b", "d"} {
		before[id], _ = md.centerOfNode(id)
	}

	target := vec3{10, 2, 5}
	md.RootMove("b", target)
	waitCenter("b", target)

	for _, id := range []string{"a", "d"} {
		got, _ := md.centerOfNode(id)
		if got.sub(before[id]).length() > 1e-6 {
			t.Fatalf("no-op move of b changed unrelated node %s: before=%v after=%v", id, before[id], got)
		}
	}
}
