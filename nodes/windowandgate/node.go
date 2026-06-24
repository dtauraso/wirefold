package windowandgate

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// windowWu is the fixed per-node coincidence window expressed as a distance in
// world units. At the one pulseSpeed (0.04 wu/ms) this equals 3000 ms — enough
// to exceed the same-cycle input skew (~69 ms measured) while staying under the
// input cadence (~3104 ms). It is a property of the node, like a neuron's
// membrane time constant, and does NOT depend on input-wire geometry.
const windowWu = 120

// pollInterval bounds the busy-spin of the window loop: between polls the loop
// parks on a short timeout (or ctx cancel) instead of spinning.
const pollInterval = 5 * time.Millisecond

// fireDwellMs holds both inputs visible (interior beads present) for this long
// once both are held, before the gate fires + clears. Without it the
// second-arriving interior bead only flashes for ~1ms before the fire clears it.
const fireDwellMs = 800

type Node struct {
	Fire           func()
	EmitGeometry   func()
	EmitInputBeads func(left, right int)
	// Now returns active-elapsed sim time (pause-aware) from the same clock the
	// PacedWire/train use. Injected by the loader (builders.go) from pb.clock.
	// The window and dwell are measured against it so they freeze on pause and
	// resume on resume — never timing out mid-pause. If unset (unit tests with no
	// loader), it falls back to a monotonic wall-clock so timing still progresses.
	Now            func() time.Duration
	WaitUntil      func(ctx context.Context, target time.Duration) error // pause-aware park on the one clock; nil in test/no-loader builds → wall-clock fallback
	Left           int
	HasLeft        bool
	Right          int
	HasRight       bool
	FromLeft       *Wiring.In
	FromRight      *Wiring.In
	ToPassed       *Wiring.Out
}

// windowMs returns the fixed coincidence window as a duration by converting the
// distance windowWu to time via the one pulseSpeed. This is pure distance-based
// timing — independent of input-wire geometry and pause-aware because the caller
// reads it against now() (the injected pause-aware clock).
func (g *Node) windowMs() time.Duration {
	return time.Duration(windowWu/Wiring.PulseSpeedWuPerMs) * time.Millisecond
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
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

	// now reads active-elapsed sim time (pause-aware) from the injected clock so
	// the window and dwell freeze on pause. Fall back to a monotonic wall-clock
	// when no clock was injected (unit tests with no loader).
	now := g.Now
	if now == nil {
		start := time.Now()
		now = func() time.Duration { return time.Since(start) }
	}

	park := g.WaitUntil
	if park == nil {
		park = func(ctx context.Context, _ time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
				return nil
			}
		}
	}

	var t0 time.Duration
	var t0Set bool
	var dwellStart time.Duration
	var dwellSet bool

	emitInputs := func() {
		l, r := -1, -1
		if g.HasLeft {
			l = g.Left
		}
		if g.HasRight {
			r = g.Right
		}
		if g.EmitInputBeads != nil {
			g.EmitInputBeads(l, r)
		}
	}
	emitInputs() // initial empty interior

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// -1 is the "no value" placeholder (upstream nodes drive it before they hold a
		// real value); it is NOT a value to be received. Discard any -1s and only fill
		// a slot with a real value, so the gate coincides real beads, not placeholders.
		if !g.HasLeft {
			for {
				v, ok := g.FromLeft.PollRecv()
				if !ok {
					break
				}
				if v != -1 {
					g.Left = v
					g.HasLeft = true
					emitInputs()
					break
				}
			}
		}

		if !g.HasRight {
			for {
				v, ok := g.FromRight.PollRecv()
				if !ok {
					break
				}
				if v != -1 {
					g.Right = v
					g.HasRight = true
					emitInputs()
					break
				}
			}
		}

		// Window opens on the first input that arrives.
		if (g.HasLeft || g.HasRight) && !t0Set {
			t0 = now()
			t0Set = true
		}

		if g.HasLeft && g.HasRight {
			// Both inputs held: dwell so both interior beads are visible
			// before the gate resolves. Once committed to the dwell, the
			// window-timeout below is gated off so it can't clip the fire.
			if !dwellSet {
				dwellStart = now()
				dwellSet = true
			}
			if now()-dwellStart >= fireDwellMs*time.Millisecond {
				// AND gate: fires 1 when both inputs are 1, else 0.
				result := 0
				if g.Left == 1 && g.Right == 1 {
					result = 1
				}
				g.Fire()
				g.FromLeft.Done()
				g.FromRight.Done()
				g.HasLeft = false
				g.HasRight = false
				t0Set = false
				dwellSet = false
				emitInputs()
				g.ToPassed.EmitOneDriven(ctx, result)
				continue
			}
		}

		// A partial combination has been open longer than W → clear it. Only
		// time out while still waiting for the second input; once both are held
		// we are committed to firing after the dwell, so the dwell can't be
		// clipped by the window even if it outlasts W.
		if t0Set && !(g.HasLeft && g.HasRight) && now()-t0 > g.windowMs() {
			g.clear(&t0Set)
			emitInputs()
		}

		// Short park between polls (pause-aware: parks on the one clock, freezes on pause).
		if park(ctx, now()+pollInterval) != nil {
			return
		}
	}
}

func init() {
	Wiring.Register("WindowAndGate", func() any { return &Node{} })
}
