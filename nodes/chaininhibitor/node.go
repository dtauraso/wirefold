package chaininhibitor

import (
	"context"
	"sync"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire                       func()
	Held                       int `wire:"data.state"`
	FromPrevChainInhibitorNode *Wiring.In
	ToNext                     Wiring.OutMulti
}

func (in *Node) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevChainInhibitorNode.Done()
			var wg sync.WaitGroup
			for _, out := range in.ToNext {
				wg.Add(1)
				go func(o *Wiring.Out) {
					defer wg.Done()
					if o.TrySend(in.Held) {
						if o.Gated() {
							o.WaitConsumed()
						}
					}
				}(out)
			}
			wg.Wait()
			in.Held = value
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &Node{} })
}
