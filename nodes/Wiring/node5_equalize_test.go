package Wiring

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

// pollNode5Converged waits until node 5's local-polar-radial distances to peers 2, 7, 8
// (all measured in node 5's own frame) have converged and are pairwise equal, since the
// nodeMover goroutines apply center messages asynchronously.
func pollNode5Converged(t *testing.T, md *MoveDispatch, target vec3) (d5to2, d5to7, d5to8 float64) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c5, _ := md.centerOfNode("5")
		c2, _ := md.centerOfNode("2")
		c7, _ := md.centerOfNode("7")
		c8, _ := md.centerOfNode("8")
		d5to2 = cart2polar(c2.sub(c5)).R
		d5to7 = cart2polar(c7.sub(c5)).R
		d5to8 = cart2polar(c8.sub(c5)).R
		if math.Abs(c5.X-target.X) <= eps && math.Abs(c5.Y-target.Y) <= eps && math.Abs(c5.Z-target.Z) <= eps &&
			math.Abs(d5to7-d5to2) <= eps && math.Abs(d5to8-d5to2) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not converge: target=%v c5=%v d5to2=%v d5to7=%v d5to8=%v", target, c5, d5to2, d5to7, d5to8)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNode5DragEqualizesNeighborDistances verifies the peer-frame local-polar-radial
// equalization scoped to node 5: dragging node 5 sets its double-link distances to
// peers 7 and 8 equal to its double-link distance to peer 2, all measured in node 5's
// own frame. Peer 2 stays put.
func TestNode5DragEqualizesNeighborDistances(t *testing.T) {
	geoms := map[string]nodeGeom{
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"5": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}), Inputs: []portGeom{{Name: "in2"}}, Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"7": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"2To5": {Source: "2", Target: "5", SourceHandle: "out", TargetHandle: "in2"},
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"2": {}, "5": {}, "7": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	targets := []vec3{
		{10, 5, -5},
		{-15, 20, 8},
	}

	var lastD5to7, lastD5to2, lastD5to8 float64
	for _, target := range targets {
		if ok := md.RootMove("5", target); !ok {
			t.Fatalf("RootMove(5, %v) = false", target)
		}
		lastD5to2, lastD5to7, lastD5to8 = pollNode5Converged(t, md, target)
	}
	t.Logf("final distances: d5to2=%v d5to7=%v d5to8=%v", lastD5to2, lastD5to7, lastD5to8)
}

// TestNode5DragCascadesToNode2Follower6AndStopsBeforeNode1 exercises the full STEP 1
// decentralized node-5 chain (node5-decentralized-cascade.md): dragging node 5 must
// cascade — via node-to-node moveMsgKindTrigger/Equalize messages, not a central
// recursion — into rule-neighbor node 2, which repositions ITS follower node 6 to hold
// dist(2,6) == dist(2,5), while node 2 itself and node 1/node 3 (across the edge that did
// NOT change) stay put. This proves both the cascade-into-2 and the delta-gated stop
// before node 1.
func TestNode5DragCascadesToNode2Follower6AndStopsBeforeNode1(t *testing.T) {
	geoms := map[string]nodeGeom{
		"1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}),
			Inputs:  []portGeom{{Name: "in1"}},
			Outputs: []portGeom{{Name: "out5"}, {Name: "out6"}}},
		"3": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{100, 10, 0}), Inputs: []portGeom{{Name: "in"}}},
		"5": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}),
			Inputs:  []portGeom{{Name: "in2"}},
			Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"6": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{35, 25, 10}), Inputs: []portGeom{{Name: "in"}}},
		"7": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"1To2": {Source: "1", Target: "2", SourceHandle: "out", TargetHandle: "in1"},
		"2To5": {Source: "2", Target: "5", SourceHandle: "out5", TargetHandle: "in2"},
		"2To6": {Source: "2", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"1To3": {Source: "1", Target: "3", SourceHandle: "out", TargetHandle: "in"},
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"1": {}, "2": {}, "3": {}, "5": {}, "6": {}, "7": {}, "8": {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c1Before, _ := md.centerOfNode("1")
	c2Before, _ := md.centerOfNode("2")
	c3Before, _ := md.centerOfNode("3")

	target := vec3{10, 5, -5}
	if ok := md.RootMove("5", target); !ok {
		t.Fatalf("RootMove(5, %v) = false", target)
	}
	pollNode5Converged(t, md, target)

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	var d2to5, d2to6 float64
	for {
		c2, _ := md.centerOfNode("2")
		c5, _ := md.centerOfNode("5")
		c6, _ := md.centerOfNode("6")
		d2to5 = cart2polar(c5.sub(c2)).R
		d2to6 = cart2polar(c6.sub(c2)).R
		if math.Abs(d2to6-d2to5) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 6 did not converge to dist(2,5): d2to5=%v d2to6=%v", d2to5, d2to6)
		}
		time.Sleep(time.Millisecond)
	}

	c1After, _ := md.centerOfNode("1")
	c2After, _ := md.centerOfNode("2")
	c3After, _ := md.centerOfNode("3")
	if got := c1After.sub(c1Before).length(); got > eps {
		t.Fatalf("node 1 moved: before=%v after=%v delta=%v", c1Before, c1After, got)
	}
	if got := c2After.sub(c2Before).length(); got > eps {
		t.Fatalf("node 2 moved: before=%v after=%v delta=%v", c2Before, c2After, got)
	}
	if got := c3After.sub(c3Before).length(); got > eps {
		t.Fatalf("node 3 moved: before=%v after=%v delta=%v", c3Before, c3After, got)
	}
	t.Logf("final: d2to5=%v d2to6=%v node1=%v node3=%v", d2to5, d2to6, c1After, c3After)
}

