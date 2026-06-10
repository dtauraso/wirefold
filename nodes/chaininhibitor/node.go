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
	FeedbackOut                *Wiring.Out
}

func (in *Node) Update(ctx context.Context) {
	// Initialize the compare value for feedback detection.
	// -1 is the sentinel meaning "no value seen yet"; real values are non-negative
	// indices, so -1 never collides with a legitimate Init index.
	held := -1

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Hold: if any output wire still has a bead in flight or holding an
		// unconsumed pulse, park until it clears — do not consume the input
		// pulse yet. This prevents drops when output transit time exceeds the
		// loop's input rate.
		anyOccupied := false
		for _, out := range in.ToNext {
			if out.Occupied() {
				anyOccupied = true
				break
			}
		}
		if anyOccupied {
			continue
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevChainInhibitorNode.Done()

			if in.FeedbackOut.Wired() {
				// Send feedback step BEFORE forwarding on ToNext so the Input
				// node unblocks (ordered: feedback send precedes next recv).
				// Step is 1 when the value changes (advance index), 0 when it
				// repeats (hold index). held == -1 on the first recv so the
				// first value always counts as a change.
				var step int
				if value != held {
					step = 1
				}
				held = value
				if in.FeedbackOut.Gated() {
					if in.FeedbackOut.TrySend(step) {
						in.FeedbackOut.WaitConsumed()
					}
				} else {
					in.FeedbackOut.TryEmit(step)
				}
				// Forward the current held value on the downstream chain.
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
			} else {
				// FeedbackOut not wired (e.g. i1): existing behavior unchanged.
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
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &Node{} })
}
