// clock.go — the single human-speed clock the network reads.
//
// MODEL.md pins this: there is exactly one clock — the system monotonic clock
// read through a fixed SCALE so it advances in integer TICKS at human-watchable
// speed (`tick = ⌊(systemNow − start) × scale⌋`). All timing in the network is
// tick counts, never wall-clock durations: goroutines wait on WaitTick(k)
// ("resume when tick ≥ k"). A bead crossing an edge takes `ticksToCross =
// arcLength / pulseSpeed` ticks (pulseSpeed in world-units-per-tick); node
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
// The clock is injectable so tests are deterministic:
//   - RealClock wraps Go's monotonic clock (time.Now) and derives the tick from
//     active elapsed; it sleeps for real between ticks.
//   - FakeClock's tick is set directly by tests via SetTick/AdvanceTicks; those
//     wake every waiter so no real sleeps are needed.
//
// Halt/Resume is the global play/pause gate (MODEL.md: "a single global gate
// halts or resumes wire animation"). While halted the tick does not advance, so
// no wire's WaitTick target is reached — progress freezes.

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
// current integer tick (paused intervals excluded). WaitTick blocks until the
// tick reaches k. Halt/Resume is the global play/pause gate.
type Clock interface {
	// Tick returns the current tick since the clock started, excluding any
	// intervals during which the clock was halted (pause-aware: ticks-while-
	// paused do not accrue).
	Tick() int64
	// WaitTick blocks until Tick() >= k, or until ctx is done. Returns ctx.Err()
	// if the context was canceled before the target tick was reached, nil
	// otherwise. Waking is edge-triggered: Resume() and (for the fake)
	// AdvanceTicks()/SetTick() re-evaluate every waiter.
	WaitTick(ctx context.Context, k int64) error
	// SleepCycle blocks for exactly one clock cycle (or until ctx is done).
	// It is the primitive for one-cycle pacing loops that previously spelled
	// WaitTick(ctx, Tick()+1); unlike that spelling it does not re-read Tick(),
	// so it is immune to a tick advancing between the read and the wait.
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
// keeps ticking. Resume broadcasts so a freshly-resumed clock re-checks every
// waiter promptly.
type RealClock struct {
	mu sync.Mutex
	// cond signals waiters when state changes (Resume, or the periodic re-check).
	cond *sync.Cond
	// start is the wall-clock instant the clock began (monotonic).
	start time.Time
	// halted is true while paused.
	halted bool
	// haltedAt is the wall-clock instant of the current halt (valid iff halted).
	haltedAt time.Time
	// haltedTotal is the accumulated paused duration across all prior halts.
	haltedTotal time.Duration
}

// NewRealClock returns a started RealClock anchored at the current monotonic instant.
func NewRealClock() *RealClock {
	c := &RealClock{start: time.Now()}
	c.cond = sync.NewCond(&c.mu)
	return c
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
	if !c.halted {
		c.halted = true
		c.haltedAt = time.Now()
	}
	c.mu.Unlock()
}

// Resume un-pauses the clock and wakes all waiters to re-check their targets.
func (c *RealClock) Resume() {
	c.mu.Lock()
	if c.halted {
		c.halted = false
		c.haltedTotal += time.Since(c.haltedAt)
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// WaitTick blocks until the tick reaches k (pause-aware) or ctx is done. The real
// clock keeps advancing on its own, so a waiter not yet at its target sleeps for
// the remaining wall duration to the next tick boundary and re-checks; while
// halted it parks on cond until Resume. A ctx watcher broadcasts on cancel.
func (c *RealClock) WaitTick(ctx context.Context, k int64) error {
	stop := c.watchCtx(ctx)
	defer close(stop)

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.tickLocked() >= k {
			return nil
		}
		if c.halted {
			// Frozen: park until Resume (or ctx cancel) broadcasts.
			c.cond.Wait()
			continue
		}
		// Running: sleep for the remaining active duration to tick k, then
		// re-check. remaining = k·tickPeriod − activeElapsed (>0 here since
		// tick < k). Drop the lock while sleeping so Halt/Resume and ctx cancel
		// can proceed; a timer broadcast re-checks the target.
		remaining := time.Duration(k)*tickPeriod - c.activeElapsedLocked()
		if remaining <= 0 {
			remaining = time.Millisecond
		}
		c.mu.Unlock()
		timer := time.NewTimer(remaining)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		case <-stop:
			// stop is closed by defer only after we return; not hit here.
			timer.Stop()
		}
		c.mu.Lock()
	}
}

// SleepCycle blocks for one tickPeriod, or until ctx is done. Plain timer, no
// lock, no cond, no halt check — the real clock has no pause-aware notion of
// "one cycle" the way WaitTick does; that gap is closed in a later pass.
func (c *RealClock) SleepCycle(ctx context.Context) error {
	select {
	case <-time.After(tickPeriod):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// watchCtx broadcasts on c.cond when ctx is done so a parked WaitTick wakes to
// observe cancellation. The caller closes the returned channel to stop the watcher.
func (c *RealClock) watchCtx(ctx context.Context) chan struct{} {
	return broadcastOnCancel(ctx, &c.mu, c.cond)
}

// broadcastOnCancel starts a goroutine that broadcasts on cond when ctx is done,
// holding mu around the broadcast so the wake cannot be lost in a waiter's
// check→Wait window. The caller closes the returned channel to stop the watcher.
// Shared by RealClock.watchCtx, FakeClock.watchCtx, and PacedWire.Recv — the one
// place the "wake a cond-parked waiter on cancellation" pattern lives.
func broadcastOnCancel(ctx context.Context, mu *sync.Mutex, cond *sync.Cond) chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			mu.Lock()
			cond.Broadcast()
			mu.Unlock()
		case <-stop:
		}
	}()
	return stop
}

