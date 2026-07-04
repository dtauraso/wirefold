// port_torus_chain_propagation_test.go — regression for transitive `port ∈ torus`
// colinearity: two coupled edges A->mid and c->mid (mid's FromLeft/FromRight both
// torus-locked to their own ring, same as A's and c's output ports) must fully
// propagate through the shared middle node when EITHER outer endpoint moves, not
// just the one hop directly off the dragged node. Mirrors the real chain found in
// topology/view/scene.json (nodes 6/9/3): node 9 (mid) has two active eqPortTorus
// locks (FromLeft, FromRight, both isInput=true) coupling it to node 6's Out2 lock
// and node 3's Out lock, via edges 6->9 and 3->9.
package Wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// chainGate2In is a minimal test-only kind with two paced inputs (FromLeft,
// FromRight) and one paced output (ToPassed) — a fake stand-in for
// WindowAndInhibitRightGate's port shape (the Wiring package's test binary does not
// import real node packages; see fanin_travel_time_test.go's faninSrc/faninSink for
// the same pattern).
type chainGate2In struct {
	FromLeft, FromRight *In
	ToPassed            *Out
}

func (n *chainGate2In) Update(ctx context.Context) {}

func init() {
	Register("ChainGate2In", func() any { return &chainGate2In{} })
}

// portTorusChainTopo builds a real loaded topology: two FanInSrc nodes "a" and "c"
// each with an "Out" port, feeding a chainGate2In "mid" on FromLeft and FromRight
// respectively — the same edge/port shape as the persisted 6-9-3 chain.
const portTorusChainTopo = `{
  "nodes": [
    {"id":"a","type":"FanInSrc","outputs":[{"name":"Out"}]},
    {"id":"mid","type":"ChainGate2In","inputs":[{"name":"FromLeft"},{"name":"FromRight"}],"outputs":[{"name":"ToPassed"}]},
    {"id":"c","type":"FanInSrc","outputs":[{"name":"Out"}]}
  ],
  "edges": [
    {"label":"e-a-mid","kind":"data","source":"a","sourceHandle":"Out","target":"mid","targetHandle":"FromLeft"},
    {"label":"e-c-mid","kind":"data","source":"c","sourceHandle":"Out","target":"mid","targetHandle":"FromRight"}
  ],
  "view": {"nodes": {
    "a":   {"x": -20, "y": 0, "z": 0},
    "mid": {"x": 0,   "y": 0, "z": 0},
    "c":   {"x": 20,  "y": 0, "z": 0}
  }}
}`

// loadPortTorusChainMD loads portTorusChainTopo and installs the two active
// eqPortTorus locks that couple a->mid and c->mid (mirroring scene.json's 6/9/3
// entries: each port pinned to its OWN node's ring).
func loadPortTorusChainMD(t *testing.T) (*MoveDispatch, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(portTorusChainTopo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tr := T.New(1024)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		cancel()
		t.Fatalf("LoadTopology: %v", err)
	}
	md.Start(ctx)
	// Place the three nodes at known world points (mirrors
	// locks_apply_integration_test.go: view positions aren't guaranteed applied
	// until an explicit RootMove settles each mover's held center).
	md.RootMove("a", vec3{-20, 0, 0})
	waitCenterEq(t, md, "a", vec3{-20, 0, 0})
	md.RootMove("mid", vec3{0, 0, 0})
	waitCenterEq(t, md, "mid", vec3{0, 0, 0})
	md.RootMove("c", vec3{20, 0, 0})
	waitCenterEq(t, md, "c", vec3{20, 0, 0})
	md.polarEqs = append(md.polarEqs,
		polarEq{Kind: eqPortTorus, PortNode: "a", PortName: "Out", PortIsInput: false, TorusNode: "a", Active: true},
		polarEq{Kind: eqPortTorus, PortNode: "mid", PortName: "FromLeft", PortIsInput: true, TorusNode: "mid", Active: true},
		polarEq{Kind: eqPortTorus, PortNode: "mid", PortName: "FromRight", PortIsInput: true, TorusNode: "mid", Active: true},
		polarEq{Kind: eqPortTorus, PortNode: "c", PortName: "Out", PortIsInput: false, TorusNode: "c", Active: true},
	)
	return md, cancel
}

