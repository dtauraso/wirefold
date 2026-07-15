package Wiring

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

// productionCascadeRoles mirrors the real 10-node topology's authored cascade roles
// (topology/nodes/*/meta.json localPolars[].role + gate) — node_move.go's
// ApplyCascadeRoles ignores any id not present in the caller's MoveDispatch, so every
// test below can pass this same map regardless of which subset of nodes its own mini
// graph builds. This is test-side data mirroring the spec; loader.go derives the
// identical map from disk for the real topology (deriveCascadeRoles).
func productionCascadeRoles() map[string]cascadeRoleSpec {
	return map[string]cascadeRoleSpec{
		"1":  {SourceID: "2", Followers: []string{"3"}, Gate: true, GateA: "2", GateB: "3"},
		"2":  {SourceID: "5", Followers: []string{"6"}},
		"5":  {SourceID: "2", Followers: []string{"7", "8"}},
		"6":  {AnchoredGates: []string{"9", "10"}},
		"9":  {Gate: true, GateA: "3", GateB: "6"},
		"10": {Gate: true, GateA: "6", GateB: "8"},
	}
}

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
	md.ApplyCascadeRoles(productionCascadeRoles())
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
// dist(2,6) == dist(2,5), while node 2 and node 1 (which never move) stay put. Node 2
// ALWAYS forwards to node 1 (no delta-gate — project_lock_propagation_decentralized);
// node 1 in turn Equalizes its follower node 3, but since dist(1,2) never changes and
// this fixture pre-equalizes node 3 to it, node 3's landing is IDEMPOTENT (a no-op),
// not a skip — termination-by-idempotence, not a sender-side change check.
func TestNode5DragCascadesToNode2Follower6AndStopsBeforeNode1(t *testing.T) {
	geoms := map[string]nodeGeom{
		"1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}),
			Inputs:  []portGeom{{Name: "in1"}},
			Outputs: []portGeom{{Name: "out5"}, {Name: "out6"}}},
		"3": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 40, 0}), Inputs: []portGeom{{Name: "in"}}}, // dist(1,3)==dist(1,2)==40, pre-equalized: node 1's always-forward Trigger (no delta-gate) is idempotent here
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
	md.ApplyCascadeRoles(productionCascadeRoles())
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
		"3": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 40, 0}), Inputs: []portGeom{{Name: "in"}}}, // dist(1,3)==dist(1,2)==40, pre-equalized: node 1's always-forward Trigger (no delta-gate) is idempotent here
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
	md.ApplyCascadeRoles(productionCascadeRoles())
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
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindDrag {
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

	// 1. Node 5 receives a self-routed Drag (SenderID == "") — the entry point that
	// commits node 5's own new position and then self-triggers (handleTrigger, called
	// directly on node 5's own goroutine, not via sendMove — so it never itself
	// appears in this tap trace, mirroring node 6's Drag entry).
	found5Trigger := false
	for _, m := range trace {
		if m.destID == "5" && m.kind == moveMsgKindDrag && m.senderID == "" {
			found5Trigger = true
		}
	}
	if !found5Trigger {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 5; got %+v", trace)
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

	// 5. Node 2 ALWAYS forwards (no delta-gate, project_lock_propagation_decentralized):
	// receiving a forwarded Trigger (SenderID=="5") still forwards to node 1 (whose own
	// sourceID is "2"), which in turn Equalizes its follower node 3. Termination is by
	// IDEMPOTENCE — node1/node2 never move here, so dist(1,2) is unchanged and node 3
	// (pre-equalized to dist(1,2) by this test's fixture geometry) lands at the SAME
	// position it started at, even though the message is sent.
	found1Trigger, found3Equalize := false, false
	for _, m := range trace {
		if m.destID == "1" && m.kind == moveMsgKindTrigger && m.senderID == "2" {
			found1Trigger = true
		}
		if m.destID == "3" && m.kind == moveMsgKindEqualize {
			found3Equalize = true
		}
	}
	if !found1Trigger {
		t.Errorf("expected a Trigger forwarded to node 1 (SenderID==\"2\"); got %+v", trace)
	}
	if !found3Equalize {
		t.Errorf("expected Equalize routed to node 3 (idempotent no-op); got %+v", trace)
	}

	// 6. Exact message count: self-trigger to 5 (1) + Equalize to 7,8 (2) + forwarded
	// trigger to 2 (1) + Equalize to 6 (1) + forwarded trigger to 1 (1) + Equalize to 3
	// (1) = 7. A reversion to the central rootMove recursion sends ZERO of these
	// messages and fails this count outright (0 != 7), making silent drift back to the
	// central coordinator impossible to pass.
	const wantCount = 7
	if len(trace) != wantCount {
		t.Errorf("expected exactly %d Equalize/Trigger messages, got %d: %+v", wantCount, len(trace), trace)
	}
}

// newFullChainDispatch builds the 9-node graph (1,2,3,5,6,7,8,9,10) used by the node-2/
// node-1 direct-drag message-system tests: 1-2, 2-5, 2-6, 1-3, 5-7, 5-8, 9-3, 9-6,
// 10-6, 10-8. Extends newNode5ChainDispatch's graph with gate nodes 9 and 10 (non-
// participants in the node-2/node-1 rule chain — they must never receive an
// Equalize/Trigger message when 2 or 1 is dragged directly).
func newFullChainDispatch() *MoveDispatch {
	geoms := map[string]nodeGeom{
		"1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{80, 0, 0}), Outputs: []portGeom{{Name: "out"}}},
		"2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{40, 0, 0}),
			Inputs:  []portGeom{{Name: "in1"}},
			Outputs: []portGeom{{Name: "out5"}, {Name: "out6"}}},
		"3": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{100, 10, 0}), Inputs: []portGeom{{Name: "in1"}, {Name: "in9"}}},
		"5": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 0, 0}),
			Inputs:  []portGeom{{Name: "in2"}},
			Outputs: []portGeom{{Name: "out7"}, {Name: "out8"}}},
		"6":  {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{35, 25, 10}), Inputs: []portGeom{{Name: "in2"}, {Name: "in9"}, {Name: "in10"}}},
		"7":  {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{0, 30, 20}), Inputs: []portGeom{{Name: "in"}}},
		"8":  {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{-25, -10, 15}), Inputs: []portGeom{{Name: "in5"}, {Name: "in10"}}},
		"9":  {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{60, 40, -10}), Outputs: []portGeom{{Name: "out3"}, {Name: "out6"}}},
		"10": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{20, -30, 15}), Outputs: []portGeom{{Name: "out6"}, {Name: "out8"}}},
	}
	edges := map[string]EdgeEndpoints{
		"1To2":  {Source: "1", Target: "2", SourceHandle: "out", TargetHandle: "in1"},
		"2To5":  {Source: "2", Target: "5", SourceHandle: "out5", TargetHandle: "in2"},
		"2To6":  {Source: "2", Target: "6", SourceHandle: "out6", TargetHandle: "in2"},
		"1To3":  {Source: "1", Target: "3", SourceHandle: "out", TargetHandle: "in1"},
		"5To7":  {Source: "5", Target: "7", SourceHandle: "out7", TargetHandle: "in"},
		"5To8":  {Source: "5", Target: "8", SourceHandle: "out8", TargetHandle: "in5"},
		"9To3":  {Source: "9", Target: "3", SourceHandle: "out3", TargetHandle: "in9"},
		"9To6":  {Source: "9", Target: "6", SourceHandle: "out6", TargetHandle: "in9"},
		"10To6": {Source: "10", Target: "6", SourceHandle: "out6", TargetHandle: "in10"},
		"10To8": {Source: "10", Target: "8", SourceHandle: "out8", TargetHandle: "in10"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.layoutHolders = map[string]*LayoutHolder{
		"1": {}, "2": {}, "3": {}, "5": {}, "6": {}, "7": {}, "8": {}, "9": {}, "10": {},
	}
	md.ApplyCascadeRoles(productionCascadeRoles())
	return md
}

// pollFullChainConverged waits until a direct node-2 or node-1 drag has fully
// converged: dist(2,6)==dist(2,5), dist(5,7)==dist(5,8)==dist(5,2), and
// dist(1,3)==dist(1,2) — the same peer-frame equalization the trace tests below
// assert as messages, verified here as final positions (the behavior-preserving
// safety net).
func pollFullChainConverged(t *testing.T, md *MoveDispatch) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c1, _ := md.centerOfNode("1")
		c2, _ := md.centerOfNode("2")
		c3, _ := md.centerOfNode("3")
		c5, _ := md.centerOfNode("5")
		c6, _ := md.centerOfNode("6")
		c7, _ := md.centerOfNode("7")
		c8, _ := md.centerOfNode("8")
		d2to6 := cart2polar(c6.sub(c2)).R
		d2to5 := cart2polar(c5.sub(c2)).R
		d5to7 := cart2polar(c7.sub(c5)).R
		d5to8 := cart2polar(c8.sub(c5)).R
		d1to3 := cart2polar(c3.sub(c1)).R
		d1to2 := cart2polar(c2.sub(c1)).R
		if math.Abs(d2to6-d2to5) <= eps && math.Abs(d5to7-d2to5) <= eps && math.Abs(d5to8-d2to5) <= eps && math.Abs(d1to3-d1to2) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("full chain did not converge: d2to6=%v d2to5=%v d5to7=%v d5to8=%v d1to3=%v d1to2=%v", d2to6, d2to5, d5to7, d5to8, d1to3, d1to2)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNode2DirectDragEmitsDecentralizedMessages proves a DIRECT node-2 drag is routed
// through node 5's decentralized trigger/equalize message system (RootMove →
// rootMoveViaMessages), not the central rootMove recursion. Before this change, a
// direct node-2 drag ran ONLY the central case "2" and emitted zero Equalize/Trigger
// messages — this test is RED against that code.
func TestNode2DirectDragEmitsDecentralizedMessages(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindDrag {
			return
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID, targetC: msg.TargetC})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target := vec3{45, 5, -5}
	if ok := md.RootMove("2", target); !ok {
		t.Fatalf("RootMove(2, %v) = false", target)
	}
	pollFullChainConverged(t, md)
	// Brief settle so no in-flight tapped send races the read below.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	has := func(dest, kind, senderID string) bool {
		for _, m := range trace {
			if m.destID == dest && m.kind == kind && m.senderID == senderID {
				return true
			}
		}
		return false
	}

	if !has("2", moveMsgKindDrag, "") {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 2; got %+v", trace)
	}
	if !has("6", moveMsgKindEqualize, "") {
		t.Errorf("expected Equalize routed to node 6; got %+v", trace)
	}
	if !has("5", moveMsgKindTrigger, "2") {
		t.Errorf("expected Trigger forwarded to node 5 (senderID=2); got %+v", trace)
	}
	if !has("1", moveMsgKindTrigger, "2") {
		t.Errorf("expected Trigger forwarded to node 1 (senderID=2); got %+v", trace)
	}
	if !has("7", moveMsgKindEqualize, "") {
		t.Errorf("expected Equalize routed to node 7; got %+v", trace)
	}
	if !has("8", moveMsgKindEqualize, "") {
		t.Errorf("expected Equalize routed to node 8; got %+v", trace)
	}
	if !has("3", moveMsgKindEqualize, "") {
		t.Errorf("expected Equalize routed to node 3; got %+v", trace)
	}

	for _, m := range trace {
		if m.destID == "9" || m.destID == "10" {
			t.Errorf("non-participant node %s must never receive an Equalize/Trigger message; got %+v", m.destID, m)
		}
	}

	const wantCount = 7
	if len(trace) != wantCount {
		t.Errorf("expected exactly %d Equalize/Trigger messages, got %d: %+v", wantCount, len(trace), trace)
	}
}

