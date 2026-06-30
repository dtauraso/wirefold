package windowandinhibitrightgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a WindowAndInhibitRightGate: the RIGHT input is NOT-inverted on capture
// (1→0, 0→1). The gate fires 1 iff left AND (NOT right).
// All shared loop logic lives in gatecommon.RunGate; this package owns the
// struct layout (required for gen-node-defs port discovery) and the init call.
// GateNode is embedded so its port fields (FromLeft, FromRight, ToPassed) are
// promoted and discovered by reflectPorts (which recurses into anonymous fields).
type Node struct {
	gatecommon.GateNode
}

func (g *Node) Update(ctx context.Context) {
	gatecommon.RunGate(ctx, &g.GateNode, false /* invertLeft */)
}

func init() {
	Wiring.Register("WindowAndInhibitRightGate", func() any { return &Node{} })
}
