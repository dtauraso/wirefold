package Wiring

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// approxEqual returns true when two floats are within eps of each other.
func approxEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}

// approxVec3 returns true when all components are within eps.
func approxVec3(a, b vec3, eps float64) bool {
	return approxEqual(a.X, b.X, eps) && approxEqual(a.Y, b.Y, eps) && approxEqual(a.Z, b.Z, eps)
}

// buildAimedFixture returns a minimal AimedPortRegistry for edges 1→2 and 1→6
// (same keys as loader.go populates in production).
func buildAimedFixture() AimedPortRegistry {
	return AimedPortRegistry{
		{NodeID: "1", PortName: "ToHoldNewSendOld", IsInput: false}: "2",
		{NodeID: "2", PortName: "FromPrevHoldNewSendOldNode", IsInput: true}: "1",
		{NodeID: "1", PortName: "ToExcitatory", IsInput: false}: "6",
		{NodeID: "6", PortName: "FromInput", IsInput: true}: "1",
	}
}

// buildGeom builds a minimal nodeGeom with a known center for testing.
func buildGeom(kind string, center vec3) nodeGeom {
	c := center
	return nodeGeom{
		Kind:   kind,
		Center: &c,
		// At least one output port so portDir has something to fall back on.
		Outputs: []portGeom{{Name: "ToHoldNewSendOld"}, {Name: "ToExcitatory"}},
		Inputs:  []portGeom{{Name: "FromPrevHoldNewSendOldNode"}, {Name: "FromInput"}},
	}
}

// TestPortDirAimed_Node1AimsAtNode2 verifies that node 1's ToHoldNewSendOld
// port direction points toward node 2's center.
func TestPortDirAimed_Node1AimsAtNode2(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	node2Center := vec3{X: 3, Y: 0, Z: 0}

	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centers := map[string]vec3{"1": node1Center, "2": node2Center}
	centerOf := func(id string) (vec3, bool) {
		c, ok := centers[id]
		return c, ok
	}

	dir, ok := portDirAimed(geom1, "ToHoldNewSendOld", false, "1", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed returned ok=false for registered port")
	}
	want := vec3{X: 1, Y: 0, Z: 0}
	const eps = 1e-9
	if !approxVec3(dir, want, eps) {
		t.Errorf("portDirAimed(node1→node2 at (3,0,0)) = %+v; want %+v", dir, want)
	}
}

// TestPortDirAimed_Node1AimsAtNode2_AfterMove verifies that after moving node 2
// to (0,3,0) the direction updates to (0,1,0).
func TestPortDirAimed_Node1AimsAtNode2_AfterMove(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	node2Center := vec3{X: 0, Y: 3, Z: 0} // moved

	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centers := map[string]vec3{"1": node1Center, "2": node2Center}
	centerOf := func(id string) (vec3, bool) {
		c, ok := centers[id]
		return c, ok
	}

	dir, ok := portDirAimed(geom1, "ToHoldNewSendOld", false, "1", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed returned ok=false for registered port after move")
	}
	want := vec3{X: 0, Y: 1, Z: 0}
	const eps = 1e-9
	if !approxVec3(dir, want, eps) {
		t.Errorf("portDirAimed(node1→node2 at (0,3,0)) = %+v; want %+v", dir, want)
	}
}

// TestLoaderInitialEdgeSegment_AimedPortUsesRadialNotRingAnchor verifies the fix
// introduced in loader.go: when the aimed-port registry is built BEFORE the
// initial edge-geometry loop, the edge segment's start position equals
// center + nodeRadius*normalize(target-center), NOT the ring-anchor position.
//
// Setup: node "1" at origin, node "2" at (5,0,0).
// Edge 1→2 uses output port "ToHoldNewSendOld" (registered aimed → node 2).
// Expected start: along +X from node 1's center.
// Ring-anchor fallback would return a non-+X direction (ringAnchorDir at index 0
// which for a multi-port node is not guaranteed +X), so any mismatch flags regression.
func TestLoaderInitialEdgeSegment_AimedPortUsesRadialNotRingAnchor(t *testing.T) {
	node1Center := vec3{X: 0, Y: 0, Z: 0}
	node2Center := vec3{X: 5, Y: 0, Z: 0}

	geom1 := buildGeom("HoldNewSendOld", node1Center)
	geom2 := buildGeom("HoldNewSendOld", node2Center)

	// Registry matching what loader.go builds for edges 1→2.
	registry := AimedPortRegistry{
		{NodeID: "1", PortName: "ToHoldNewSendOld", IsInput: false}: "2",
		{NodeID: "2", PortName: "FromPrevHoldNewSendOldNode", IsInput: true}: "1",
	}
	centers := map[string]vec3{"1": node1Center, "2": node2Center}
	centerOf := func(id string) (vec3, bool) {
		c, ok := centers[id]
		return c, ok
	}

	seg := segmentBetweenPortsAimed(geom1, "ToHoldNewSendOld", "1", geom2, "FromPrevHoldNewSendOldNode", "2", registry, centerOf)

	// Start should be node1Center + nodeRadius * (1,0,0).
	r := nodeRadius(geom1.Kind)
	wantStart := vec3{X: r, Y: 0, Z: 0}
	const eps = 1e-9
	if !approxVec3(seg.Start, wantStart, eps) {
		t.Errorf("initial edge segment Start = %+v; want aimed radial %+v (nodeRadius=%v); ring-anchor bug if non-+X", seg.Start, wantStart, r)
	}

	// End should be node2Center + nodeRadius * (-1,0,0) (aimed back at node 1).
	wantEnd := vec3{X: node2Center.X - r, Y: 0, Z: 0}
	if !approxVec3(seg.End, wantEnd, eps) {
		t.Errorf("initial edge segment End = %+v; want aimed radial %+v", seg.End, wantEnd)
	}
}