// TestNode1DragEmitsSelfTrigger is the RED proof for routing node 1's DIRECT drag
// through the decentralized goroutine-message path (mirroring
// TestNode9DragEmitsSelfTrigger / node9-decentralized-gate.md, widened to node 1 per
// retire-central-rootmove STEP 1): a node-1 drag must route a self-initiated Trigger
// (SenderID=="") to node 1's OWN inbox via sendMove, and must NOT send any
// Equalize/Trigger message to any OTHER node (in particular neighbors 2 and 3, which
// must never move or receive a message from a DIRECT node-1 drag). Against the
// CURRENT central rootMove path (case "1" runs synchronously on the drag call stack,
// never touching sendMove), this test is RED: zero messages are tapped.
func TestNode1DragEmitsSelfTrigger(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindDrag {
			return
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID, targetC: msg.TargetC})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target := vec3{85, 10, -8}
	if ok := md.RootMove("1", target); !ok {
		t.Fatalf("RootMove(1, %v) = false", target)
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c1, _ := md.centerOfNode("1")
		c2, _ := md.centerOfNode("2")
		c3, _ := md.centerOfNode("3")
		d1to2 := cart2polar(c2.sub(c1)).R
		d1to3 := cart2polar(c3.sub(c1)).R
		if math.Abs(d1to3-d1to2) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 1 drag did not converge to equal radii: d1to2=%v d1to3=%v", d1to2, d1to3)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	found1Trigger := false
	for _, m := range trace {
		if m.destID == "1" && m.kind == moveMsgKindDrag && m.senderID == "" {
			found1Trigger = true
		}
		if m.destID == "2" || m.destID == "3" {
			t.Errorf("neighbor %s must never receive an Equalize/Trigger message from a DIRECT node-1 drag; got %+v", m.destID, m)
		}
	}
	if !found1Trigger {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 1; got %+v", trace)
	}
}

