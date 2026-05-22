package ChainInhibitorNode

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ChainInhibitorNode struct {
	Fire                       func()
	HeldValue                  int `wire:"data.initialSlots.held"`
	FromPrevChainInhibitorNode *Wiring.In
	ToNext                     Wiring.OutMulti
}

func (in *ChainInhibitorNode) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			for _, out := range in.ToNext {
				out.TrySend(in.HeldValue)
			}
			in.HeldValue = value
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &ChainInhibitorNode{} })
}
