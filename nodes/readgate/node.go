package readgate

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
	Fire               func()
	Value              int
	HasValue           bool
	HasChainInhibitor  bool
	FromInput          *Wiring.In
	FromChainInhibitor *Wiring.In
	ToChainInhibitor   *Wiring.Out
}

// windowMs derives the coincidence window W from the node's current input wires:
// W = windowFactor * max(simLatencyMs over input wires). Recomputed from live
// wire geometry (via In.SimLatencyMs) so node moves / reconnects are reflected.
func (g *Node) windowMs() time.Duration {
	maxLat := g.FromInput.SimLatencyMs()
	if c := g.FromChainInhibitor.SimLatencyMs(); c > maxLat {
		maxLat = c
	}
	return time.Duration(windowFactor*maxLat) * time.Millisecond
}

// clear discards both held inputs without firing: Done drains each upstream wire
// (so a consumeGated source's WaitConsumed returns) and the has-input flags reset.
func (g *Node) clear(t0Set *bool) {
	if g.HasValue {
		g.FromInput.Done()
	}
	if g.HasChainInhibitor {
		g.FromChainInhibitor.Done()
	}
	g.FromInput.Breadcrumb("window_clear", "")
	g.HasValue = false
	g.HasChainInhibitor = false
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

		if !g.HasValue {
			if v, ok := g.FromInput.PollRecv(); ok {
				g.Value = v
				g.HasValue = true
			}
		}

		if !g.HasChainInhibitor {
			if _, ok := g.FromChainInhibitor.PollRecv(); ok {
				g.HasChainInhibitor = true
			}
		}

		// Window opens on the first input that arrives.
		if (g.HasValue || g.HasChainInhibitor) && !t0Set {
			t0 = time.Now()
			t0Set = true
		}

		if g.HasValue && g.HasChainInhibitor {
			// All inputs present within W → keep and fire.
			g.Fire()
			g.FromInput.Done()
			g.FromChainInhibitor.Done()
			g.HasValue = false
			g.HasChainInhibitor = false
			t0Set = false
			if g.ToChainInhibitor.Gated() {
				if g.ToChainInhibitor.TrySend(g.Value) {
					if !g.ToChainInhibitor.WaitConsumed() {
						return
					}
				}
			} else {
				g.ToChainInhibitor.TryEmit(g.Value)
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
	Wiring.Register("ReadGate", func() any { return &Node{} })
}
