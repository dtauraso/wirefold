package ChainInhibitorNode

import (
	"context"
	"fmt"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type ChainInhibitorNode struct {
	Id                         int
	Name                       string
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
			fmt.Printf("%s: received %d (old=%d)\n", in.Name, value, in.HeldValue)
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
