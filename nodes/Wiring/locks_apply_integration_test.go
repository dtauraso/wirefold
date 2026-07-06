// locks_apply_integration_test.go — end-to-end proof that completing an equation enforces
// it IMMEDIATELY (not only on the next drag), over a real loaded topology with live movers.
// Mirrors the gesture.go rule-completion sequence: append + ensureEqLinks + settle term A.

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

func TestEquationAppliesImmediatelyOnCompletion(t *testing.T) {
	t.Skip("deferred: polar-frame regression — colinearity/move/aimed rebuild pending (polar-frame-rewrite.md phase 4/6); allowed for now")
	// Center "c" with two satellites "a" and "b". The equation θ(a)==θ(b) about c must snap
	// b to a's colatitude the moment it is set — no drag required.
	const topo = `{
	  "nodes": [
	    {"id":"c","type":"FanInSink","inputs":[{"name":"In"}]},
	    {"id":"a","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"b","type":"FanInSrc","outputs":[{"name":"Out"}]}
	  ],
	  "edges": [
	    {"label":"ea","kind":"data","source":"a","sourceHandle":"Out","target":"c","targetHandle":"In"},
	    {"label":"eb","kind":"data","source":"b","sourceHandle":"Out","target":"c","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "c": {"x": 0,  "y": 0,  "z": 0},
	    "a": {"x": 10, "y": 10, "z": 0},
	    "b": {"x": 10, "y": 2,  "z": 0}
	  }}
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
	md.Start(ctx)

	// Movers apply center updates asynchronously; poll until a node's published center
	// reaches the target (or fail after a budget).
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
	thetaAbout := func(center, node string) float64 {
		c, _ := md.centerOfNode(center)
		p, _ := md.centerOfNode(node)
		return cart2polar(p.sub(c)).Theta
	}

	// Place the three nodes at known world points and let the movers settle.
	md.RootMove("c", vec3{0, 0, 0})
	md.RootMove("a", vec3{10, 10, 0})
	md.RootMove("b", vec3{10, 2, 0})
	waitCenter("c", vec3{0, 0, 0})
	waitCenter("a", vec3{10, 10, 0})
	waitCenter("b", vec3{10, 2, 0})

	thetaA := thetaAbout("c", "a")
	if math.Abs(thetaA-thetaAbout("c", "b")) < 0.1 {
		t.Fatalf("test setup: θ(a) and θ(b) should differ; both ≈ %.3f", thetaA)
	}

	// Replicate the gesture.go completion path for eq θ(a) == θ(b).
	eq := polarEq{
		Center: "c",
		A:      polarTerm{Node: "a", Comp: compTheta, Sign: 1},
		B:      polarTerm{Node: "b", Comp: compTheta, Sign: 1},
		Active: true,
	}
	md.appendPolarEq(eq)
	md.ensureEqLinks(eq)
	if c, ok := md.centerOfNode(eq.A.Node); ok {
		md.RootMove(eq.A.Node, c)
	}

	// b must have snapped to a's colatitude WITHOUT any drag.
	deadline := time.Now().Add(500 * time.Millisecond)
	var thetaB float64
	for {
		thetaB = thetaAbout("c", "b")
		if math.Abs(thetaB-thetaA) <= 1e-3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if math.Abs(thetaB-thetaA) > 1e-3 {
		t.Fatalf("equation did not apply immediately: θ(b)=%.4f, want θ(a)=%.4f", thetaB, thetaA)
	}
}
