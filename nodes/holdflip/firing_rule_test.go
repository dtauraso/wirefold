package holdflip

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// value=1 → output should be 1-1=0.
func TestFlipOneToZero(t *testing.T) {
	r := newPacedFlipRig(t)
	defer r.close()
	r.feed(t, 1)
	r.expectFlip(t, 0)
}

// value=0 → output should be 1-0=1.
func TestFlipZeroToOne(t *testing.T) {
	r := newPacedFlipRig(t)
	defer r.close()
	r.feed(t, 0)
	r.expectFlip(t, 1)
}

// TestDrainToLatest verifies that when multiple values arrive in quick succession
// (simulating a Pulse flood), HoldFlip acts on the LATEST value. Feed: 0,0,0,1 →
// latest is 1 → output should settle on 1-1=0, not 1-0=1. Also verifies the
// interior bead reflects the latest input value.
func TestDrainToLatest(t *testing.T) {
	r := newPacedFlipRigWithBead(t)
	defer r.close()

	// Feed the backlog: stale 0s followed by the current value 1, each delivered
	// with 0 latency on the very next clock advance so they queue faster than the
	// paced main loop's own tick cadence.
	r.feed(t, 0)
	r.feed(t, 0)
	r.feed(t, 0)
	r.feed(t, 1)

	// Expect the flipped output to settle on 0 (1-1, because latest input is 1).
	r.expectFlip(t, 0)

	// Held display should reflect the latest input value (1), not any stale value.
	r.mu.Lock()
	defer r.mu.Unlock()
	lastHeld := -1
	for _, v := range r.heldVals {
		if v != gatecommon.NoValue {
			lastHeld = v
		}
	}
	if lastHeld != 1 {
		t.Fatalf("expected held display to show 1 (latest input), got %d (full sequence: %v)", lastHeld, r.heldVals)
	}
}

// TestInteriorBeadUpdatesOnInput verifies that the interior bead (EmitHeldBead)
// updates to the input value when input arrives. The startup sentinel is emitted
// first; after input arrives, the bead should reflect the input value. This is
// the key property of the two-goroutine split: MAIN updates the display the
// instant input arrives, independent of the DRIVE output cycle.
func TestInteriorBeadUpdatesOnInput(t *testing.T) {
	r := newPacedFlipRigWithBead(t)
	defer r.close()

	r.feed(t, 1)
	r.expectFlip(t, 0) // 1-1=0 confirms held was set and drive picked it up.

	r.mu.Lock()
	beads := append([]int(nil), r.heldVals...)
	r.mu.Unlock()

	if len(beads) < 2 {
		t.Fatalf("expected at least 2 bead updates (sentinel + input), got %v", beads)
	}
	if beads[0] != gatecommon.NoValue {
		t.Fatalf("expected first bead to be sentinel %d, got %d", gatecommon.NoValue, beads[0])
	}
	// Find the first non-sentinel bead — should be the input value 1.
	last := -1
	for _, v := range beads {
		if v != gatecommon.NoValue {
			last = v
			break
		}
	}
	if last != 1 {
		t.Fatalf("expected interior bead to show input value 1, got %d (sequence: %v)", last, beads)
	}
}

// --- Paced-path coverage -----------------------------------------------------
//
// The tests above run in CHAN mode. Production wires holdflip with PacedWire +
// the one FakeClock/RealClock, where the DRIVE goroutine's EmitOneDriven BLOCKS
// per wire traversal (self-pacing) instead of spinning into a buffer. This test
// exercises that real paced output drive: it feeds inputs over a paced In wire
// and observes the flipped pulses delivered over a paced Out wire, advancing the
// shared FakeClock to move beads. It asserts the core flip behavior 0->1->0.