// tappedMsg is one (destID, msg) pair recorded by the msgTap during a test.
type tappedMsg struct {
	destID   string
	kind     string
	senderID string
	targetC  float64
}

// newNode5ChainDispatch builds the 7-node graph (1,2,3,5,6,7,8) used by the node-5-chain
// tests: 1-2, 2-5, 2-6, 1-3, 5-7, 5-8.
func newNode5ChainDispatch() *MoveDispatch {
	geoms := map[string]nodeGeom{
		"1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}),
			Inputs:  []portGeom{{Name: "in1"}},
			Outputs: []portGeom{{Name: "out5"}, {Name: "out6"}}},
		"3": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{100, 10, 0}), Inputs: []portGeom{{Name: "in"}}},
		"5": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}),
			Inputs:  []portGeom{{Name: "in2"}},
			Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"6": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{35, 25, 10}), Inputs: []portGeom{{Name: "in"}}},
		"7": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"1To2": {Source: "1", Target: "2", SourceHandle: "out", TargetHandle: "in1"},
		"2To5": {Source: "2", Target: "5", SourceHandle: "out5", TargetHandle: "in2"},
		"2To6": {Source: "2", Target: "6", SourceHandle: "out6", TargetHandle: "in"},
		"1To3": {Source: "1", Target: "3", SourceHandle: "out", TargetHandle: "in"},
		"5To7": {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8": {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"1": {}, "2": {}, "3": {}, "5": {}, "6": {}, "7": {}, "8": {},
	}
	return md
}

