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

// TestPolarLockNoBlowup reproduces the real-scene fly-away structure IN-PACKAGE: node "n2"
// is simultaneously the CENTER of the (n5,φ)=(n6,−φ) lock AND a term of the center-"c1"
// locks (with n3), and n6 is torus-coupled to n3 (edge n6→n3). Dragging n5 propagates
// n5→n6 (polar about n2) → n6→n3 (torus z) → n3→n2 (polar about c1), moving n2 — the very
// center n6 is measured from. On the CURRENT cart2polar cascade this compounds to ~1e11;
// with stored local polar (the offset is carried, not re-derived from a moving center) it
// must stay bounded and settle. This is the permanent guard for "panel locks never fly away".
func TestPolarLockNoBlowup(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"c1","type":"AimedSink","x":0,"y":0,"z":0,"inputs":[{"name":"In"}]},
	    {"id":"n2","type":"AimedSink","x":60,"y":10,"z":0,"inputs":[{"name":"In"}]},
	    {"id":"n3","type":"AimedPacer","x":40,"y":-30,"z":10,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]},
	    {"id":"n5","type":"AimedSrc","x":90,"y":20,"z":5,"outputs":[{"name":"Out"}]},
	    {"id":"n6","type":"AimedPacer","x":75,"y":-10,"z":-8,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]}
	  ],
	  "edges": [
	    {"label":"e63","kind":"data","source":"n6","sourceHandle":"Feedback","target":"n3","targetHandle":"FromSrc"}
	  ],
	  "view": {"nodes": {
	    "c1": {"x":0,"y":0,"z":0}, "n2": {"x":60,"y":10,"z":0},
	    "n3": {"x":40,"y":-30,"z":10}, "n5": {"x":90,"y":20,"z":5}, "n6": {"x":75,"y":-10,"z":-8}
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

	// Locks (authored before Start; positions are good).
	md.polarEqs = []polarEq{
		{Center: "n2", A: polarTerm{"n5", compPhi, 1}, B: polarTerm{"n6", compPhi, -1}, Active: true},
		{Center: "c1", A: polarTerm{"n2", compPhi, 1}, B: polarTerm{"n3", compPhi, -1}, Active: true},
		{Center: "c1", A: polarTerm{"n2", compR, 1}, B: polarTerm{"n3", compR, 1}, Active: true},
		{Kind: eqPortTorus, PortNode: "n6", PortName: "Feedback", PortIsInput: false, TorusNode: "n6", Active: true},
		{Kind: eqPortTorus, PortNode: "n3", PortName: "FromSrc", PortIsInput: true, TorusNode: "n3", Active: true},
	}
	for _, eq := range md.polarEqs {
		md.ensureEqLinks(eq)
	}

	md.Start(ctx)

	// Drag n5 in small steps toward n2 (a real drag stream), letting the cascade settle.
	for i := 0; i < 25; i++ {
		md.RootMove("n5", vec3{X: 90 - float64(i), Y: 20 - float64(i)*0.4, Z: 5})
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)

	for _, id := range []string{"c1", "n2", "n3", "n5", "n6"} {
		c, ok := md.centerOfNode(id)
		if !ok {
			t.Fatalf("node %s has no center", id)
		}
		if m := c.length(); math.IsNaN(m) || math.IsInf(m, 0) || m > 1e5 {
			t.Fatalf("node %s BLEW UP: center=%v |c|=%g", id, c, m)
		}
	}
}