// pacedFlipRig wires a holdflip Node with a paced In and paced Out sharing one
// FakeClock, plus an observer In on the output wire to read delivered pulses.
type pacedFlipRig struct {
	clk      *Wiring.FakeClock
	inPw     *Wiring.PacedWire
	observer *Wiring.In
	cancel   context.CancelFunc
	wg       *sync.WaitGroup
	ctx      context.Context
	// heldVals/mu are populated only by newPacedFlipRigWithBead, which wires
	// EmitHeldBead to record every interior-bead update for assertions.
	heldVals []int
	mu       sync.Mutex
}

func newPacedFlipRig(t *testing.T) *pacedFlipRig {
	t.Helper()
	const latMs = 10.0
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr
	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)
	outPw.Trace = tr

	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewInPaced(inPw, ctx, "hf", "In", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "hf", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	// observer reads the flipped pulses the node drives onto the output wire.
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedFlipRig{clk: clk, inPw: inPw, observer: observer, cancel: cancel, wg: &wg, ctx: ctx}
}

// newPacedFlipRigWithBead is newPacedFlipRig plus an EmitHeldBead hook that
// records every interior-bead update into the returned rig's heldVals (guarded
// by its mu), for tests that assert on the interior bead sequence.
func newPacedFlipRigWithBead(t *testing.T) *pacedFlipRig {
	t.Helper()
	const latMs = 10.0
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr
	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)
	outPw.Trace = tr

	r := &pacedFlipRig{clk: clk, cancel: cancel, ctx: ctx}

	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewInPaced(inPw, ctx, "hf", "In", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "hf", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		EmitHeldBead: func(v int) {
			r.mu.Lock()
			r.heldVals = append(r.heldVals, v)
			r.mu.Unlock()
		},
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	r.inPw = inPw
	r.observer = observer
	r.wg = &wg
	return r
}

func (r *pacedFlipRig) close() { r.cancel(); r.wg.Wait() }

// feed places value v on the paced input wire and advances the shared clock so
// the bead is delivered into the node's input slot.
func (r *pacedFlipRig) feed(t *testing.T, v int) {
	t.Helper()
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, v, 0) { // 0 latency: delivered on next advance
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.AdvanceTicks(1)
}

// expectFlip advances the shared clock in wire-latency steps, draining the
// observer, until a pulse carrying want is delivered. The DRIVE goroutine runs
// autonomously (self-pacing on EmitOneDriven), so the loop is a bounded
// advance-and-poll: it always converges once held is set, and the iteration cap
// is only a hang guard — there is no wall-clock timing assertion.
func (r *pacedFlipRig) expectFlip(t *testing.T, want int) {
	t.Helper()
	const latMs = 10.0
	for i := 0; i < 5000; i++ {
		r.clk.AdvanceTicks(int64(latMs))
		for {
			v, ok := r.observer.PollRecv()
			if !ok {
				break
			}
			if v == want {
				return
			}
		}
		// Yield so the autonomous DRIVE goroutine can place its next pulse before
		// the next advance. Scheduling nudge only — not a timing assertion.
		time.Sleep(50 * time.Microsecond)
	}
	t.Fatalf("paced drive never delivered flipped value %d", want)
}

// newPacedFlipRigLat is newPacedFlipRig parameterized by the wire's simulated
// latency (ms), so timing tests can pick a latency wide enough (in ticks) to
// observe multi-tick spacing between consecutive pulses.
func newPacedFlipRigLat(t *testing.T, latMs float64) *pacedFlipRig {
	t.Helper()
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr
	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)
	outPw.Trace = tr

	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewInPaced(inPw, ctx, "hf", "In", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "hf", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedFlipRig{clk: clk, inPw: inPw, observer: observer, cancel: cancel, wg: &wg, ctx: ctx}
}

