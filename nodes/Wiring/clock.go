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
	// Copy returns a clock a single goroutine can OWN from this point on. Per
	// per-goroutine-clock.md: a goroutine calls Copy() exactly ONCE, at its own
	// start, and uses only the returned clock thereafter — never a second call
	// mid-loop, and the returned clock must never be handed to a second
	// goroutine (that would just re-share the same object under a new name).
	// *RealClock returns a pointer to a fresh value-copy of itself, so the two
	// clocks share no memory: the copy inherits the origin/accScaled/speed by
	// value and from then on a speed change on one is invisible to the other,
	// correctly, with nothing left to lock.
	Copy() Clock
}

// SetSpeed is deliberately NOT on the Clock interface (per-goroutine-clock.md
// item 4): once a clock is a per-goroutine COPY, nothing outside the goroutine
// that owns it may mutate it — a second goroutine reaching in to call SetSpeed
// on someone else's copy is exactly the shared-object shape this model removes.
// (*RealClock).SetSpeed stays a concrete, exported method: the owning goroutine
// still needs to apply a speed change to ITS OWN copy. How a speed change is
// DELIVERED to every live copy is a separate, not-yet-built step (see
// per-goroutine-clock.md "Delivery"); stdin_reader.go's current SetSpeed call
// site type-asserts down to *RealClock and is a KNOWN, documented no-op today
// (see its own comment) — not this interface's concern.

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
	// Per-goroutine ownership removes that shape by ownership rather than by
	// locking — a RealClock is now
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

// ApplySpeedNonBlocking is the delivery half of per-goroutine-clock.md
// "Delivery": every paced loop grows exactly this one poll, folded into its
// existing sleep/select point. speedCh is a buffered-1, latest-wins channel
// built once at load time (see loader.go / builders.go) and owned from then on
// by exactly the one goroutine that reads it — nothing else may read it, so no
// lock is needed. A pending value (if any) is drained and applied to clk's OWN
// copy via SetSpeed; an empty channel is a no-op; a nil channel (unwired
// goroutines, or test builds constructed with no loader) is always a no-op
// too, since a receive on a nil channel is never selected. This is
// non-blocking on purpose — a goroutine that is not yet awake must never be
// forced to wake early just to drain its inbox.
func ApplySpeedNonBlocking(clk Clock, speedCh <-chan float64) {
	select {
	case sp := <-speedCh:
		if rc, ok := clk.(*RealClock); ok {
			rc.SetSpeed(sp)
		}
	default:
	}
}

// SendSpeedNonBlocking is the send half of Delivery: it delivers speed to one
// clock-holder's buffered-1 channel WITHOUT blocking on a goroutine that may be
// asleep or never reads. If the buffer already holds a stale pending value
// (a rapid second change arrived before the holder woke to drain the first),
// that stale value is dropped and replaced — LATEST WINS is correct because
// speed is absolute state, not an event stream (per-goroutine-clock.md
// "Delivery"). ch must be a channel this call's caller alone sends on (the
// stdin-reader goroutine, which is the sole writer of every channel collected
// at load) — sending from two goroutines onto the same ch would race the
// drain-then-send pair below.
func SendSpeedNonBlocking(ch chan float64, speed float64) {
	select {
	case ch <- speed:
		return
	default:
	}
	// Buffer full: drain the stale value, then place the new one. Both steps are
	// non-blocking; if some other reader raced us and drained it first between
	// the two selects, the second send below still succeeds (buffer now empty).
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- speed:
	default:
	}
}

// inertClock is GONE (per-goroutine-clock.md API demolition item 3). It existed only
// because an INJECTED clock could be ABSENT: an unwired In needed a non-nil thing to
// return from a port accessor, and reflectBuild's type-matched field injection meant a
// rename could silently inject nothing, leaving an unguarded clk.Tick() to panic with no
// recover over the node goroutine. A goroutine that constructs (or Copies) its own clock
// cannot have a nil one — every clock-holder now gets a real *RealClock, seeded from the
// loader's origin at construction, so there is no "absent" case left to paper over. Do
// not reintroduce a placeholder/inert Clock implementation; that is exactly the
// unrepresentable-nil trap this deletion removes.
