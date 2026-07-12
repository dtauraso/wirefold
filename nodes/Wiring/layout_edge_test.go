// layout_edge_test.go verifies the hidden layout graph (layout_edge.go): the
// LayoutPort propagation primitive on its own (a chain and a cycle), plus that
// the loader's buildLayoutEdges phase mirrors the domain spec edges onto it
// one-for-one (source -> target).

package Wiring

import (
	"testing"
	"time"
)

// drainLayoutPort blocks (with a bounded deadline) until p's inbound channel
// has a message, calls Handle on it, and returns the message. Used to walk a
// propagation wave one hop at a time in test code (no busy loop).
func drainLayoutPort(t *testing.T, p *LayoutPort) LayoutMsg {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := p.TryRecv(); ok {
			p.Handle(msg)
			return msg
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("layout port %s: no message received before deadline", p.id)
	return LayoutMsg{}
}

// TestLayoutPortChainPropagates builds a 3-node chain (a -> b -> c) directly
// out of LayoutPort and verifies an Inject at a reaches b and c.
func TestLayoutPortChainPropagates(t *testing.T) {
	a := NewLayoutPort("a")
	b := NewLayoutPort("b")
	c := NewLayoutPort("c")
	// SLICE 2: Handle only forwards past a radius-forwarding node (HoldNewSendOld). This test is
	// about the chain-propagation mechanics, not the radius-forwarder gate, so mark every
	// node a radius-forwarding node directly (same-package field access).
	a.forwardsRadius, b.forwardsRadius, c.forwardsRadius = true, true, true
	a.connectTo(b)
	b.connectTo(c)

	a.Inject(LayoutMsg{Visited: map[string]bool{}})

	drainLayoutPort(t, a) // a handles + forwards to b
	drainLayoutPort(t, b) // b handles + forwards to c

	msg, ok := c.TryRecv()
	if !ok {
		t.Fatal("c never received the propagated message")
	}
	if !msg.Visited["a"] || !msg.Visited["b"] {
		t.Fatalf("expected a and b marked visited by the time c receives it, got %+v", msg.Visited)
	}
}

// TestLayoutPortCycleTerminates builds a 3-node cycle (a -> b -> c -> a) and
// verifies propagation visits each node exactly once and does not hang: after
// c forwards back to a, a's Handle sees itself already visited and stops
// (no further forward), so the wave drains to quiescence.
func TestLayoutPortCycleTerminates(t *testing.T) {
	a := NewLayoutPort("a")
	b := NewLayoutPort("b")
	c := NewLayoutPort("c")
	// SLICE 2: mark every node a radius-forwarding node so Handle forwards (see the chain test's
	// comment above); this test is about cycle termination mechanics, not the gate.
	a.forwardsRadius, b.forwardsRadius, c.forwardsRadius = true, true, true
	a.connectTo(b)
	b.connectTo(c)
	c.connectTo(a)

	a.Inject(LayoutMsg{Visited: map[string]bool{}})

	drainLayoutPort(t, a) // a: visited={a}, forwards to b
	drainLayoutPort(t, b) // b: visited={a,b}, forwards to c

	msg, ok := c.TryRecv()
	if !ok {
		t.Fatal("c never received the propagated message")
	}
	c.Handle(msg) // c: visited={a,b,c}, forwards to a (already-visited a terminates)

	// a receives the cycle-closing message and terminates it (does not re-forward).
	msg2, ok := a.TryRecv()
	if !ok {
		t.Fatal("a never received the cycle-closing message")
	}
	a.Handle(msg2)
	if !msg2.Visited["a"] || !msg2.Visited["b"] || !msg2.Visited["c"] {
		t.Fatalf("expected all three nodes marked visited by the time the cycle closes, got %+v", msg2.Visited)
	}

	// No further messages appear anywhere: the wave drained to quiescence.
	if _, ok := a.TryRecv(); ok {
		t.Fatal("a received an unexpected extra message; cycle did not terminate")
	}
	if _, ok := b.TryRecv(); ok {
		t.Fatal("b received an unexpected extra message; cycle did not terminate")
	}
	if _, ok := c.TryRecv(); ok {
		t.Fatal("c received an unexpected extra message; cycle did not terminate")
	}
}

// TestBuildLayoutEdgesMirrorsSpecEdges builds a buildCtx from a small spec
// (three nodes, a fan-out: n1 -> n2, n1 -> n3) and asserts buildLayoutEdges
// produces a LayoutPort per node whose outbound set mirrors the domain edges
// exactly (same source -> target connectivity, one hidden edge per domain
// edge).
func TestBuildLayoutEdgesMirrorsSpecEdges(t *testing.T) {
	spec := topoSpec{
		Nodes: []specNode{
			{ID: "n1", Type: "Input"},
			{ID: "n2", Type: "HoldNewSendOld"},
			{ID: "n3", Type: "Hold"},
		},
		Edges: []specEdge{
			{Label: "e1", Kind: "data", Source: "n1", SourceHandle: "ToHoldNewSendOld", Target: "n2", TargetHandle: "FromPrevHoldNewSendOldNode"},
			{Label: "e2", Kind: "data", Source: "n1", SourceHandle: "ToExcitatory", Target: "n3", TargetHandle: "In"},
		},
	}

	b := &buildCtx{spec: spec}
	b.buildLayoutEdges()

	if len(b.layoutPorts) != 3 {
		t.Fatalf("expected 3 layout ports (one per node), got %d", len(b.layoutPorts))
	}
	n1 := b.layoutPorts["n1"]
	n2 := b.layoutPorts["n2"]
	n3 := b.layoutPorts["n3"]
	if n1 == nil || n2 == nil || n3 == nil {
		t.Fatalf("missing a layout port: n1=%v n2=%v n3=%v", n1, n2, n3)
	}
	// SLICE 2: n1's spec type ("Input") is not a radius-forwarding node, so Handle would not forward
	// past it. This test is only about hidden-edge fan-out mirroring the domain edges,
	// not the radius-forwarder gate, so mark it a radius-forwarding node directly (same-package access).
	n1.forwardsRadius = true
	if len(n1.out) != 2 {
		t.Fatalf("n1 should fan out on the hidden layout graph to both n2 and n3, got %d outbound edges", len(n1.out))
	}
	if len(n2.out) != 0 || len(n3.out) != 0 {
		t.Fatalf("n2/n3 have no domain out-edges in this spec, so no hidden out-edges either: n2=%d n3=%d", len(n2.out), len(n3.out))
	}

	// Inject at n1 and verify BOTH n2 and n3 receive it (mirrors the domain
	// fan-out one-for-one).
	n1.Inject(LayoutMsg{Visited: map[string]bool{}})
	drainLayoutPort(t, n1)

	if _, ok := n2.TryRecv(); !ok {
		t.Fatal("n2 did not receive the layout message propagated from n1 over the mirrored edge e1")
	}
	if _, ok := n3.TryRecv(); !ok {
		t.Fatal("n3 did not receive the layout message propagated from n1 over the mirrored edge e2")
	}
}