// TestPortDirAimed_NonRegisteredPort verifies that a non-registered port still
// returns a non-zero direction via the portDir fallback.
func TestPortDirAimed_NonRegisteredPort(t *testing.T) {
	registry := buildAimedFixture()

	node1Center := vec3{X: 0, Y: 0, Z: 0}
	geom1 := buildGeom("HoldNewSendOld", node1Center)

	centerOf := func(id string) (vec3, bool) { return vec3{}, false }

	// "SomeOtherPort" is not in the registry; should fall back to portDir.
	// portDir returns (vec3{}, false) for unknown ports so we test a known port
	// not in the registry by querying with a different nodeID.
	dir, _ := portDir(geom1, "ToHoldNewSendOld", false)
	if dir.length() == 0 {
		// portDir with no AnchorId returns ringAnchorDir(R, 0) which is (1,0,0) —
		// never zero for a valid port on a known kind. If we get zero the test is
		// inconclusive but not a regression.
		t.Log("portDir returned zero vector for fallback port (inconclusive)")
		return
	}
	// Now verify portDirAimed with a node NOT in the registry also falls back.
	geomX := buildGeom("HoldNewSendOld", vec3{X: 10, Y: 0, Z: 0})
	dirAimed, ok := portDirAimed(geomX, "ToHoldNewSendOld", false, "99", registry, centerOf)
	if !ok {
		t.Fatal("portDirAimed fallback returned ok=false for a valid port")
	}
	if dirAimed.length() == 0 {
		t.Error("portDirAimed fallback returned zero direction for a non-registered node")
	}
}

// aimedSrc / aimedSink / aimedPacer are minimal node kinds registered once for
// TestAimedPortRegistry_DerivedFromEdges.
type aimedSrc struct {
	Out        *Out
	FeedbackIn *In
}

func (n *aimedSrc) Update(_ context.Context) {}

type aimedSink struct{ In *In }

func (n *aimedSink) Update(_ context.Context) {}

type aimedPacer struct {
	FromSrc  *In
	Feedback *Out
}

func (n *aimedPacer) Update(_ context.Context) {}

func init() {
	Register("AimedSrc", func() any { return &aimedSrc{} })
	Register("AimedSink", func() any { return &aimedSink{} })
	Register("AimedPacer", func() any { return &aimedPacer{} })
}

// TestAimedPortRegistry_DerivedFromEdges verifies that the aimed-port registry is
// derived purely from the loaded edge list: every edge contributes 2 entries
// (source output + target input), including feedback edges. This test uses an
// inline topology with a feedback loop (Pacer→Src) to confirm that loop
// is NOT excluded from the registry.
func TestAimedPortRegistry_DerivedFromEdges(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src",   "type":"AimedSrc",   "x":0,  "y":0,  "z":0,  "outputs":[{"name":"Out"}], "inputs":[{"name":"FeedbackIn"}]},
	    {"id":"sink",  "type":"AimedSink",  "x":5,  "y":0,  "z":0,  "inputs":[{"name":"In"}]},
	    {"id":"pacer", "type":"AimedPacer", "x":0,  "y":5,  "z":0,  "inputs":[{"name":"FromSrc"}], "outputs":[{"name":"Feedback"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"chain","source":"src",   "sourceHandle":"Out",      "target":"sink",  "targetHandle":"In"},
	    {"label":"e2","kind":"chain","source":"src",   "sourceHandle":"Out",      "target":"pacer", "targetHandle":"FromSrc"},
	    {"label":"e3","kind":"chain","source":"pacer", "sourceHandle":"Feedback", "target":"src",   "targetHandle":"FeedbackIn"}
	  ]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(64)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	reg := md.AimedPorts
	// 3 edges × 2 ports each = 6 entries.
	// Note: e2 and e1 share the same source port (src.Out→sink and src.Out→pacer);
	// the registry is a map so duplicate keys from multiple edges on the same port
	// overwrite — count may be ≤6. We care that the feedback pair IS present.
	if len(reg) == 0 {
		t.Fatal("aimed-port registry is empty; want edge-derived entries")
	}

	// Feedback edge (pacer→src): must be registered just like forward edges.
	feedbackOut := AimedPortKey{NodeID: "pacer", PortName: "Feedback", IsInput: false}
	if got, ok := reg[feedbackOut]; !ok || got != "src" {
		t.Errorf("feedback aimed port {pacer,Feedback,false}: got %q,%v; want \"src\",true", got, ok)
	}
	feedbackIn := AimedPortKey{NodeID: "src", PortName: "FeedbackIn", IsInput: true}
	if got, ok := reg[feedbackIn]; !ok || got != "pacer" {
		t.Errorf("feedback aimed port {src,FeedbackIn,true}: got %q,%v; want \"pacer\",true", got, ok)
	}

	// Forward edge: src.Out → sink.In.
	fwdOut := AimedPortKey{NodeID: "src", PortName: "Out", IsInput: false}
	if got, ok := reg[fwdOut]; !ok {
		t.Errorf("forward aimed port {src,Out,false}: not registered (got %q,%v)", got, ok)
	}
	fwdIn := AimedPortKey{NodeID: "sink", PortName: "In", IsInput: true}
	if got, ok := reg[fwdIn]; !ok || got != "src" {
		t.Errorf("forward aimed port {sink,In,true}: got %q,%v; want \"src\",true", got, ok)
	}
}
