package InputNode

import (
	"context"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type InputNode struct {
	Id         int
	Name       string
	Trace      *T.Trace
	Init       []int `wire:"data.init"`
	ToReadGate chan<- int
}

func (n *InputNode) Update(ctx context.Context) {
	for i := 0; i < len(n.Init); {
		select {
		case <-ctx.Done():
			return
		default:
		}
		select {
		case n.ToReadGate <- n.Init[i]:
			n.Trace.Fire(n.Name)
			n.Trace.Send(n.Name, "ToReadGate", n.Init[i])
			i++
		default:
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &InputNode{} })
}
