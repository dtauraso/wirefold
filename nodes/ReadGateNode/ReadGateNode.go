package ReadGateNode

import (
	"context"
	"fmt"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ReadGateNode struct {
	Id                int
	Name              string
	Fire              func()
	Value             int
	HasValue          bool
	HasChainInhibitor bool
	FromInput          *Wiring.In
	FromChainInhibitor *Wiring.In
	ToChainInhibitor   *Wiring.Out
}

func (g *ReadGateNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasValue {
			if v, ok := g.FromInput.TryRecv(); ok {
				g.Value = v
				g.HasValue = true
			}
		}

		if !g.HasChainInhibitor {
			if _, ok := g.FromChainInhibitor.TryRecv(); ok {
				g.HasChainInhibitor = true
			}
		}

		if g.HasValue && g.HasChainInhibitor {
			fmt.Printf("%s: value=%d → %d\n", g.Name, g.Value, g.Value)
			if g.ToChainInhibitor.TrySend(g.Value) {
				g.Fire()
				g.HasValue = false
				g.HasChainInhibitor = false
			}
		}
	}
}

func init() {
	Wiring.Register("ReadGate", func() any { return &ReadGateNode{} })
}