// TestFlipPacedPulseSpacingConsistent proves the non-blocking (one-StepOnce-
// per-tick) DriveHeld rewrite reproduces the SAME per-tick cadence the old
// blocking DriveBeadToDelivery path did: with a fixed wire latency, the drive
// goroutine places its next pulse bead the instant the previous one is
// delivered, so consecutive same-value pulses are separated by a CONSTANT
// number of ticks (the wire's fixed traversal-tick count, latMs/MsPerTick).
// Advances the shared FakeClock ONE TICK AT A TIME (not in latency-sized
// jumps) so this also exercises StepOnce being driven every tick, matching
// how the goroutine actually paces itself in production.
func TestFlipPacedPulseSpacingConsistent(t *testing.T) {
	const latMs = 160.0 // latMs/MsPerTick(16) = 10 ticks between pulses
	r := newPacedFlipRigLat(t, latMs)
	defer r.close()

	// held starts at NoValue -> DriveHeld emits the NoValue sentinel pulses
	// until input arrives. Feed 0 so the flipped output settles to 1, then
	// measure the tick-spacing between consecutive delivered "1" pulses.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 0, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}

	var arriveTicks []int64
	const maxTicks = 20000
	var tick int64
	for tick = 0; tick < maxTicks && len(arriveTicks) < 4; tick++ {
		r.clk.AdvanceTicks(1)
		time.Sleep(20 * time.Microsecond) // let the woken drive goroutine run
		for {
			v, ok := r.observer.PollRecv()
			if !ok {
				break
			}
			if v == 1 {
				arriveTicks = append(arriveTicks, r.clk.Tick())
			}
		}
	}
	if len(arriveTicks) < 4 {
		t.Fatalf("expected at least 4 delivered '1' pulses within %d ticks, got %v", maxTicks, arriveTicks)
	}

	// Spacing between consecutive arrivals must be CONSTANT (steady-state
	// cadence, once held has settled and the sentinel transient has passed —
	// drop the first gap, which may include leftover sentinel pulses).
	gaps := make([]int64, 0, len(arriveTicks)-2)
	for i := 2; i < len(arriveTicks); i++ {
		gaps = append(gaps, arriveTicks[i]-arriveTicks[i-1])
	}
	for i, g := range gaps {
		if g != gaps[0] {
			t.Fatalf("pulse spacing not constant: gaps=%v (arrivals=%v)", gaps, arriveTicks)
		}
		_ = i
	}
	if gaps[0] <= 0 {
		t.Fatalf("non-positive pulse spacing: %v", gaps)
	}
}

// TestFlipPacedCancelExitsPromptly proves the DRIVE goroutine is NOT parked
// inside a full traversal: cancelling ctx makes it exit promptly even with NO
// further clock advancement (a blocking multi-tick park would only unblock via
// its own ctx watcher too, but this asserts the exit happens within a short
// WALL-CLOCK bound, not requiring the test to drive the fake clock through an
// entire traversal to observe it).
func TestFlipPacedCancelExitsPromptly(t *testing.T) {
	const latMs = 160.0
	r := newPacedFlipRigLat(t, latMs)

	// Get the drive goroutine into an in-flight (mid-traversal) state: place an
	// input bead and advance a couple of ticks — not enough for a full 10-tick
	// traversal to complete.
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 0, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.AdvanceTicks(2)
	time.Sleep(5 * time.Millisecond)

	r.cancel()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		// exited promptly, no further clock advancement needed.
	case <-time.After(2 * time.Second):
		t.Fatal("drive goroutine did not exit promptly on ctx cancel (appears parked)")
	}
}

// TestFlipPacedPath drives the core flip behavior over real PacedWires + FakeClock:
// input 0 -> output 1, then input 1 -> output 0 (1-held each time).
func TestFlipPacedPath(t *testing.T) {
	r := newPacedFlipRig(t)
	defer r.close()

	// held := 0 → drive pulses 1-0 = 1.
	r.feed(t, 0)
	r.expectFlip(t, 1)

	// held := 1 → drive pulses 1-1 = 0.
	r.feed(t, 1)
	r.expectFlip(t, 0)

	// held := 0 again → back to 1 (0->1->0 round trip on the flipped output).
	r.feed(t, 0)
	r.expectFlip(t, 1)
}
