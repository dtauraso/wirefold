package input

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire       func()
	Init       []int `wire:"data.init"`
	Repeat     bool  `wire:"data.repeat"`
	ToReadGate *Wiring.Out
}

func (n *Node) Update(ctx context.Context) {
	for i := 0; n.Repeat || i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		if len(n.Init) == 0 {
			return
		}
		n.Fire()
		if n.ToReadGate.TrySend(n.Init[i%len(n.Init)]) {
			if n.ToReadGate.Gated() {
				if !n.ToReadGate.WaitConsumed() {
					return
				}
			}
			i++
			if !n.Repeat && i >= len(n.Init) {
				return
			}
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