// FakeClock is the deterministic Clock for tests. The tick advances ONLY via
// AdvanceTicks(n)/SetTick(n) (and never while halted). Advancing wakes every
// waiter so a test can drive the whole cascade with no real sleeps: place beads,
// advance past their ticksToCross, and delivery fires synchronously on the test's
// timeline.
type FakeClock struct {
	mu     sync.Mutex
	cond   *sync.Cond
	tick   int64
	halted bool
}

// NewFakeClock returns a FakeClock at tick 0.
func NewFakeClock() *FakeClock {
	c := &FakeClock{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Tick returns the fake tick.
func (c *FakeClock) Tick() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tick
}

// AdvanceTicks moves the tick forward by n and wakes every waiter. While halted,
// AdvanceTicks is a no-op (pause stops the tick). A non-positive n is ignored.
// After it returns, every WaitTick whose target is now satisfied will observe it
// (waking is via cond.Broadcast under the same lock).
func (c *FakeClock) AdvanceTicks(n int64) {
	c.mu.Lock()
	if !c.halted && n > 0 {
		c.tick += n
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// SetTick sets the tick to an absolute value and wakes every waiter. It ignores a
// value below the current tick (the tick never moves backward) and is a no-op
// while halted. Convenience for tests that think in absolute tick targets.
func (c *FakeClock) SetTick(k int64) {
	c.mu.Lock()
	if !c.halted && k > c.tick {
		c.tick = k
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// Halt freezes the fake tick; subsequent AdvanceTicks/SetTick are no-ops until Resume.
func (c *FakeClock) Halt() {
	c.mu.Lock()
	c.halted = true
	c.mu.Unlock()
}

// Resume un-freezes the fake tick and wakes waiters to re-check.
func (c *FakeClock) Resume() {
	c.mu.Lock()
	c.halted = false
	c.cond.Broadcast()
	c.mu.Unlock()
}

// WaitTick blocks until the fake tick reaches k or ctx is done. It parks on cond
// and is woken by AdvanceTicks / SetTick / Resume / ctx-cancel; on each wake it
// re-checks the target. Fully deterministic — the only thing that moves the
// target closer is a test advancing the tick.
func (c *FakeClock) WaitTick(ctx context.Context, k int64) error {
	stop := c.watchCtx(ctx)
	defer close(stop)

	c.mu.Lock()
	defer c.mu.Unlock()
	for c.tick < k {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.cond.Wait()
	}
	// Target met: prefer target-success over a simultaneous ctx cancellation,
	// matching RealClock.WaitTick (which returns nil directly on tick >= k).
	return nil
}

// SleepCycle blocks until the fake tick advances by exactly one from its
// current value when the call starts, or until ctx is done. Reuses the same
// cond-wait/watchCtx wakeup pattern as WaitTick so AdvanceTicks-driven tests
// stay deterministic.
func (c *FakeClock) SleepCycle(ctx context.Context) error {
	stop := c.watchCtx(ctx)
	defer close(stop)

	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.tick + 1
	for c.tick < target {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.cond.Wait()
	}
	return nil
}

// watchCtx broadcasts on c.cond when ctx is done so a parked WaitTick wakes.
func (c *FakeClock) watchCtx(ctx context.Context) chan struct{} {
	return broadcastOnCancel(ctx, &c.mu, c.cond)
}

// Compile-time assertions that both impls satisfy Clock.
var (
	_ Clock = (*RealClock)(nil)
	_ Clock = (*FakeClock)(nil)
)
