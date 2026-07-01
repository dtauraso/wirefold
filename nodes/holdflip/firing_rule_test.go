package holdflip

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// testHangGuard bounds how long a test waits for an async node output. It is a
// HANG GUARD, not a timing assertion: the node always eventually emits, so the
// test succeeds the instant the awaited event arrives. The value is large so that
// the success path never depends on a wall-clock window — under `-race` with many
// parallel runs, wire pacing slows and a tight deadline (the old 200ms) flaked.
const testHangGuard = 10 * time.Second

// outBufTestCap sizes the chan-mode out buffer generously. The node's drive
// goroutine spins emitting startup sentinels with no pacing in chan mode and EXITS
// the moment the buffer is full (EmitOneDriven returns false). A small buffer can
// fill before the main loop sets held — under `-race` the drainer goroutine can be
// scheduled late — so the drive dies before any real value flows. A large buffer
// guarantees the main loop wins the race (sets held within a handful of iterations)
// long before the drive could fill the buffer, keeping the drive alive until a real
// value is emitted. Paced production never spins, so this is a chan-mode-only knob.
const outBufTestCap = 4096

// drainForFirstReal continuously receives from out and reports the first
// non-sentinel value on the returned channel. The dedicated drainer matters in
// CHAN MODE (these tests): there Out.EmitOneDriven is non-blocking and the node's
// drive goroutine EXITS the instant the out buffer is full (EmitOneDriven returns
// false). A select-based reader that interleaves other work can starve the drainer
// under `-race`, let the buffer fill with startup sentinels, and kill the drive
// before any real value flows — a permanent hang. A goroutine that does nothing but
// `<-out` keeps the buffer drained so the drive stays alive until held is set.
// (Production uses paced mode, where EmitOneDriven blocks per wire traversal and
// self-paces, so this busy-fill-and-die shape never occurs.)
func drainForFirstReal(ctx context.Context, out <-chan int) <-chan int {
	res := make(chan int, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case v := <-out:
				if v == -1 {
					continue // sentinel: no value held yet
				}
				select {
				case res <- v:
				default:
				}
			}
		}
	}()
	return res
}

// firstFlip sends value into a node and returns the first non-sentinel output
// (i.e., the first output after input arrives, which should be 1-value).
// The DRIVE goroutine emits -1 sentinel until held is set; we skip those.
func firstFlip(value int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, outBufTestCap)
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
	}
	// Pre-load input BEFORE launching Update. In chan mode (this test) In.TryRecv is
	// non-blocking, so if the main loop runs before the send it sees an empty channel
	// and returns immediately — held never gets set and only sentinels stream (the
	// flake: -1 returned after the deadline). Production uses paced mode where TryRecv
	// blocks, so this race is test-construction only; the buffered (cap 1) channel
	// makes the pre-load safe. Matches the other tests, which already pre-load.
	in <- value

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	res := drainForFirstReal(ctx, out)
	select {
	case v := <-res:
		cancel()
		wg.Wait()
		return v, nil
	case <-time.After(testHangGuard):
		cancel()
		wg.Wait()
		return -1, nil
	}
}

// value=1 → output should be 1-1=0.
func TestFlipOneToZero(t *testing.T) {
	got, _ := firstFlip(1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// value=0 → output should be 1-0=1.
func TestFlipZeroToOne(t *testing.T) {
	got, _ := firstFlip(0)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// TestDrainToLatest verifies that when multiple values are queued (simulating a
// Pulse flood), HoldFlip acts on the LATEST value. Queue: 0,0,0,1 → latest is 1
// → output should be 1-1=0, not 1-0=1. Also verifies the interior bead reflects
// the latest input value.
func TestDrainToLatest(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	// Buffer large enough to pre-load the backlog before the node reads.
	in := make(chan int, 8)
	out := make(chan int, outBufTestCap)
	heldVals := []int{}
	var mu sync.Mutex
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
		EmitHeldBead: func(v int) {
			mu.Lock()
			heldVals = append(heldVals, v)
			mu.Unlock()
		},
	}
	// Pre-load the backlog: stale 0s followed by the current value 1.
	in <- 0
	in <- 0
	in <- 0
	in <- 1

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Expect the first non-sentinel output to be 0 (1-1, because latest input is 1).
	res := drainForFirstReal(ctx, out)
	var firstReal int
	select {
	case firstReal = <-res:
	case <-time.After(testHangGuard):
		cancel()
		wg.Wait()
		t.Fatal("timed out waiting for output")
	}
	cancel()
	wg.Wait()

	if firstReal != 0 {
		t.Fatalf("expected output 0 (latest input was 1, flip → 0), got %d", firstReal)
	}

	// Held display should reflect the latest input value (1), not any stale value.
	mu.Lock()
	defer mu.Unlock()
	lastHeld := -1
	for _, v := range heldVals {
		if v != -1 {
			lastHeld = v
		}
	}
	if lastHeld != 1 {
		t.Fatalf("expected held display to show 1 (latest input), got %d (full sequence: %v)", lastHeld, heldVals)
	}
}

// TestInteriorBeadUpdatesOnInput verifies that the interior bead (EmitHeldBead)
// updates to the input value when input arrives. The startup sentinel (-1) is
// emitted first; after input arrives, the bead should reflect the input value.
// This is the key property of the two-goroutine split: MAIN updates the display
// the instant input arrives, independent of the DRIVE output cycle.
func TestInteriorBeadUpdatesOnInput(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, outBufTestCap)
	beadCh := make(chan int, 8)
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
		EmitHeldBead: func(v int) {
			beadCh <- v
		},
	}
	// Pre-load input before starting so TryRecv (non-blocking in chan mode) finds it.
	in <- 1

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Collect bead updates: expect startup sentinel (-1) then input value (1).
	var beads []int
	deadline := time.After(testHangGuard)
loop:
	for len(beads) < 2 {
		select {
		case v := <-beadCh:
			beads = append(beads, v)
		case <-deadline:
			break loop
		}
	}
	cancel()
	wg.Wait()

	if len(beads) < 2 {
		t.Fatalf("expected at least 2 bead updates (sentinel + input), got %v", beads)
	}
	if beads[0] != -1 {
		t.Fatalf("expected first bead to be sentinel -1, got %d", beads[0])
	}
	// Find the first non-sentinel bead — should be the input value 1.
	last := -1
	for _, v := range beads {
		if v != -1 {
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

func (r *pacedFlipRig) close() { r.cancel(); r.wg.Wait() }

// feed places value v on the paced input wire and advances the shared clock so
// the bead is delivered into the node's input slot.
func (r *pacedFlipRig) feed(t *testing.T, v int) {
	t.Helper()
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, v, 0) { // 0 latency: delivered on next advance
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.Advance(1 * time.Millisecond)
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
		r.clk.Advance(latMs * time.Millisecond)
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
