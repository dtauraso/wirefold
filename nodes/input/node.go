package input

import (
	"context"
	"sync"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire         func()
	EmitGeometry func()
	// EmitNodeBeads streams the live interior buffer (2x2 grid) as node-bead
	// events — one per present bead. Injected by Wiring.reflectBuild (captures this
	// node's geometry). Called whenever working/backup change so the emitted set
	// always reflects the live arrays. Discrete positions only this phase.
	EmitNodeBeads func(working, backup []int)
	// EmitRefillSlide runs the clock-paced animated refill: the OLD backup (top
	// row) slides DOWN into the working (bottom) row at human speed. Injected by
	// Wiring.reflectBuild (captures this node's id + geometry + the shared clock).
	// It blocks for the slide duration (pause-aware). nil on test builds without
	// injection — the caller then falls back to the instant refill. beads is the
	// OLD backup contents that become the new working row.
	EmitRefillSlide func(beads []int)
	Init        []int `wire:"data.init"`
	Repeat      bool  `wire:"data.repeat"`
	ToInhibitor *Wiring.Out
	// ToExcitatory fans the emitted value out to an Excitatory node (sample-and-hold). It is
	// optional: when unwired (Wired()==false) the emit is skipped so existing
	// topologies without an Excitatory are unaffected.
	ToExcitatory *Wiring.Out
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

	// emitBeads streams the live interior buffer as a discrete node-bead snapshot
	// (present beads only). Called on the initial full state and after every array
	// mutation (each pop, each refill) so the emitted set tracks working/backup.
	emitBeads := func() {
		if n.EmitNodeBeads != nil {
			n.EmitNodeBeads(working, backup)
		}
	}
	emitBeads() // initial full(4) state

	if n.FeedbackIn.Wired() {
		// Feedback ring: PEEK+SEND then READ. Sending does NOT deplete the
		// buffer — each iteration peeks the END of action (working) and launches
		// that bead; the buffer stays full (4) at rest. The FIRST send is just the
		// normal loop body (peek+send) running before any feedback is read, so the
		// ring self-starts with no special seed and no t=0 deadlock.
		//
		// After sending, READ node 2's feedback s on FeedbackIn:
		//   s == 1 -> POP the end (the "change the bead" action); refill on empty.
		//   s == 0 -> hold: do nothing, keep sending the same last bead next loop.
		for {
			if ctx.Err() != nil {
				return
			}

			// Guard: never peek an empty slice. Refill keeps action non-empty,
			// but be safe.
			if len(working) == 0 {
				working = backup
				backup = append([]int(nil), init...)
				emitBeads()
			}

			// PEEK the end (do NOT reslice) and SEND. Buffer unchanged.
			v := working[len(working)-1]
			n.Fire()
			// Node 1 initiates a goroutine per wired output so node 2
			// (ToInhibitor) and node 6 (ToExcitatory) get the same bead
			// concurrently. wg.Wait keeps node 1 paced and preserves the
			// feedback-ring ordering (the TryRecv below still runs after).
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { defer wg.Done(); n.ToInhibitor.EmitOneDriven(ctx, v) }()
			if n.ToExcitatory.Wired() {
				wg.Add(1)
				go func() { defer wg.Done(); n.ToExcitatory.EmitOneDriven(ctx, v) }()
			}
			wg.Wait()

			// READ: block until Inhibitor sends the step on FeedbackIn.
			step, ok := n.FeedbackIn.TryRecv()
			if !ok {
				return
			}
			n.FeedbackIn.Done()
			if step != 1 {
				// Hold: buffer unchanged, send the same last bead next loop.
				continue
			}

			// s == 1: POP the end (change the bead); refill when action empties.
			working = working[:len(working)-1]
			if len(working) == 0 {
				// Animated refill: the top row (backup) SLIDES DOWN into the
				// working row at human speed (clock-paced, pause-aware). After the
				// slide lands, the new top row appears via the full emitBeads below.
				if n.EmitRefillSlide != nil {
					n.EmitRefillSlide(backup)
				}
				working = backup
				backup = append([]int(nil), init...)
			}
			emitBeads() // array changed (pop, maybe refill) → restream interior
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
		emitBeads() // array changed (pop, maybe refill) → restream interior
		// Node 1 initiates a goroutine per wired output so node 2
		// (ToInhibitor) and node 6 (ToExcitatory) get the same bead
		// concurrently; wg.Wait keeps node 1 paced before the next pop.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); n.ToInhibitor.EmitOneDriven(ctx, v) }()
		if n.ToExcitatory.Wired() {
			wg.Add(1)
			go func() { defer wg.Done(); n.ToExcitatory.EmitOneDriven(ctx, v) }()
		}
		wg.Wait()
		emitted++
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
