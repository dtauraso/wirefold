// clock.go — the single human-speed clock the network reads.
//
// MODEL.md pins this: there is exactly one clock — the system monotonic clock
// read through a fixed SCALE so it advances in integer TICKS at human-watchable
// speed (`tick = ⌊(systemNow − start) × scale⌋`). All timing in the network is
// tick counts, never wall-clock durations: goroutines pace themselves with
// SleepCycle, which blocks for exactly one clock cycle. A bead crossing an edge
// takes `ticksToCross = arcLength / pulseSpeed` ticks (pulseSpeed in
// world-units-per-tick); node
// processing windows are tick counts. There is no separate render cadence — the
// tick IS the animation clock.
//
// SCALE arithmetic (behavior-preserving vs. the retired wall-clock model):
// the old model sampled bead positions every 16 ms and crossed an edge in
// arcLength/pulseSpeedWuPerMs wall-ms. We pick one tick ≈ one old 16 ms sample:
//
//	MsPerTick = 16   ⇒   scale = 1 tick / 16 ms = 62.5 ticks/sec.
//
// So a bead visits ~the same number of positions in ~the same wall time, and
// pause/resume look identical. (pulseSpeed's world-units-per-tick reinterpret
// lives in paced_wire.go: PulseSpeedWuPerTick = PulseSpeedWuPerMs × MsPerTick.)
//
// The model is sleep-only: pacing loops call SleepCycle to wait exactly one
// clock cycle rather than blocking on a target tick. RealClock is the single
// production Clock implementation.
//
// The clock is free-running: there is no play/pause gate (that feature was removed
// end-to-end), so the tick advances monotonically with wall time for the life of
// the process.

package Wiring

import (
	"context"
	"time"
)

// MsPerTick is the scale of the human-speed clock: one tick spans this many
// wall-milliseconds while running (scale = 1/MsPerTick ticks per ms). 16 ms/tick
// = 62.5 ticks/sec, matching the retired 16 ms position-sample cadence so visible
// bead speed is unchanged.
const MsPerTick = 16

// tickPeriod is MsPerTick as a Duration (the wall span of one running tick).
const tickPeriod = MsPerTick * time.Millisecond

// Clock is the one human-speed clock the network reads. Tick() returns the
// current integer tick. The clock is free-running (no pause gate).
type Clock interface {
	// Tick returns the current tick since the clock started. The clock is
	// free-running wall-clock time floored to ticks; there is no pause, so ticks
	// accrue monotonically from start for the life of the process.
	Tick() int64
	// SleepCycle blocks for exactly one clock cycle (or until ctx is done).
	// It is the primitive for one-cycle pacing loops; it does not read Tick()
	// itself, so it is immune to a tick advancing between the call and the wait.
	SleepCycle(ctx context.Context) error
}

// RealClock is the production Clock: wall-clock elapsed since start, floored to a
// tick via MsPerTick. It is free-running — there is no halt/pause gate (the
// play/pause feature was removed end-to-end), so Tick() is a plain monotonic
// function of real time. Nothing waits on this clock via a condition variable —
// pacing loops call SleepCycle (time.After-based) and re-check Tick() themselves.
type RealClock struct {
	// start is the wall-clock instant the clock began (monotonic). Immutable after
	// construction, so Tick() needs no lock.
	start time.Time
}

// NewRealClock returns a started RealClock anchored at the current monotonic instant.
func NewRealClock() *RealClock {
	return &RealClock{start: time.Now()}
}

// Tick returns the current tick: wall-clock elapsed since start, floored to ticks.
func (c *RealClock) Tick() int64 {
	elapsed := time.Since(c.start)
	if elapsed < 0 {
		elapsed = 0
	}
	return int64(elapsed / tickPeriod)
}

// SleepCycle blocks for one tickPeriod, or until ctx is done.
func (c *RealClock) SleepCycle(ctx context.Context) error {
	select {
	case <-time.After(tickPeriod):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Compile-time assertion that RealClock satisfies Clock.
var _ Clock = (*RealClock)(nil)

// inertClock is the Clock handed to an unwired In when no shared clock exists at all
// (a test build with no loader — see PortBindings.clock). It is the clock analogue of
// deadEndIn's placeholder channel: it exists only so In.Clock() has something non-nil
// to return, because a nil Clock is a nil-interface method call waiting to happen and
// every pacing loop calls SleepCycle unconditionally.
//
// SleepCycle blocks until ctx is done rather than returning immediately: a node with no
// clock has no cycle to sleep for, and returning nil would spin its pacing loop hot. The
// node's loop sees the ctx error and exits — inert, which is the contract for an unfed
// port (validate.go). Tick is 0: with no clock there is no time to report.
//
// Production never sees this: the loader always sets PortBindings.clock (loader.go), so
// an unwired In gets the SAME shared clock every wired node runs on and stays alive,
// polling a port that never delivers — inert by precondition-gating, not by exiting.
type inertClock struct{}

func (inertClock) Tick() int64 { return 0 }
func (inertClock) SleepCycle(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Compile-time assertion that inertClock satisfies Clock.
var _ Clock = inertClock{}
