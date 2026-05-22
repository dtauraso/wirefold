package input

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire       func()
	Init       []int `wire:"data.init"`
	ToReadGate *Wiring.Out
}

func (n *Node) Update(ctx context.Context) {
	for i := 0; i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		n.Fire()
		if n.ToReadGate.TrySend(n.Init[i]) {
			i++
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
