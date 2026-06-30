package windowandinhibitrightgate

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a WindowAndInhibitRightGate: the RIGHT input is NOT-inverted on capture
// (1→0, 0→1). The gate fires 1 iff left AND (NOT right).
// All shared loop logic lives in gatecommon.RunGate; this package owns the
// struct layout (required for gen-node-defs port discovery) and the init call.
type Node struct {
	Fire           func()
	EmitGeometry   func()
	EmitInputBeads func(left, right int)
	// Now returns active-elapsed sim time (pause-aware); nil → wall-clock fallback.
	Now       func() time.Duration
	WaitUntil func(ctx context.Context, target time.Duration) error
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
	gatecommon.RunGate(ctx, &gatecommon.GateNode{
		Fire:           g.Fire,
		EmitGeometry:   g.EmitGeometry,
		EmitInputBeads: g.EmitInputBeads,
		Now:            g.Now,
		WaitUntil:      g.WaitUntil,
		FromLeft:       g.FromLeft,
		FromRight:      g.FromRight,
		ToPassed:       g.ToPassed,
	}, false /* invertLeft */)
}

func init() {
	Wiring.Register("WindowAndInhibitRightGate", func() any { return &Node{} })
}
