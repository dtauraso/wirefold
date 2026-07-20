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
// current integer tick, advancing at the current playback speed.
type Clock interface {
	// Tick returns the current tick since the clock started. Ticks accrue as
	// SCALED wall time — wall elapsed integrated against the playback speed (see
	// SetSpeed) — so at speed 1 it is plain wall time, at 2 it advances twice as
	// fast, and at 0 it holds. It is monotonic non-decreasing for the process life.
	Tick() int64
	// SleepCycle blocks for exactly one WALL clock cycle (or until ctx is done).
	// It is the primitive for one-cycle pacing loops; it does not read Tick()
	// itself, so it is immune to a tick advancing between the call and the wait.
	// It sleeps WALL time regardless of playback speed — the loop re-reads Tick()
	// to see how many scaled ticks actually elapsed, so speed scaling lives in
	// Tick(), not in the sleep cadence.
	SleepCycle(ctx context.Context) error
	// SetSpeed sets the global playback-speed multiplier applied to tick advance
	// (0 = frozen, 1 = normal, 2 = double). Everything timed in ticks — bead
	// travel, in-node animation, node/gate windows — scales together because they
	// all read Tick(). Continuity is automatic: Tick() is a continuous function of
	// wall time (piecewise-linear across speed changes), so a bead's fractional
	// progress t=(now−placement)/crossTicks never jumps when the speed changes.
	SetSpeed(speed float64)
	// Copy returns a clock a single goroutine can OWN from this point on. Per
	// per-goroutine-clock.md: a goroutine calls Copy() exactly ONCE, at its own
	// start, and uses only the returned clock thereafter — never a second call
	// mid-loop, and the returned clock must never be handed to a second
	// goroutine (that would just re-share the same object under a new name).
	// *RealClock returns a pointer to a fresh value-copy of itself, so the two
	// clocks share no memory: the copy inherits the origin/accScaled/speed by
	// value and from then on SetSpeed on one is invisible to the other,
	// correctly, with nothing left to lock. inertClock returns itself — it is
	// stateless (Tick is a constant 0, SetSpeed a no-op), so "copying" it has
	// nothing to duplicate and sharing the single value is harmless.
	Copy() Clock
}

// RealClock is the production Clock: SCALED wall-clock elapsed since start, floored
// to a tick via MsPerTick. "Scaled" = wall time integrated against the playback
// speed — at speed s the tick advances s× as fast as wall time. Speed changes
// accumulate the scaled elapsed up to the change instant, then continue from the
// new slope, so Tick() stays continuous and monotonic across changes (the same
// accumulate-on-transition shape the removed halt gate used, generalized from
// {0,1} to an arbitrary non-negative multiplier). Nothing waits on this clock via
// a condition variable — pacing loops call SleepCycle (wall time.After) and
// re-check Tick() themselves.
type RealClock struct {
	// No mutex here on purpose. The mutex this struct used to carry existed for a
	// contention shape that no longer applies: many pacing-loop readers of Tick()
	// racing the one SetSpeed writer, all reaching through ONE shared *RealClock.
	// Per-goroutine-clock (docs/planning/visual-editor/per-goroutine-clock.md)
	// removes that shape by ownership rather than by locking — a RealClock is now
	// held by exactly ONE goroutine, which is the only thing that ever reads or
	// writes it. There is no second goroutine to race, so there is nothing left to
	// guard. A mutex on state nobody else touches is not "extra safety," it is dead
	// weight documenting a sharing relationship that no longer exists.
	//
	// Deleting mu is also what makes RealClock legal to COPY: `sync.Mutex` is a
	// `go vet` copylocks violation, so as long as it lived here the struct could
	// only be passed by pointer — which is exactly the "one object, many holders"
	// shape being removed. With mu gone, `c2 := *c1` is a plain value copy, and
	// that copy is how a goroutine gets ITS OWN clock: it dereference-copies from
	// an existing RealClock, inheriting its origin/accScaled/speed by value, and
	// from then on is independent — SetSpeed on one copy is invisible to the
	// other, correctly, because nothing is shared to make it otherwise.
	// speed is the current playback multiplier (>= 0). Default 1.
	speed float64
	// accScaled is scaled elapsed accumulated across all PRIOR speed segments, up
	// to lastChange. The live segment (lastChange → now) is added at read time.
	accScaled time.Duration
	// lastChange is the wall instant the current speed segment began (construction
	// or the last SetSpeed).
	lastChange time.Time
}

// NewRealClock returns a started RealClock at speed 1, anchored at the current
// monotonic instant.
func NewRealClock() *RealClock {
	return &RealClock{speed: 1, lastChange: time.Now()}
}

// scaledElapsed returns total scaled elapsed = accumulated prior segments + the
// live segment (wall time since lastChange × current speed). No locking: only
// the owning goroutine ever calls this.
func (c *RealClock) scaledElapsed() time.Duration {
	live := time.Duration(float64(time.Since(c.lastChange)) * c.speed)
	total := c.accScaled + live
	if total < 0 {
		total = 0
	}
	return total
}

// Tick returns the current tick: scaled elapsed floored to ticks.
func (c *RealClock) Tick() int64 {
	return int64(c.scaledElapsed() / tickPeriod)
}

// SetSpeed sets the playback-speed multiplier. It banks the scaled elapsed of the
// segment that just ended, then starts a new segment at the new speed — so Tick()
// is continuous across the change (no jump). A negative value is clamped to 0.
func (c *RealClock) SetSpeed(speed float64) {
	if speed < 0 {
		speed = 0
	}
	now := time.Now()
	c.accScaled += time.Duration(float64(now.Sub(c.lastChange)) * c.speed)
	c.lastChange = now
	c.speed = speed
}

// Copy returns a pointer to a fresh value-copy of c: a plain struct copy (legal
// now that mu is gone — see the field comment above), inheriting the current
// speed/accScaled/lastChange by value. The caller goroutine owns the result
// from here on; nothing is shared with c or any other copy taken from it.
func (c *RealClock) Copy() Clock {
	cp := *c
	return &cp
}

// SleepCycle blocks for one WALL tickPeriod, or until ctx is done.
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
func (inertClock) SetSpeed(float64) {}

// Copy returns itself: inertClock is stateless (Tick is a constant 0,
// SetSpeed a no-op), so there is nothing per-instance to duplicate and
// sharing the single zero-size value across goroutines is harmless.
func (c inertClock) Copy() Clock { return c }

// Compile-time assertion that inertClock satisfies Clock.
var _ Clock = inertClock{}

// NewInertClock returns the same inert, never-nil, never-spinning Clock that an
// unwired In falls back to (inertClock, unexported so it can't be constructed
// outside this package). It is exported so a node kind that holds a bare
// `Wiring.Clock` struct field injected by reflectBuild (rather than derived
// from a wired port's In.Clock/Out.Clock) can SEED that field to a real, safe
// default at construction instead of leaving it as an unrepresentable-nil trap
// on test builds without a loader. See input.Node.Clock's doc comment for the
// motivating case: reflectBuild injects by matching the field's exact type, so
// a rename silently misses it and an unguarded `clk.Tick()` panics with no
// recover over the node goroutine — the same hazard ports.go's In.Clock()
// comment describes for PORT-derived clocks, reintroduced via a bare field.
func NewInertClock() Clock { return inertClock{} }
