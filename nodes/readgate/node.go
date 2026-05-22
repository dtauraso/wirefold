package readgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire              func()
	Value             int
	HasValue          bool
	HasChainInhibitor bool
	FromInput          *Wiring.In
	FromChainInhibitor *Wiring.In
	ToChainInhibitor   *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
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
			g.Fire()
			if g.ToChainInhibitor.TrySend(g.Value) {
				g.HasValue = false
				g.HasChainInhibitor = false
			}
		}
	}
}

func init() {
	Wiring.Register("ReadGate", func() any { return &Node{} })
}
