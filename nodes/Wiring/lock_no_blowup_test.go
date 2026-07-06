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
	// Positions are scene polar (r,θ,φ) about the origin, from the cartesian points
	// c1(0,0,0) n2(60,10,0) n3(40,-30,10) n5(90,20,5) n6(75,-10,-8).
	const topo = `{
	  "nodes": [
	    {"id":"c1","type":"AimedSink","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0,"inputs":[{"name":"In"}]},
	    {"id":"n2","type":"AimedSink","scenePolarR":60.827625303,"scenePolarTheta":1.40564764938,"scenePolarPhi":0,"inputs":[{"name":"In"}]},
	    {"id":"n3","type":"AimedPacer","scenePolarR":50.9901951359,"scenePolarTheta":2.19981112922,"scenePolarPhi":0.244978663127,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]},
	    {"id":"n5","type":"AimedSrc","scenePolarR":92.3309265631,"scenePolarTheta":1.35245344738,"scenePolarPhi":0.0554985052457,"outputs":[{"name":"Out"}]},
	    {"id":"n6","type":"AimedPacer","scenePolarR":76.0854782465,"scenePolarTheta":1.70260881705,"scenePolarPhi":-0.106264862891,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]}
	  ],
	  "edges": [
	    {"label":"e63","kind":"data","source":"n6","sourceHandle":"Feedback","target":"n3","targetHandle":"FromSrc"}
	  ]
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
	md.setPolarEqs([]polarEq{
		{A: polarTerm{"n5", compPhi, 1}, B: polarTerm{"n6", compPhi, -1}, Active: true},
		{A: polarTerm{"n2", compPhi, 1}, B: polarTerm{"n3", compPhi, -1}, Active: true},
		{A: polarTerm{"n2", compR, 1}, B: polarTerm{"n3", compR, 1}, Active: true},
		{Kind: eqPortTorus, PortNode: "n6", PortName: "Feedback", PortIsInput: false, TorusNode: "n6", Active: true},
		{Kind: eqPortTorus, PortNode: "n3", PortName: "FromSrc", PortIsInput: true, TorusNode: "n3", Active: true},
	})
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
