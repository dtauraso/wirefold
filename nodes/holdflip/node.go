package holdflip

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// pollInterval bounds the busy-spin of the update loop: between polls the loop
// parks on a short timeout (or ctx cancel) instead of spinning.
const pollInterval = 5 * time.Millisecond

type Node struct {
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Value        int
	HasValue     bool
	In           *Wiring.In
	Out          *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

	// held tracks the last received INPUT value displayed inside the node sphere.
	// -1 is the sentinel meaning "no value seen yet" → empty interior. The bead is
	// re-emitted only when held actually changes below.
	held := -1
	if g.EmitHeldBead != nil {
		g.EmitHeldBead(held)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasValue {
			if v, ok := g.In.PollRecv(); ok {
				g.Value = v
				g.HasValue = true
			}
		}

		if g.HasValue {
			// Display the INPUT value inside the node sphere. Re-emit only when it
			// changes; the held display persists (it is NOT cleared after firing)
			// until a new input replaces it. The Out wire still carries 1-value.
			heldChanged := g.Value != held
			held = g.Value
			if heldChanged && g.EmitHeldBead != nil {
				g.EmitHeldBead(g.Value)
			}

			// Single value held → fire immediately, emit the inverted value.
			result := 1 - g.Value
			g.Fire()
			g.In.Done()
			g.HasValue = false
			g.In.Breadcrumb("hold_flip", "")
			if g.Out.Gated() {
				if g.Out.TrySend(result) {
					if !g.Out.WaitConsumed() {
						return
					}
				}
			} else {
				g.Out.TryEmit(result)
			}
			continue
		}

		// Short park between polls to avoid busy-spin.
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

func init() {
	Wiring.Register("HoldFlip", func() any { return &Node{} })
}
