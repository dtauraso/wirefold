package ReadGateNode

import (
	"context"
	"fmt"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ReadGateNode struct {
	Id                int
	Name              string
	Trace             *T.Trace
	Value             int
	HasValue          bool
	HasChainInhibitor bool
	FromInput          <-chan int
	FromChainInhibitor <-chan int
	ToChainInhibitor   chan<- int
}

func (g *ReadGateNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasValue {
			select {
			case v := <-g.FromInput:
				g.Value = v
				g.HasValue = true
				g.Trace.Recv(g.Name, "FromInput", v)
			default:
			}
		}

		if !g.HasChainInhibitor {
			select {
			case v := <-g.FromChainInhibitor:
				g.HasChainInhibitor = true
				g.Trace.Recv(g.Name, "FromChainInhibitor", v)
			default:
			}
		}

		if g.HasValue && g.HasChainInhibitor {
			fmt.Printf("%s: value=%d → %d\n", g.Name, g.Value, g.Value)
			g.Trace.Fire(g.Name)
			select {
			case g.ToChainInhibitor <- g.Value:
			default:
			}
			g.Trace.Send(g.Name, "ToChainInhibitor", g.Value)
			g.HasValue = false
			g.HasChainInhibitor = false
		}
	}
}

func init() {
	Wiring.Register("ReadGate", func() any { return &ReadGateNode{} })
}
