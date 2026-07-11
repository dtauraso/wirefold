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

// WindowMs is the target coincidence window expressed in milliseconds. This is a
// design choice calibrated to exceed the same-cycle input skew (~69 ms measured)
// while staying under the input cadence (~3104 ms).
const WindowMs = 3000

// WindowWu is derived from WindowMs and the one pulse speed so it stays correct
// if PulseSpeedWuPerMs is retuned: WindowWu = WindowMs × PulseSpeedWuPerMs.
const WindowWu = WindowMs * Wiring.PulseSpeedWuPerMs // = 120 wu at PulseSpeedWuPerMs=0.04

// PollIntervalTicks bounds the busy-spin of the window loop. It is a free
// scheduling choice (not derivable from pulse speed or fire-dwell) that trades
// CPU burn against reaction latency between window polls. One tick is the finest
// grain of the human-speed clock (MsPerTick ms ≈ the old 5 ms poll rounds up to 1).
const PollIntervalTicks = 1

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
	// Tick returns the current tick (pause-aware) from the same human-speed clock
	// the PacedWire/train use. Injected by the loader (builders.go) from pb.clock.
	// The window and dwell are measured in ticks against it so they freeze on pause
	// and resume on resume — never timing out mid-pause. If unset (unit tests with
	// no loader), it falls back to a wall-clock-derived tick so timing progresses.
	Tick      func() int64
	WaitTick  func(ctx context.Context, k int64) error // pause-aware park; nil → wall-clock fallback
	Left      int
	HasLeft   bool
	Right     int
	HasRight  bool
	FromLeft  *Wiring.In
	FromRight *Wiring.In
	ToPassed  *Wiring.Out
	// Layout is the hidden-layout-graph port (nodes/Wiring/layout_edge.go),
	// injected by the loader the same way EmitGeometry is. nil on builds
	// without a loader; RunGate nil-guards its poll.
	Layout *Wiring.LayoutPort
}

// windowTicks is the fixed coincidence window as a tick count (WindowMs / MsPerTick).
const windowTicks = int64(WindowMs / Wiring.MsPerTick)

// fireDwellTicks is FireDwellMs converted to a tick count.
const fireDwellTicks = int64(FireDwellMs / Wiring.MsPerTick)

// gateWindow holds the window/dwell timing state for one RunGate loop instance.
// It is local to a single call (not part of GateNode) since it is pure loop-scoped
// bookkeeping, not node-shared state.
type gateWindow struct {
	t0         int64
	t0Set      bool
	dwellStart int64
	dwellSet   bool
}

// drainLatestReal consumes ALL queued beads on a side and returns the most-recent
// REAL value (discarding NoValue placeholders). got=false when nothing real was queued.
func drainLatestReal(in *Wiring.In) (int, bool) {
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

// emitInputs reports the currently-held interior bead values (NoValue where a side
// isn't held yet).
func emitInputs(g *GateNode) {
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

// clearWindow discards both held inputs without firing: resets the has-input flags
// and the window-open state. Breadcrumb on FromLeft (the consistent logging point).
func clearWindow(g *GateNode, w *gateWindow) {
	g.FromLeft.Breadcrumb("window_clear", "")
	g.HasLeft = false
	g.HasRight = false
	w.t0Set = false
}

// captureLeft drains FromLeft and, if a real value arrived, applies the invertLeft
// inversion and stores it (only if it changed the held value/presence). Returns
// true when the held state changed and inputs should be re-emitted.
func captureLeft(g *GateNode, invertLeft bool) bool {
	v, got := drainLatestReal(g.FromLeft)
	if !got {
		return false
	}
	var stored int
	if invertLeft {
		stored = 1 - v // NOT the left input
	} else {
		stored = v
	}
	if !g.HasLeft || g.Left != stored {
		g.Left = stored
		g.HasLeft = true
		return true
	}
	return false
}

// captureRight drains FromRight and, if a real value arrived, applies the
// complementary (NOT invertLeft) inversion and stores it. Returns true when the
// held state changed and inputs should be re-emitted.
func captureRight(g *GateNode, invertLeft bool) bool {
	v, got := drainLatestReal(g.FromRight)
	if !got {
		return false
	}
	var stored int
	if !invertLeft {
		stored = 1 - v // NOT the right input
	} else {
		stored = v
	}
	if !g.HasRight || g.Right != stored {
		g.Right = stored
		g.HasRight = true
		return true
	}
	return false
}

// openWindowIfNeeded opens the coincidence window on the first input to arrive.
func openWindowIfNeeded(g *GateNode, w *gateWindow, now func() int64) {
	if (g.HasLeft || g.HasRight) && !w.t0Set {
		w.t0 = now()
		w.t0Set = true
		// Breadcrumb the window-open instant. t0 is now captured against the
		// clock, so an observer that waits for this before advancing the sim
		// clock can't race the t0 = now() read (deterministic test sync).
		g.FromLeft.Breadcrumb("window_open", "")
	}
}

// tryFireOnDwell handles the both-inputs-held case: it starts the fire-dwell timer
// on first entry, and once the dwell has elapsed, fires the AND result and resets
// the held/window/dwell state. Returns true if it fired (caller should `continue`
// its loop iteration without also running the window-timeout check).
func tryFireOnDwell(ctx context.Context, g *GateNode, w *gateWindow, now func() int64, clk Wiring.Clock) bool {
	if !(g.HasLeft && g.HasRight) {
		return false
	}
	// Both inputs held: dwell so both interior beads are visible before the gate
	// resolves. Once committed to the dwell, the window-timeout is gated off so it
	// can't clip the fire.
	if !w.dwellSet {
		w.dwellStart = now()
		w.dwellSet = true
		// Breadcrumb the dwell-start instant. dwellStart is now captured against
		// the clock, so an observer can wait for this before advancing the sim
		// clock without racing the dwellStart = now() read.
		g.FromLeft.Breadcrumb("dwell_start", "")
	}
	if now()-w.dwellStart < fireDwellTicks {
		return false
	}
	// AND gate over the stored values (each side already applied its inversion on
	// capture); fires 1 iff Left==1 AND Right==1.
	result := 0
	if g.Left == 1 && g.Right == 1 {
		result = 1
	}
	if g.Fire != nil {
		g.Fire()
	}
	g.HasLeft = false
	g.HasRight = false
	w.t0Set = false
	w.dwellSet = false
	emitInputs(g)
	if clk == nil {
		// chan mode (unit tests without a paced clock): keep the original
		// blocking drive-to-delivery behavior.
		g.ToPassed.EmitOneDriven(ctx, result)
	} else {
		// Paced mode: place the fire result without walking it to delivery.
		// The caller's per-cycle loop (RunGate) StepOnces it one position per
		// human-clock cycle — the gate goroutine is never parked across the
		// output traversal.
		g.ToPassed.PlaceDriven(result)
	}
	return true
}

// tickDuration converts a tick count to the equivalent wall-clock time.Duration
// using the same MsPerTick conversion as defaultTick/defaultPark below, so both
// wall-clock fallbacks agree on what a "tick" means.
func tickDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * Wiring.MsPerTick * time.Millisecond
}

// defaultTick returns a wall-clock-derived tick function for use when GateNode.Tick
// is unset (unit tests with no loader).
func defaultTick() func() int64 {
	start := time.Now()
	return func() int64 { return int64(time.Since(start) / tickDuration(1)) }
}

// defaultPark returns a wall-clock park function for use when GateNode.WaitTick is
// unset (unit tests with no loader).
func defaultPark() func(ctx context.Context, _ int64) error {
	return func(ctx context.Context, _ int64) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(tickDuration(PollIntervalTicks)):
			return nil
		}
	}
}

