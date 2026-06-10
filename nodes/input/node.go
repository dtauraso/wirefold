package input

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire         func()
	EmitGeometry func()
	Init        []int `wire:"data.init"`
	Repeat      bool  `wire:"data.repeat"`
	ToReadGate  *Wiring.Out
	FeedbackIn  *Wiring.In
}

func (n *Node) Update(ctx context.Context) {
	if n.EmitGeometry != nil {
		n.EmitGeometry()
	}
	if len(n.Init) == 0 {
		return
	}

	if n.FeedbackIn.Wired() {
		// Feedback ring: SEND then READ each iteration so the first emit (i=0)
		// is the ring seed and there is no t=0 deadlock. FeedbackIn delivers the
		// step (0 = same index, 1 = advance) from the downstream ChainInhibitor.
		i := 0
		for n.Repeat || i < len(n.Init) {
			if ctx.Err() != nil {
				return
			}
			n.Fire()
			// SEND: place current Init value before waiting for feedback.
			if n.ToReadGate.Gated() {
				if n.ToReadGate.TrySend(n.Init[i%len(n.Init)]) {
					if !n.ToReadGate.WaitConsumed() {
						return
					}
				}
			} else {
				n.ToReadGate.TryEmit(n.Init[i%len(n.Init)])
			}
			// READ: block until ChainInhibitor sends the step on FeedbackIn.
			s, ok := n.FeedbackIn.TryRecv()
			if !ok {
				return
			}
			n.FeedbackIn.Done()
			i = (i + s) % len(n.Init)
		}
		return
	}

	// Plain emit path (FeedbackIn not wired): existing behavior preserved.
	for i := 0; n.Repeat || i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		n.Fire()
		if n.ToReadGate.Gated() {
			if n.ToReadGate.TrySend(n.Init[i%len(n.Init)]) {
				if !n.ToReadGate.WaitConsumed() {
					return
				}
				i++
				if !n.Repeat && i >= len(n.Init) {
					return
				}
			}
		} else {
			// fire-and-forget: advance unconditionally after TryEmit (no wait).
			n.ToReadGate.TryEmit(n.Init[i%len(n.Init)])
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