func waitCenterEq(t *testing.T, md *MoveDispatch, id string, want vec3) vec3 {
	t.Helper()
	for i := 0; i < 200; i++ {
		if c, ok := md.centerOfNode(id); ok && c.sub(want).length() < 1e-6 {
			return c
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, _ := md.centerOfNode(id)
	t.Fatalf("node %s never reached %v (last %v)", id, want, got)
	return got
}

// waitCenterChanged polls until id's center's z differs from before by more than eps,
// or times out returning the last-seen center (caller asserts).
func waitCenterChanged(t *testing.T, md *MoveDispatch, id string, before vec3) vec3 {
	t.Helper()
	var last vec3
	for i := 0; i < 200; i++ {
		c, ok := md.centerOfNode(id)
		if ok {
			last = c
			if chordLength(c, before) > lockPropEps {
				return c
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return last
}

// TestPortTorusChainPropagatesThroughMiddle_EndpointMove is the regression: dragging
// outer endpoint "a" must ripple through the shared middle "mid" to the FAR endpoint
// "c" (second hop) — applyPortTorusColinearity called once on "a" alone only reaches
// "mid"; RootMove's worklist must re-run it on "mid" to reach "c".
func TestPortTorusChainPropagatesThroughMiddle_EndpointMove(t *testing.T) {
	md, cancel := loadPortTorusChainMD(t)
	defer cancel()

	// Settle initial positions (view z=0 for all three).
	waitCenterEq(t, md, "a", vec3{-20, 0, 0})
	waitCenterEq(t, md, "mid", vec3{0, 0, 0})
	cBefore := waitCenterEq(t, md, "c", vec3{20, 0, 0})

	target := vec3{-20, 0, 7} // move a's z away from mid's/c's z
	if ok := md.RootMove("a", target); !ok {
		t.Fatalf("RootMove(a) returned false")
	}

	aFinal := waitCenterEq(t, md, "a", target)
	if aFinal.sub(target).length() > 1e-6 {
		t.Fatalf("dragged node a: got %v, want pinned to target %v", aFinal, target)
	}

	midFinal := waitCenterChanged(t, md, "mid", vec3{0, 0, 0})
	if midFinal.Z != target.Z {
		t.Fatalf("mid.z=%v after a's move, want a.z=%v (first hop)", midFinal.Z, target.Z)
	}

	cFinal := waitCenterChanged(t, md, "c", cBefore)
	if cFinal.Z != target.Z {
		t.Fatalf("REGRESSION: far endpoint c.z=%v after dragging a, want a.z=%v — second hop through mid did not propagate", cFinal.Z, target.Z)
	}
}

// TestPortTorusChainPropagatesThroughMiddle_MiddleMove is the guard: dragging the
// shared middle node "mid" must move BOTH outer endpoints — the case that already
// worked before this change (one call to applyPortTorusColinearity(mid,...) touches
// both coupled edges directly).
func TestPortTorusChainPropagatesThroughMiddle_MiddleMove(t *testing.T) {
	md, cancel := loadPortTorusChainMD(t)
	defer cancel()

	aBefore := waitCenterEq(t, md, "a", vec3{-20, 0, 0})
	waitCenterEq(t, md, "mid", vec3{0, 0, 0})
	cBefore := waitCenterEq(t, md, "c", vec3{20, 0, 0})

	target := vec3{0, 0, 9}
	if ok := md.RootMove("mid", target); !ok {
		t.Fatalf("RootMove(mid) returned false")
	}

	midFinal := waitCenterEq(t, md, "mid", target)
	if midFinal.sub(target).length() > 1e-6 {
		t.Fatalf("dragged node mid: got %v, want pinned to target %v", midFinal, target)
	}

	aFinal := waitCenterChanged(t, md, "a", aBefore)
	if aFinal.Z != target.Z {
		t.Fatalf("a.z=%v after dragging mid, want mid.z=%v", aFinal.Z, target.Z)
	}
	cFinal := waitCenterChanged(t, md, "c", cBefore)
	if cFinal.Z != target.Z {
		t.Fatalf("c.z=%v after dragging mid, want mid.z=%v", cFinal.Z, target.Z)
	}
}

// TestPortTorusChainCycleTerminates guards against a cycle of coupled edges hanging
// RootMove: a triangle a-mid, mid-c, c-a all torus-coupled must still terminate
// (maxIters backstop) with finite centers.
func TestPortTorusChainCycleTerminates(t *testing.T) {
	md, cancel := loadPortTorusChainMD(t)
	defer cancel()
	waitCenterEq(t, md, "a", vec3{-20, 0, 0})
	waitCenterEq(t, md, "mid", vec3{0, 0, 0})
	waitCenterEq(t, md, "c", vec3{20, 0, 0})

	// Close the cycle: couple c's Out (already locked) to a NEW lock so a->c also
	// reads as a coupled edge. Add a direct edge a->c via edgeMovers so the loop has a
	// third leg; reuse existing "Out"/"Out" lock semantics is enough since
	// portTorusLocked only checks (node,port,isInput), already active for a and c.
	md.edgeMovers["e-a-c-extra"] = &edgeMover{
		edgeID: "e-a-c-extra",
		srcID:  "c", srcH: "Out",
		dstID: "a", dstH: "FromLeft", // reuse mid's FromLeft lock name isn't on "a"; add one below
	}
	md.polarEqs = append(md.polarEqs,
		polarEq{Kind: eqPortTorus, PortNode: "a", PortName: "FromLeft", PortIsInput: true, TorusNode: "a", Active: true},
	)

	done := make(chan bool, 1)
	go func() {
		done <- md.RootMove("a", vec3{-20, 0, 5})
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatalf("RootMove(a) returned false")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("RootMove did not terminate within 5s on a coupled cycle")
	}

	for _, id := range []string{"a", "mid", "c"} {
		c, ok := md.centerOfNode(id)
		if !ok {
			t.Fatalf("node %s has no center after cycle move", id)
		}
		if c.X != c.X || c.Y != c.Y || c.Z != c.Z { // NaN check
			t.Fatalf("node %s center is NaN: %v", id, c)
		}
	}
}