// RunGate runs the shared window-and-inhibit gate loop.
// invertLeft=true  → the LEFT input is NOT-inverted on capture  (WindowAndInhibitLeftGate).
// invertLeft=false → the RIGHT input is NOT-inverted on capture (WindowAndInhibitRightGate).
func RunGate(ctx context.Context, g *GateNode, invertLeft bool) {
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

	now := g.Tick
	if now == nil {
		now = defaultTick()
	}

	park := g.WaitTick
	if park == nil {
		park = defaultPark()
	}

	// clk selects paced vs chan mode for the OUTPUT drive: paced mode places the
	// fire result and StepOnces it one position per cycle below (never parking
	// across the traversal); chan mode keeps the original blocking
	// EmitOneDriven behavior inside tryFireOnDwell.
	clk := g.ToPassed.Clock()

	var w gateWindow
	emitInputs(g) // initial empty interior

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Park BEFORE this cycle's observe/fire/step work (not after), so a
		// StepOnce call below is always preceded by exactly one tick advance —
		// never two StepOnce calls within the same tick and never a StepOnce
		// skipped by a `continue`. PollIntervalTicks==1 today, so this is the
		// same one-tick-per-cycle cadence as the canonical WaitTick(Tick()+1)
		// shape (nodes/pacer, nodes/holdnewsendold); if PollIntervalTicks were
		// ever raised above 1 for a coarser OBSERVE cadence, the output
		// StepOnce below would still only run once per this (coarser) cycle —
		// there is currently no place in this loop that steps the output on a
		// finer grain than the poll interval, so raising PollIntervalTicks
		// would slow output transit too; flagged, not hit today.
		if park(ctx, now()+PollIntervalTicks) != nil {
			return
		}

		if p := g.Layout; p != nil {
			if msg, ok := p.TryRecv(); ok {
				p.Handle(msg)
			}
		}

		// Each side tracks the MOST-RECENT real bead: drain to the latest value
		// (discarding NoValue placeholders) and update the slot even if already
		// held. NoValue never fills a slot.
		if captureLeft(g, invertLeft) {
			emitInputs(g)
		}
		if captureRight(g, invertLeft) {
			emitInputs(g)
		}

		openWindowIfNeeded(g, &w, now)

		fired := tryFireOnDwell(ctx, g, &w, now, clk)

		// A partial combination has been open longer than W → clear it. Only
		// time out while still waiting for the second input; once both are held
		// we are committed to firing after the dwell. Skipped on the cycle we
		// just fired (mirrors the old `continue`).
		if !fired && w.t0Set && !(g.HasLeft && g.HasRight) && now()-w.t0 > windowTicks {
			clearWindow(g, &w)
			emitInputs(g)
		}

		if clk != nil {
			// Paced mode: advance any in-flight ToPassed output bead exactly one
			// position-step per cycle. The gate goroutine is never parked across
			// the output traversal — StepOnce runs every cycle regardless of
			// whether this cycle fired, so a bead placed on a previous fire keeps
			// moving while the window/dwell logic above continues concurrently.
			g.ToPassed.StepOnce(ctx)
		}
	}
}
