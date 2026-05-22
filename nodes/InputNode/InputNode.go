package InputNode

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type InputNode struct {
	Fire       func()
	Init       []int `wire:"data.init"`
	ToReadGate *Wiring.Out
}

func (n *InputNode) Update(ctx context.Context) {
	for i := 0; i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		if n.ToReadGate.TrySend(n.Init[i]) {
			n.Fire()
			i++
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &InputNode{} })
}
