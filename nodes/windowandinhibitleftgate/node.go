package windowandinhibitleftgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a WindowAndInhibitLeftGate: the LEFT input is NOT-inverted on capture
// (1→0, 0→1). The gate fires 1 iff (NOT left) AND right.
// All shared loop logic lives in gatecommon.RunGate; this package owns the
// struct layout (required for gen-node-defs port discovery) and the init call.
// GateNode is embedded so its port fields (FromLeft, FromRight, ToPassed) are
// promoted and discovered by reflectPorts (which recurses into anonymous fields).
type Node struct {
	gatecommon.GateNode
	// ToPulse3 is a declared output to a Pulse node (instance 3). Intentionally
	// inert (no send logic) — see 9To3 edge task.
	ToPulse3 *Wiring.Out
	// ToPulse6 is a declared output to a Pulse node (instance 6). Intentionally
	// inert (no send logic) — see 9To6 edge task.
	ToPulse6 *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
	gatecommon.RunGate(ctx, &g.GateNode, true /* invertLeft */)
}

func init() {
	Wiring.Register("WindowAndInhibitLeftGate", func() any { return &Node{} })
}
