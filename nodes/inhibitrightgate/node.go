package inhibitrightgate

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// windowFactor scales the max input-wire latency into the coincidence window W.
// W = windowFactor * max(simLatencyMs over the node's current input wires).
const windowFactor = 1.5

// pollInterval bounds the busy-spin of the window loop: between polls the loop
// parks on a short timeout (or ctx cancel) instead of spinning.
const pollInterval = 5 * time.Millisecond

type Node struct {
	Fire     func()
	Left     int
	HasLeft  bool
	Right    int
	HasRight bool
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
}

// windowMs derives the coincidence window W from the node's current input wires:
// W = windowFactor * max(simLatencyMs over input wires). Recomputed from live
// wire geometry (via In.SimLatencyMs) so node moves / reconnects are reflected.
func (g *Node) windowMs() time.Duration {
	maxLat := g.FromLeft.SimLatencyMs()
	if r := g.FromRight.SimLatencyMs(); r > maxLat {
		maxLat = r
	}
	return time.Duration(windowFactor*maxLat) * time.Millisecond
}

// clear discards both held inputs without firing: Done drains each upstream wire
// (so a consumeGated source's WaitConsumed returns) and the has-input flags reset.
func (g *Node) clear(t0Set *bool) {
	if g.HasLeft {
		g.FromLeft.Done()
	}
	if g.HasRight {
		g.FromRight.Done()
	}
	g.FromLeft.Breadcrumb("window_clear", "")
	g.HasLeft = false
	g.HasRight = false
	*t0Set = false
}

func (g *Node) Update(ctx context.Context) {
	var t0 time.Time
	var t0Set bool

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasLeft {
			if v, ok := g.FromLeft.PollRecv(); ok {
				g.Left = v
				g.HasLeft = true
			}
		}

		if !g.HasRight {
			if v, ok := g.FromRight.PollRecv(); ok {
				g.Right = v
				g.HasRight = true
			}
		}

		// Window opens on the first input that arrives.
		if (g.HasLeft || g.HasRight) && !t0Set {
			t0 = time.Now()
			t0Set = true
		}

		if g.HasLeft && g.HasRight {
			// All inputs present within W → keep and fire.
			result := 0
			if g.Left == 1 && g.Right == 0 {
				result = 1
			}
			g.Fire()
			g.FromLeft.Done()
			g.FromRight.Done()
			g.HasLeft = false
			g.HasRight = false
			t0Set = false
			if g.ToPassed.Gated() {
				if g.ToPassed.TrySend(result) {
					if !g.ToPassed.WaitConsumed() {
						return
					}
				}
			} else {
				g.ToPassed.TryEmit(result)
			}
			continue
		}

		// A partial combination has been open longer than W → clear it.
		if t0Set && time.Since(t0) > g.windowMs() {
			g.clear(&t0Set)
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
	Wiring.Register("InhibitRightGate", func() any { return &Node{} })
}