// TestNode1DragKeepsNeighbors23Fixed is the behavior-preserving safety net (mirroring
// TestNode9DragEqualRadiiNeighborsFixed): dragging node 1 directly must NOT move node
// 2 or node 3 at all.
func TestNode1DragKeepsNeighbors23Fixed(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c2Before, _ := md.centerOfNode("2")
	c3Before, _ := md.centerOfNode("3")

	target := vec3{85, 10, -8}
	if ok := md.RootMove("1", target); !ok {
		t.Fatalf("RootMove(1, %v) = false", target)
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c1, _ := md.centerOfNode("1")
		c2, _ := md.centerOfNode("2")
		c3, _ := md.centerOfNode("3")
		d1to2 := cart2polar(c2.sub(c1)).R
		d1to3 := cart2polar(c3.sub(c1)).R
		if math.Abs(d1to3-d1to2) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 1 drag did not converge to equal radii: d1to2=%v d1to3=%v", d1to2, d1to3)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)

	c2After, _ := md.centerOfNode("2")
	c3After, _ := md.centerOfNode("3")
	if got := c2After.sub(c2Before).length(); got > eps {
		t.Fatalf("node 2 moved: before=%v after=%v delta=%v", c2Before, c2After, got)
	}
	if got := c3After.sub(c3Before).length(); got > eps {
		t.Fatalf("node 3 moved: before=%v after=%v delta=%v", c3Before, c3After, got)
	}
}

// TestNode1DragEqualRadii verifies the gate-node equal-radii pattern (mirroring node
// 9): after dragging node 1, dist(1,2) == dist(1,3) — node 1 lands equidistant from
// its two neighbors, neither of which moves.
func TestNode1DragEqualRadii(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	target := vec3{85, 10, -8}
	if ok := md.RootMove("1", target); !ok {
		t.Fatalf("RootMove(1, %v) = false", target)
	}

	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	var d1to2, d1to3 float64
	for {
		c1, _ := md.centerOfNode("1")
		c2, _ := md.centerOfNode("2")
		c3, _ := md.centerOfNode("3")
		d1to2 = cart2polar(c2.sub(c1)).R
		d1to3 = cart2polar(c3.sub(c1)).R
		if math.Abs(d1to3-d1to2) <= eps {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 1 drag did not converge to equal radii: d1to2=%v d1to3=%v", d1to2, d1to3)
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("final: d1to2=%v d1to3=%v", d1to2, d1to3)
}

// TestNode2DirectDragBehaviorPreserved is the SAFETY NET: it holds both before and
// after routing node 2's drag through the decentralized message system — final
// positions must match what the central cascade produced. References (5, 1) don't
// move; node 2's peer 6 and (transitively) 5's peers 7/8 and 1's peer 3 equalize.
func TestNode2DirectDragBehaviorPreserved(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c5Before, _ := md.centerOfNode("5")
	c1Before, _ := md.centerOfNode("1")

	target := vec3{45, 5, -5}
	if ok := md.RootMove("2", target); !ok {
		t.Fatalf("RootMove(2, %v) = false", target)
	}
	pollFullChainConverged(t, md)
	time.Sleep(20 * time.Millisecond)

	const eps = 1e-6
	c5After, _ := md.centerOfNode("5")
	c1After, _ := md.centerOfNode("1")
	if got := c5After.sub(c5Before).length(); got > eps {
		t.Fatalf("reference node 5 moved: before=%v after=%v delta=%v", c5Before, c5After, got)
	}
	if got := c1After.sub(c1Before).length(); got > eps {
		t.Fatalf("reference node 1 moved: before=%v after=%v delta=%v", c1Before, c1After, got)
	}
}

// TestNode1DirectDragBehaviorPreserved (node 2/3-unmoved, equal-radii behavior) is
// superseded by TestNode1DragKeepsNeighbors23Fixed and TestNode1DragEqualRadii above,
// which assert the new gate-pattern behavior directly.

// pollNode9EqualRadii waits until dist(9,3)==dist(9,6) within eps and returns both.
func pollNode9EqualRadii(t *testing.T, md *MoveDispatch) (d9to3, d9to6 float64) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c9, _ := md.centerOfNode("9")
		c3, _ := md.centerOfNode("3")
		c6, _ := md.centerOfNode("6")
		d9to3 = cart2polar(c3.sub(c9)).R
		d9to6 = cart2polar(c6.sub(c9)).R
		if math.Abs(d9to3-d9to6) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 9 did not converge to equal radii: d9to3=%v d9to6=%v", d9to3, d9to6)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNode9DragEmitsSelfTrigger is the RED proof for routing node 9's DIRECT drag
// through the decentralized goroutine-message path (node9-decentralized-gate.md,
// mirroring node5-decentralized-cascade.md's rootMoveViaMessages for nodes 5/2): a
// node-9 drag must route a self-initiated Trigger (SenderID=="") to node 9's OWN
// inbox via sendMove, and must NOT send any Equalize/Trigger message to any OTHER
// node (in particular neighbors 3 and 6, which must never move or receive a
// message). Against the CURRENT central rootMove path (case "9" runs synchronously
// on the drag call stack, never touching sendMove for Equalize/Trigger kinds), this
// test is RED: zero messages are tapped, so found9Trigger stays false.
func TestNode9DragEmitsSelfTrigger(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindDrag {
			return
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID, targetC: msg.TargetC})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target := vec3{55, 30, -5}
	if ok := md.RootMove("9", target); !ok {
		t.Fatalf("RootMove(9, %v) = false", target)
	}
	pollNode9EqualRadii(t, md)
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	found9Trigger := false
	for _, m := range trace {
		if m.destID == "9" && m.kind == moveMsgKindDrag && m.senderID == "" {
			found9Trigger = true
		}
		if m.destID == "3" || m.destID == "6" {
			t.Errorf("neighbor %s must never receive an Equalize/Trigger message; got %+v", m.destID, m)
		}
	}
	if !found9Trigger {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 9; got %+v", trace)
	}
}

// TestNode9DragEqualRadiiNeighborsFixed is the behavior-preserving safety net: after
// dragging node 9, dist(9,3)==dist(9,6) within eps, and neighbors 3 and 6 do not
// move. Holds both before and after routing node 9's drag through the decentralized
// message path.
func TestNode9DragEqualRadiiNeighborsFixed(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c3Before, _ := md.centerOfNode("3")
	c6Before, _ := md.centerOfNode("6")

	target := vec3{55, 30, -5}
	if ok := md.RootMove("9", target); !ok {
		t.Fatalf("RootMove(9, %v) = false", target)
	}
	d9to3, d9to6 := pollNode9EqualRadii(t, md)
	time.Sleep(20 * time.Millisecond)

	const eps = 1e-6
	c3After, _ := md.centerOfNode("3")
	c6After, _ := md.centerOfNode("6")
	if got := c3After.sub(c3Before).length(); got > eps {
		t.Fatalf("node 3 moved: before=%v after=%v delta=%v", c3Before, c3After, got)
	}
	if got := c6After.sub(c6Before).length(); got > eps {
		t.Fatalf("node 6 moved: before=%v after=%v delta=%v", c6Before, c6After, got)
	}
	t.Logf("final: d9to3=%v d9to6=%v", d9to3, d9to6)
}

// pollNode10EqualRadii waits until dist(10,6)==dist(10,8) within eps and returns both.
func pollNode10EqualRadii(t *testing.T, md *MoveDispatch) (d10to6, d10to8 float64) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c10, _ := md.centerOfNode("10")
		c6, _ := md.centerOfNode("6")
		c8, _ := md.centerOfNode("8")
		d10to6 = cart2polar(c6.sub(c10)).R
		d10to8 = cart2polar(c8.sub(c10)).R
		if math.Abs(d10to6-d10to8) <= eps {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 10 did not converge to equal radii: d10to6=%v d10to8=%v", d10to6, d10to8)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNode10DragEmitsSelfTrigger is the RED proof for routing node 10's DIRECT drag
// through the decentralized goroutine-message path (mirroring
// TestNode9DragEmitsSelfTrigger / node9-decentralized-gate.md): a node-10 drag must
// route a self-initiated Trigger (SenderID=="") to node 10's OWN inbox via sendMove,
// and must NOT send any Equalize/Trigger message to any OTHER node (in particular
// neighbors 6 and 8, which must never move or receive a message). Against the CURRENT
// central rootMove path (case "10" runs synchronously on the drag call stack, never
// touching sendMove for Equalize/Trigger kinds), this test is RED: zero messages are
// tapped, so found10Trigger stays false.
func TestNode10DragEmitsSelfTrigger(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindDrag {
			return
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID, targetC: msg.TargetC})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target := vec3{-30, 45, 5}
	if ok := md.RootMove("10", target); !ok {
		t.Fatalf("RootMove(10, %v) = false", target)
	}
	pollNode10EqualRadii(t, md)
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	found10Trigger := false
	for _, m := range trace {
		if m.destID == "10" && m.kind == moveMsgKindDrag && m.senderID == "" {
			found10Trigger = true
		}
		if m.destID == "6" || m.destID == "8" {
			t.Errorf("neighbor %s must never receive an Equalize/Trigger message; got %+v", m.destID, m)
		}
	}
	if !found10Trigger {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 10; got %+v", trace)
	}
}

// TestNode10DragEqualRadiiNeighborsFixed is the behavior-preserving safety net: after
// dragging node 10, dist(10,6)==dist(10,8) within eps, and neighbors 6 and 8 do not
// move. Holds both before and after routing node 10's drag through the decentralized
// message path.
func TestNode10DragEqualRadiiNeighborsFixed(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c6Before, _ := md.centerOfNode("6")
	c8Before, _ := md.centerOfNode("8")

	target := vec3{-30, 45, 5}
	if ok := md.RootMove("10", target); !ok {
		t.Fatalf("RootMove(10, %v) = false", target)
	}
	d10to6, d10to8 := pollNode10EqualRadii(t, md)
	time.Sleep(20 * time.Millisecond)

	const eps = 1e-6
	c6After, _ := md.centerOfNode("6")
	c8After, _ := md.centerOfNode("8")
	if got := c6After.sub(c6Before).length(); got > eps {
		t.Fatalf("node 6 moved: before=%v after=%v delta=%v", c6Before, c6After, got)
	}
	if got := c8After.sub(c8Before).length(); got > eps {
		t.Fatalf("node 8 moved: before=%v after=%v delta=%v", c8Before, c8After, got)
	}
	t.Logf("final: d10to6=%v d10to8=%v", d10to6, d10to8)
}

// pollNode6CascadeConverged waits until a node-6 drag's full cascade has settled: node 6
// at target, node 9 equidistant from 3 and 6, node 10 equidistant from 6 and 8, and node 5
// equidistant (from node 2) to node 2's distance to node 6.
func pollNode6CascadeConverged(t *testing.T, md *MoveDispatch, target vec3) {
	t.Helper()
	const eps = 1e-6
	deadline := time.Now().Add(2 * time.Second)
	for {
		c6, ok6 := md.centerOfNode("6")
		c9, _ := md.centerOfNode("9")
		c10, _ := md.centerOfNode("10")
		c3, _ := md.centerOfNode("3")
		c8, _ := md.centerOfNode("8")
		c2, _ := md.centerOfNode("2")
		c5, _ := md.centerOfNode("5")
		if ok6 && math.Abs(c6.X-target.X) <= eps && math.Abs(c6.Y-target.Y) <= eps && math.Abs(c6.Z-target.Z) <= eps {
			d9to3 := cart2polar(c3.sub(c9)).R
			d9to6 := cart2polar(c6.sub(c9)).R
			d10to6 := cart2polar(c6.sub(c10)).R
			d10to8 := cart2polar(c8.sub(c10)).R
			d2to5 := cart2polar(c5.sub(c2)).R
			d2to6 := cart2polar(c6.sub(c2)).R
			if math.Abs(d9to3-d9to6) <= eps && math.Abs(d10to6-d10to8) <= eps && math.Abs(d2to5-d2to6) <= eps {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("node 6 cascade did not converge: c6=%v target=%v", c6, target)
		}
		time.Sleep(time.Millisecond)
	}
}

// placeAtDistanceFromBothRef is an independent reimplementation of
// MoveDispatch.placeAtDistanceFromBoth used by the tests below to derive the EXPECTED
// landing point for node 9/10, without calling the package's own internal helper.
func placeAtDistanceFromBothRef(cur, a, b vec3, d float64) vec3 {
	ab := b.sub(a)
	half := ab.length() / 2
	if d < half {
		d = half
	}
	m := a.add(b).scale(0.5)
	nhat := ab.scale(1 / (2 * half))
	q := cur.sub(nhat.scale(cur.sub(m).dot(nhat)))
	dir := q.sub(m)
	rho := math.Sqrt(math.Max(0, d*d-half*half))
	if dir.length() == 0 {
		return m
	}
	return m.add(dir.normalize().scale(rho))
}

// TestNode6DragPlaces9And10AndMoves5 is the BEHAVIOR-PRESERVING SAFETY NET for node 6's
// drag cascade: it must hold both before AND after converting node 6's drag to the
// decentralized goroutine-message path. Node 6 lands at the raw drag target (free move,
// no re-solve). Its two gate neighbors 9 and 10 each land at the shortest of node 6's two
// c-distances (to 9, to 10) from BOTH their fixed neighbors, making all four edges
// (9-3, 9-6, 6-10, 10-8) equal. Node 2 (untouched directly) re-equalizes its remaining
// peer, node 5, to the fresh dist(2,6) — node 2 itself, node 1, and node 3 do not move.
func TestNode6DragPlaces9And10AndMoves5(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	c3Before, _ := md.centerOfNode("3")
	c8Before, _ := md.centerOfNode("8")
	c9Before, _ := md.centerOfNode("9")
	c10Before, _ := md.centerOfNode("10")
	c2Before, _ := md.centerOfNode("2")
	c1Before, _ := md.centerOfNode("1")

	target6 := vec3{X: -36.872596156504756, Y: -4.348074526720876, Z: -24.181828865130925}

	const step = localStepR
	cTo9 := math.Round(c9Before.sub(target6).length() / step)
	cTo10 := math.Round(c10Before.sub(target6).length() / step)
	shortest := math.Min(cTo9, cTo10)
	d := shortest * step

	expected9 := placeAtDistanceFromBothRef(c9Before, c3Before, target6, d)
	expected10 := placeAtDistanceFromBothRef(c10Before, target6, c8Before, d)

	if !md.RootMove("6", target6) {
		t.Fatal("RootMove returned false for known node")
	}
	pollNode6CascadeConverged(t, md, target6)
	time.Sleep(20 * time.Millisecond)

	const eps = 1e-6
	c6After, _ := md.centerOfNode("6")
	c9After, _ := md.centerOfNode("9")
	c10After, _ := md.centerOfNode("10")
	c3After, _ := md.centerOfNode("3")
	c8After, _ := md.centerOfNode("8")
	c2After, _ := md.centerOfNode("2")
	c5After, _ := md.centerOfNode("5")
	c1After, _ := md.centerOfNode("1")

	if got := c9After.sub(expected9).length(); got > eps {
		t.Fatalf("node 9 landing = %+v, want equal-distance-circle solve %+v (delta=%v)", c9After, expected9, got)
	}
	if got := c10After.sub(expected10).length(); got > eps {
		t.Fatalf("node 10 landing = %+v, want equal-distance-circle solve %+v (delta=%v)", c10After, expected10, got)
	}

	dist9To3 := c9After.sub(c3After).length()
	dist9To6 := c9After.sub(c6After).length()
	dist10To6 := c10After.sub(c6After).length()
	dist10To8 := c10After.sub(c8After).length()
	if math.Abs(dist9To3-dist9To6) > eps {
		t.Fatalf("node 9's radii to 3 and 6 not equal: %v vs %v", dist9To3, dist9To6)
	}
	if math.Abs(dist10To6-dist10To8) > eps {
		t.Fatalf("node 10's radii to 6 and 8 not equal: %v vs %v", dist10To6, dist10To8)
	}
	if math.Abs(dist9To3-dist10To8) > eps {
		t.Fatalf("dist(9,3)=%v != dist(10,8)=%v, want all four edges equal", dist9To3, dist10To8)
	}
	if math.Abs(dist9To3-d) > eps {
		t.Fatalf("dist(9,3)=%v, want shortest-c distance d=%v", dist9To3, d)
	}

	if got := c3After.sub(c3Before).length(); got > eps {
		t.Fatalf("node 3 moved: before=%v after=%v delta=%v", c3Before, c3After, got)
	}
	if got := c8After.sub(c8Before).length(); got > eps {
		t.Fatalf("node 8 moved: before=%v after=%v delta=%v", c8Before, c8After, got)
	}
	if got := c2After.sub(c2Before).length(); got > eps {
		t.Fatalf("node 2 moved: before=%v after=%v delta=%v", c2Before, c2After, got)
	}
	if got := c1After.sub(c1Before).length(); got > eps {
		t.Fatalf("node 1 moved: before=%v after=%v delta=%v", c1Before, c1After, got)
	}

	d2to5 := cart2polar(c5After.sub(c2After)).R
	d2to6 := cart2polar(c6After.sub(c2After)).R
	if math.Abs(d2to5-d2to6) > eps {
		t.Fatalf("node 5 not at dist(2,6): d2to5=%v d2to6=%v", d2to5, d2to6)
	}
	t.Logf("final: d=%v dist9To3=%v dist10To6=%v d2to5=%v d2to6=%v", d, dist9To3, dist10To6, d2to5, d2to6)
}

// TestNode6DragEmitsDecentralizedMessages is the TRACE proof (node6-decentralized.md,
// widened by node6-drag-decentralized.md): a node-6 drag must route the drag ITSELF
// (moveMsgKindDrag, SenderID=="") to node 6's own inbox — replacing the old central
// commit + separate self-initiated Trigger send, now that node 6 commits AND
// self-triggers on its own goroutine in one handler (handleTrigger is called
// directly, not re-routed through sendMove, since it's already running on node 6's
// own goroutine) — plus a GatePlace message to BOTH node 9 and node 10, and a
// forwarded Trigger (SenderID=="6") to node 2 — all via sendMove — and must never
// send an Equalize/Trigger/GatePlace/Drag message that MOVES node 2 or node 6 itself
// beyond that one initiating Drag (node 2 only receives the forwarded Trigger, which
// repositions its peer 5, not node 2).
func TestNode6DragEmitsDecentralizedMessages(t *testing.T) {
	md := newFullChainDispatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	var mu sync.Mutex
	var recorded []tappedMsg
	md.SetMsgTap(func(destID string, msg moveMsg) {
		if msg.Kind != moveMsgKindEqualize && msg.Kind != moveMsgKindTrigger && msg.Kind != moveMsgKindGatePlace && msg.Kind != moveMsgKindDrag {
			return
		}
		mu.Lock()
		recorded = append(recorded, tappedMsg{destID: destID, kind: msg.Kind, senderID: msg.SenderID, targetC: msg.TargetC})
		mu.Unlock()
	})
	defer md.SetMsgTap(nil)

	target6 := vec3{X: 45, Y: 20, Z: -5}
	if ok := md.RootMove("6", target6); !ok {
		t.Fatalf("RootMove(6, %v) = false", target6)
	}
	pollNode6CascadeConverged(t, md, target6)
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	trace := append([]tappedMsg(nil), recorded...)
	mu.Unlock()

	t.Logf("recorded %d Equalize/Trigger/GatePlace messages:", len(trace))
	for _, m := range trace {
		t.Logf("  dest=%s kind=%s senderID=%q targetC=%v", m.destID, m.kind, m.senderID, m.targetC)
	}

	has := func(dest, kind, senderID string) bool {
		for _, m := range trace {
			if m.destID == dest && m.kind == kind && m.senderID == senderID {
				return true
			}
		}
		return false
	}

	if !has("6", moveMsgKindDrag, "") {
		t.Errorf("expected a self-routed Drag (SenderID==\"\") to node 6; got %+v", trace)
	}
	if !has("9", moveMsgKindGatePlace, "6") {
		t.Errorf("expected a GatePlace message (senderID=6) to node 9; got %+v", trace)
	}
	if !has("10", moveMsgKindGatePlace, "6") {
		t.Errorf("expected a GatePlace message (senderID=6) to node 10; got %+v", trace)
	}
	if !has("2", moveMsgKindTrigger, "6") {
		t.Errorf("expected a Trigger forwarded to node 2 (senderID=6); got %+v", trace)
	}
	if !has("5", moveMsgKindEqualize, "") {
		t.Errorf("expected an Equalize routed to node 5; got %+v", trace)
	}

	for _, m := range trace {
		if m.destID == "6" && m.kind != moveMsgKindDrag {
			t.Errorf("node 6 must never be moved by another node's message; got %+v", m)
		}
		if m.destID == "2" && m.kind != moveMsgKindTrigger {
			t.Errorf("node 2 must never be moved (only re-triggered); got %+v", m)
		}
	}

	const wantCount = 5
	if len(trace) != wantCount {
		t.Errorf("expected exactly %d messages, got %d: %+v", wantCount, len(trace), trace)
	}
}
