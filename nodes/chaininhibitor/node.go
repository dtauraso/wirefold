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

		// Hold: if any output wire still has a bead in flight, park until it
		// clears — do not consume the input pulse yet. This prevents drops when
		// output transit time exceeds the loop's input rate.
		anyInFlight := false
		for _, out := range in.ToNext {
			if out.InFlight() {
				anyInFlight = true
				break
			}
		}
		if anyInFlight {
			continue
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevChainInhibitorNode.Done()
			var wg sync.WaitGroup
			for _, out := range in.ToNext {
				wg.Add(1)
				go func(o *Wiring.Out) {
					defer wg.Done()
					if o.Gated() {
						if o.TrySend(in.Held) {
							o.WaitConsumed()
						}
					} else {
						o.TryEmit(in.Held)
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