// TestNode5DragEmitsDecentralizedMessages is an ANTI-DRIFT test: it does not merely check
// final positions (which the OLD central rootMove recursion also reproduces) — it asserts
// on the actual moveMsgKindTrigger/moveMsgKindEqualize traffic routed through sendMove
// (node_move.go:737, the one chokepoint every node-to-node message crosses), via the
// test-only md.SetMsgTap seam. A reversion to the central recursion (node5-decentralized-
// cascade.md step 3's "retire origin/central recursion" run backwards) sends ZERO Equalize/
// Trigger messages and fails this test hard, even though it would still pass the final-
// position tests above.
func TestNode5DragEmitsDecentralizedMessages(t *testing.T) {
	md := newNode5ChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger {
			return // ignore Center/Resend/etc. fanCenters noise
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{
			destID:   destID,
			kind:     msg.Kind,
			senderID: msg.SenderID,
			targetC:  msg.TargetC,
		})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target := vec3{10, 5, -5}
	if ok := md.RootMove("5", target); !ok {
		t.Fatalf("RootMove(5, %v) = false", target)
	}
	// Reuse the two chains' convergence polls so the trace has fully drained before we
	// read it: node 5's own peer equalize (7/8 vs 2), then node 2's follower (6) vs 5.
	d5to2, _, _ := pollNode5Converged(t, md, target)
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c2, _ := md.centerOfNode("2")
		c6, _ := md.centerOfNode("6")
		c5, _ := md.centerOfNode("5")
		if math.Abs(cart2polar(c6.sub(c2)).R-cart2polar(c5.sub(c2)).R) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 6 did not converge")
		}
		time.Sleep(time.Millisecond)
	}
	// A brief settle so no in-flight tapped send races the read below (sendMove's tap
	// call happens synchronously before the channel send, so by the time a message's
	// EFFECT is observable via centerOfNode, its tap call has already returned).
	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	// 1. Node 5 receives a self-initiated Trigger (SenderID == "").
	found5Trigger := false
	for _, m := range trace {
		if m.destID == "5" && m.kind == moveMsgKindTrigger && m.senderID == "" {
			found5Trigger = true
		}
	}
	if !found5Trigger {
		t.Errorf("expected a self-initiated Trigger (SenderID==\"\") to node 5; got %+v", trace)
	}

	// 2. Equalize routed to followers 7 AND 8, with TargetC ~= dist(5,2).
	got7, got8 := false, false
	for _, m := range trace {
		if m.kind != moveMsgKindEqualize {
			continue
		}
		switch m.destID {
		case "7":
			got7 = true
			if math.Abs(m.targetC-d5to2) > 1e-6 {
				t.Errorf("Equalize to 7: targetC=%v want ~%v", m.targetC, d5to2)
			}
		case "8":
			got8 = true
			if math.Abs(m.targetC-d5to2) > 1e-6 {
				t.Errorf("Equalize to 8: targetC=%v want ~%v", m.targetC, d5to2)
			}
		}
	}
	if !got7 || !got8 {
		t.Errorf("expected Equalize routed to both 7 and 8; got7=%v got8=%v trace=%+v", got7, got8, trace)
	}

	// 3. Trigger forwarded to node 2 (from node 5).
	found2Trigger := false
	for _, m := range trace {
		if m.destID == "2" && m.kind == moveMsgKindTrigger && m.senderID == "5" {
			found2Trigger = true
		}
	}
	if !found2Trigger {
		t.Errorf("expected a Trigger forwarded to node 2 (SenderID==\"5\"); got %+v", trace)
	}

	// 4. Equalize routed to node 2's follower 6.
	found6Equalize := false
	for _, m := range trace {
		if m.destID == "6" && m.kind == moveMsgKindEqualize {
			found6Equalize = true
		}
	}
	if !found6Equalize {
		t.Errorf("expected Equalize routed to node 6; got %+v", trace)
	}

	// 5. THE KEY ANTI-DRIFT ASSERTION: no Equalize or Trigger is EVER routed to node 1 or
	// node 3 — the delta-gated stop before node 1 (node5-decentralized-cascade.md "no
	// message to 1"). A reversion to the unconditional central cascade (which visits node
	// 1 as a no-op via equalizeNeighborDistancesWithSourceCenter) would, if it were also
	// wired to route THROUGH sendMove with these kinds, break this assertion; the current
	// central path doesn't emit these kinds at all, which assertion 6 below also catches.
	for _, m := range trace {
		if m.destID == "1" || m.destID == "3" {
			t.Errorf("node %s must never receive an Equalize/Trigger message; got %+v", m.destID, m)
		}
	}

	// 6. Exact message count: self-trigger to 5 (1) + Equalize to 7,8 (2) +
	// forwarded trigger to 2 (1) + Equalize to 6 (1) = 5. A reversion to the central
	// rootMove recursion sends ZERO of these messages and fails this count outright
	// (0 != 5), making silent drift back to the central coordinator impossible to pass.
	const wantCount = 5
	if len(trace) != wantCount {
		t.Errorf("expected exactly %d Equalize/Trigger messages, got %d: %+v", wantCount, len(trace), trace)
	}
}
