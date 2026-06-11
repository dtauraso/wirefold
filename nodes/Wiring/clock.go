// clock.go — the single monotonic clock the network reads.
//
// MODEL.md pins this: there is exactly one clock. All wire timing is arithmetic
// on its readings — `inFlightTime = arcLength / pulseSpeed` is derived, not a
// second timer, and each wire converts clock deltas into bead advancement and
// self-delivers when elapsed reaches inFlightTime. The play/pause gate stops the
// arithmetic, not the clock: elapsed excludes paused intervals (a bead 40% across
// stays 40% on resume).
//
// The clock is injectable so tests are deterministic:
//   - RealClock wraps Go's monotonic clock (time.Now) and sleeps for real.
//   - FakeClock is advanced by tests via Advance(d); Advance wakes every wire
//     waiting to deliver, so no real sleeps are needed.
//
// The interface exposes a pause-aware Now() (active elapsed since start, with
// paused intervals excluded) and WaitUntil(ctx, target): block until active
// elapsed reaches target. Each PacedWire calls WaitUntil to self-deliver — there
// is no central scheduler; every wire reads this one clock independently.
//
// Halt/Resume is the global play/pause gate (MODEL.md: "a single global gate
// halts or resumes wire animation"). While halted, active elapsed does not
// advance, so no wire's WaitUntil deadline is reached — progress freezes.

package Wiring

import (
	"context"
	"sync"
	"time"
)

// Clock is the one monotonic clock the network reads. Now() returns active
// elapsed (paused intervals excluded). WaitUntil blocks until active elapsed
// reaches target (or ctx is done). Halt/Resume is the global play/pause gate.
type Clock interface {
	// Now returns the active elapsed time since the clock started, excluding any
	// intervals during which the clock was halted (pause-aware: elapsed-while-
	// paused is zero).
	Now() time.Duration
	// WaitUntil blocks until Now() >= target, or until ctx is done. Returns
	// ctx.Err() if the context was canceled before the deadline was reached,
	// nil otherwise. Waking is edge-triggered: Resume() and (for the fake)
	// Advance() re-evaluate every waiter.
	WaitUntil(ctx context.Context, target time.Duration) error
	// Halt pauses the clock: active elapsed stops advancing until Resume.
	Halt()
	// Resume un-pauses the clock: active elapsed advances again from where it
	// stopped (no wall-clock catch-up).
	Resume()
}

// RealClock is the production Clock. It tracks active elapsed by subtracting the
// total halted duration from wall-clock elapsed, so pause stops the arithmetic
// while the underlying monotonic clock keeps ticking. Waiters poll the active
// elapsed against their deadline; Resume broadcasts so a freshly-resumed clock
// re-checks every waiter promptly.
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

// nowLocked computes active elapsed; caller must hold c.mu.
func (c *RealClock) nowLocked() time.Duration {
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

// Now returns active elapsed (pause-aware).
func (c *RealClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nowLocked()
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

// Resume un-pauses the clock and wakes all waiters to re-check their deadlines.
func (c *RealClock) Resume() {
	c.mu.Lock()
	if c.halted {
		c.halted = false
		c.haltedTotal += time.Since(c.haltedAt)
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// WaitUntil blocks until active elapsed reaches target (pause-aware) or ctx is
// done. The real clock keeps advancing on its own, so a waiter that is not yet
// at its deadline sleeps for the remaining wall-clock duration and re-checks;
// while halted it parks on cond until Resume. A ctx watcher broadcasts on cancel.
func (c *RealClock) WaitUntil(ctx context.Context, target time.Duration) error {
	stop := c.watchCtx(ctx)
	defer close(stop)

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := c.nowLocked()
		if now >= target {
			return nil
		}
		if c.halted {
			// Frozen: park until Resume (or ctx cancel) broadcasts.
			c.cond.Wait()
			continue
		}
		// Running: sleep for the remaining active duration, then re-check.
		// (Active and wall-clock advance 1:1 while running, so remaining active
		// == remaining wall-clock.) Drop the lock while sleeping so Halt/Resume
		// and ctx cancel can proceed; a timer broadcast re-checks the deadline.
		remaining := target - now
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

// watchCtx broadcasts on c.cond when ctx is done so a parked WaitUntil wakes to
// observe cancellation. The caller closes the returned channel to stop the watcher.
func (c *RealClock) watchCtx(ctx context.Context) chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-stop:
		}
	}()
	return stop
}

// FakeClock is the deterministic Clock for tests. Active elapsed advances ONLY
// via Advance(d) (and never while halted). Advance wakes every waiter so a test
// can drive the whole cascade with no real sleeps: place beads, Advance past
// their inFlightTime, and delivery fires synchronously on the test's timeline.
type FakeClock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	elapsed time.Duration
	halted  bool
}

// NewFakeClock returns a FakeClock at elapsed 0.
func NewFakeClock() *FakeClock {
	c := &FakeClock{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Now returns the fake active elapsed.
func (c *FakeClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.elapsed
}

// Advance moves active elapsed forward by d and wakes every waiter. While
// halted, Advance is a no-op (pause stops the arithmetic). A non-positive d is
// ignored. After Advance returns, every WaitUntil whose deadline is now satisfied
// will observe it (waking is via cond.Broadcast under the same lock).
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	if !c.halted && d > 0 {
		c.elapsed += d
	}
	c.cond.Broadcast()
	c.mu.Unlock()
}

// Halt freezes fake elapsed; subsequent Advance calls are no-ops until Resume.
func (c *FakeClock) Halt() {
	c.mu.Lock()
	c.halted = true
	c.mu.Unlock()
}

// Resume un-freezes fake elapsed and wakes waiters to re-check.
func (c *FakeClock) Resume() {
	c.mu.Lock()
	c.halted = false
	c.cond.Broadcast()
	c.mu.Unlock()
}

// WaitUntil blocks until fake elapsed reaches target or ctx is done. It parks on
// cond and is woken by Advance / Resume / ctx-cancel; on each wake it re-checks
// the deadline. This is fully deterministic — the only thing that moves the
// deadline closer is a test calling Advance.
func (c *FakeClock) WaitUntil(ctx context.Context, target time.Duration) error {
	stop := c.watchCtx(ctx)
	defer close(stop)

	c.mu.Lock()
	defer c.mu.Unlock()
	for c.elapsed < target {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.cond.Wait()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// watchCtx broadcasts on c.cond when ctx is done so a parked WaitUntil wakes.
func (c *FakeClock) watchCtx(ctx context.Context) chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-stop:
		}
	}()
	return stop
}

// Compile-time assertions that both impls satisfy Clock.
var (
	_ Clock = (*RealClock)(nil)
	_ Clock = (*FakeClock)(nil)
)
