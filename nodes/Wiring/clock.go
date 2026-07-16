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
// Halt/Resume is the global play/pause gate (MODEL.md: "a single global gate
// halts or resumes wire animation"). While halted the tick does not advance.

package Wiring

import (
	"context"
	"sync"
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
// current integer tick (paused intervals excluded). Halt/Resume is the global
// play/pause gate.
type Clock interface {
	// Tick returns the current tick since the clock started, excluding any
	// intervals during which the clock was halted (pause-aware: ticks-while-
	// paused do not accrue).
	Tick() int64
	// SleepCycle blocks for exactly one clock cycle (or until ctx is done).
	// It is the primitive for one-cycle pacing loops; it does not read Tick()
	// itself, so it is immune to a tick advancing between the call and the wait.
	SleepCycle(ctx context.Context) error
	// Halt pauses the clock: the tick stops advancing until Resume.
	Halt()
	// Resume un-pauses the clock: the tick advances again from where it stopped
	// (no wall-clock catch-up).
	Resume()
}

// RealClock is the production Clock. It tracks active elapsed by subtracting the
// total halted duration from wall-clock elapsed, then floors that to a tick via
// MsPerTick, so pause stops tick advance while the underlying monotonic clock
// keeps ticking. Nothing waits on this clock via a condition variable — pacing
// loops call SleepCycle (time.After-based) and re-check Tick() themselves.
type RealClock struct {
	mu sync.Mutex
	// start is the wall-clock instant the clock began (monotonic).
	start time.Time
	// halted is true while paused.
	halted bool
	// haltedAt is the wall-clock instant of the current halt (valid iff halted).
	haltedAt time.Time
	// haltedTotal is the accumulated paused duration across all prior halts.
	haltedTotal time.Duration
	// hasRun is true once the clock has left halted at least once (i.e. Resume has made a
	// real halted->running transition). It never goes back to false for this process's
	// lifetime — it distinguishes "never started" (fresh load) from "started, now paused"
	// for the RunButton's run-vs-resume label. The clock owns this bit itself, exactly like
	// halted: it is set inside Resume()'s transition guard, never derived by snapshot.go,
	// main.go, or TS (see MODEL.md's bridge-surface rule).
	hasRun bool
	// haltedHook, when non-nil, is called with the new halted state and the current hasRun
	// bit from inside the Halt()/Resume() transition guards below — exactly once per real
	// state change. This is the sole trace-emit point for KindHalted (see Trace.Halted): the
	// 4 Halt/Resume call sites (main.go, stdin_reader.go) never emit directly, so the truth
	// can't drift across them. Optional/nil-safe: headless tests construct a RealClock
	// without wiring a hook.
	haltedHook func(halted bool, hasRun bool)
}

// SetHaltedHook installs the clock's halted-state trace hook: after this call, every REAL
// transition made by Halt()/Resume() calls hook(halted, hasRun) exactly once. Call once at
// startup (after both the clock and Trace exist) before the first Halt()/Resume(). Pass nil
// to leave it unset (the default) — Halt/Resume then emit nothing, which is safe for
// headless tests.
func (c *RealClock) SetHaltedHook(hook func(halted bool, hasRun bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.haltedHook = hook
}

// NewRealClock returns a started RealClock anchored at the current monotonic instant.
func NewRealClock() *RealClock {
	return &RealClock{start: time.Now()}
}

// activeElapsedLocked computes active elapsed (pause-aware); caller holds c.mu.
func (c *RealClock) activeElapsedLocked() time.Duration {
	elapsed := time.Since(c.start) - c.haltedTotal
	if c.halted {
		// Subtract the in-progress halt so elapsed freezes while paused.
		elapsed -= time.Since(c.haltedAt)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return elapsed
}

// tickLocked floors active elapsed to a tick; caller holds c.mu.
func (c *RealClock) tickLocked() int64 {
	return int64(c.activeElapsedLocked() / tickPeriod)
}

// Tick returns the current tick (pause-aware).
func (c *RealClock) Tick() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tickLocked()
}

// Halt pauses the clock.
func (c *RealClock) Halt() {
	c.mu.Lock()
	changed := false
	if !c.halted {
		c.halted = true
		c.haltedAt = time.Now()
		changed = true
	}
	hook := c.haltedHook
	hasRun := c.hasRun
	c.mu.Unlock()
	// Emit outside the lock — the hook may block on a channel send (Trace.emit), and Tick()
	// must not wait on that.
	if changed && hook != nil {
		hook(true, hasRun)
	}
}

// Resume un-pauses the clock. The first real halted->running transition sets hasRun true
// for the rest of this process's lifetime (see the hasRun field doc).
func (c *RealClock) Resume() {
	c.mu.Lock()
	changed := false
	if c.halted {
		c.halted = false
		c.haltedTotal += time.Since(c.haltedAt)
		c.hasRun = true
		changed = true
	}
	hook := c.haltedHook
	hasRun := c.hasRun
	c.mu.Unlock()
	if changed && hook != nil {
		hook(false, hasRun)
	}
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
// port (validate.go). Tick is 0 and Halt/Resume are no-ops: with no clock there is no
// time to report or stop.
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
func (inertClock) Halt()   {}
func (inertClock) Resume() {}

// Compile-time assertion that inertClock satisfies Clock.
var _ Clock = inertClock{}
