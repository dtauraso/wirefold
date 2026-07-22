// fixture_kinds_test.go — shared minimal source/sink node kinds used across the Wiring
// package's tests: SrcNode is a one-Out source, SinkNode a one-In sink. Every test that
// uses them wires a SINGLE edge into the sink's one port (not fan-in). Registered once here
// because ~10 test topologies reference the "SrcNode"/"SinkNode" type strings.

package Wiring

import "context"

// srcNode is a minimal source kind with one paced Out. Position writes route through
// nodeMover's own goroutine (node_move.go), so no layout plumbing is needed here.
type srcNode struct {
	LayoutHolder
	Out *Out
}

func (n *srcNode) Update(ctx context.Context) {
	<-ctx.Done()
}

// sinkNode is a minimal sink kind with one paced In.
type sinkNode struct {
	LayoutHolder
	In *In
}

func (n *sinkNode) Update(ctx context.Context) {
	<-ctx.Done()
}

func init() {
	Register("SrcNode", func() any { return &srcNode{} })
	Register("SinkNode", func() any { return &sinkNode{} })
}
