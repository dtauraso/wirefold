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

// popEnd reads and removes the END element of working, refilling from backup
// when working empties. working/backup are the double-buffer: each is a fresh
// copy of init, and end-popping [1,0] yields 0 then 1. Returns the popped value.
// Caller guarantees len(working) > 0 (refill keeps it non-empty when init != nil).
func popEnd(working, backup *[]int, init []int) int {
	v := (*working)[len(*working)-1]
	*working = (*working)[:len(*working)-1]
	if len(*working) == 0 {
		// Refill: the top row (backup) slides down to become the new working
		// row; a fresh top row appears.
		*working = *backup
		*backup = append([]int(nil), init...)
	}
	return v
}

func (n *Node) Update(ctx context.Context) {
	if n.EmitGeometry != nil {
		n.EmitGeometry()
	}
	if len(n.Init) == 0 {
		return
	}

	// Double-buffer derived from the spec init: working (bottom row) and backup
	// (top row), each a fresh copy of init. The working array IS the emission
	// state — no persistent index. End-popping is the read: end of working is
	// the next value out.
	init := append([]int(nil), n.Init...)
	working := append([]int(nil), init...)
	backup := append([]int(nil), init...)

	if n.FeedbackIn.Wired() {
		// Feedback ring: SEED then READ. The first pop+send is unconditional so
		// the ring is bootstrapped (no t=0 deadlock) — without it, nothing emits
		// and the downstream ChainInhibitor never produces a feedback step.
		// Thereafter FeedbackIn gates pops: s == 1 -> pop the end and send;
		// s == 0 -> hold (send nothing this step).
		seeded := false
		for {
			if ctx.Err() != nil {
				return
			}
			s := 1
			if seeded {
				// READ: block until ChainInhibitor sends the step on FeedbackIn.
				step, ok := n.FeedbackIn.TryRecv()
				if !ok {
					return
				}
				n.FeedbackIn.Done()
				s = step
			}
			seeded = true

			if s != 1 {
				// Hold: send nothing this step.
				continue
			}

			n.Fire()
			v := popEnd(&working, &backup, init)
			if n.ToReadGate.Gated() {
				if n.ToReadGate.TrySend(v) {
					if !n.ToReadGate.WaitConsumed() {
						return
					}
				}
			} else {
				n.ToReadGate.TryEmit(v)
			}
		}
	}

	// Plain emit path (FeedbackIn not wired): pop the end every iteration,
	// refilling on empty. With Repeat the buffer refills forever; without it,
	// emit exactly len(init) values (one working drain) then stop.
	emitted := 0
	for n.Repeat || emitted < len(init) {
		if ctx.Err() != nil {
			return
		}
		n.Fire()
		v := popEnd(&working, &backup, init)
		if n.ToReadGate.Gated() {
			if n.ToReadGate.TrySend(v) {
				if !n.ToReadGate.WaitConsumed() {
					return
				}
				emitted++
			}
		} else {
			// fire-and-forget: advance unconditionally after TryEmit (no wait).
			n.ToReadGate.TryEmit(v)
			emitted++
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
