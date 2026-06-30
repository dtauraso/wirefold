// Package gatecommon holds the shared constants and gate-loop body used by
// WindowAndInhibitLeftGate and WindowAndInhibitRightGate. Each of those node
// packages is its own package (primitive landing rule) but delegates its
// Update body here, parameterised by which side is NOT-inverted on capture.
package gatecommon

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// WindowWu is the fixed per-node coincidence window expressed as a distance in
// world units. At the one pulseSpeed (0.04 wu/ms) this equals 3000 ms — enough
// to exceed the same-cycle input skew (~69 ms measured) while staying under the
// input cadence (~3104 ms). It is a property of the node, like a neuron's
// membrane time constant, and does NOT depend on input-wire geometry.
const WindowWu = 120

// PollInterval bounds the busy-spin of the window loop: between polls the loop
// parks on a short timeout (or ctx cancel) instead of spinning.
const PollInterval = 5 * time.Millisecond

// FireDwellMs holds both inputs visible (interior beads present) for this long
// once both are held, before the gate fires + clears. Without it the
// second-arriving interior bead only flashes for ~1ms before the fire clears it.
const FireDwellMs = 800

// NoValue is the sentinel meaning "no value yet" / "no real bead". Real values
// are non-negative indices so NoValue (-1) never collides with a legitimate value.
const NoValue = -1

// GateNode holds all the fields shared between the two gate node kinds.
// Each kind embeds GateNode so its init/Update can delegate here.
type GateNode struct {
	Fire           func()
	EmitGeometry   func()
	EmitInputBeads func(left, right int)
	// Now returns active-elapsed sim time (pause-aware) from the same clock the
	// PacedWire/train use. Injected by the loader (builders.go) from pb.clock.
	// The window and dwell are measured against it so they freeze on pause and
	// resume on resume — never timing out mid-pause. If unset (unit tests with no
	// loader), it falls back to a monotonic wall-clock so timing still progresses.
	Now       func() time.Duration
	WaitUntil func(ctx context.Context, target time.Duration) error // pause-aware park; nil → wall-clock fallback
	Left      int
	HasLeft   bool
	Right     int
	HasRight  bool
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
}

// windowMs returns the fixed coincidence window as a duration by converting the
// distance WindowWu to time via the one pulseSpeed.
func windowMs() time.Duration {
	return time.Duration(WindowWu/Wiring.PulseSpeedWuPerMs) * time.Millisecond
}

// RunGate runs the shared window-and-inhibit gate loop.
// invertLeft=true  → the LEFT input is NOT-inverted on capture  (WindowAndInhibitLeftGate).
// invertLeft=false → the RIGHT input is NOT-inverted on capture (WindowAndInhibitRightGate).
func RunGate(ctx context.Context, g *GateNode, invertLeft bool) {
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

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
			case <-time.After(PollInterval):
				return nil
			}
		}
	}

	var t0 time.Duration
	var t0Set bool
	var dwellStart time.Duration
	var dwellSet bool

	emitInputs := func() {
		l, r := NoValue, NoValue
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

	// drainLatestReal consumes ALL queued beads on a side and returns the
	// most-recent REAL value (discarding NoValue placeholders). got=false when
	// nothing real was queued.
	drainLatestReal := func(in *Wiring.In) (int, bool) {
		v, got := NoValue, false
		for {
			nv, ok := in.PollRecv()
			if !ok {
				break
			}
			if nv != NoValue {
				v = nv
				got = true
			}
		}
		return v, got
	}

	// clear discards both held inputs without firing: Done drains each upstream
	// wire (so a consumeGated source's WaitConsumed returns) and the has-input
	// flags reset. Breadcrumb on FromLeft (the consistent logging point).
	clear := func() {
		if g.HasLeft {
			g.FromLeft.Done()
		}
		if g.HasRight {
			g.FromRight.Done()
		}
		g.FromLeft.Breadcrumb("window_clear", "")
		g.HasLeft = false
		g.HasRight = false
		t0Set = false
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Each side tracks the MOST-RECENT real bead: drain to the latest value
		// (discarding NoValue placeholders) and update the slot even if already
		// held. NoValue never fills a slot.
		if v, got := drainLatestReal(g.FromLeft); got {
			var stored int
			if invertLeft {
				stored = 1 - v // NOT the left input
			} else {
				stored = v
			}
			if !g.HasLeft || g.Left != stored {
				g.Left = stored
				g.HasLeft = true
				emitInputs()
			}
		}

		if v, got := drainLatestReal(g.FromRight); got {
			var stored int
			if !invertLeft {
				stored = 1 - v // NOT the right input
			} else {
				stored = v
			}
			if !g.HasRight || g.Right != stored {
				g.Right = stored
				g.HasRight = true
				emitInputs()
			}
		}

		// Window opens on the first input that arrives.
		if (g.HasLeft || g.HasRight) && !t0Set {
			t0 = now()
			t0Set = true
		}

		if g.HasLeft && g.HasRight {
			// Both inputs held: dwell so both interior beads are visible before
			// the gate resolves. Once committed to the dwell, the window-timeout
			// below is gated off so it can't clip the fire.
			if !dwellSet {
				dwellStart = now()
				dwellSet = true
			}
			if now()-dwellStart >= FireDwellMs*time.Millisecond {
				// AND gate over the stored values (each side already applied its
				// inversion on capture); fires 1 iff Left==1 AND Right==1.
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
		// we are committed to firing after the dwell.
		if t0Set && !(g.HasLeft && g.HasRight) && now()-t0 > windowMs() {
			clear()
			emitInputs()
		}

		// Short park between polls (pause-aware: parks on the one clock, freezes on pause).
		if park(ctx, now()+PollInterval) != nil {
			return
		}
	}
}
