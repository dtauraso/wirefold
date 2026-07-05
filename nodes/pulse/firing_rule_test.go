package pulse

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// testHangGuard bounds how long a test waits for an async drive output. Success
// happens the instant the awaited value arrives; the guard is only for a stuck run.
const testHangGuard = 10 * time.Second

// outBufTestCap sizes the chan-mode out buffer generously. In chan mode the drive
// goroutine spins emitting startup sentinels with no pacing and EXITS the moment
// the buffer is full (EmitOneDriven returns false). A large buffer guarantees the
// main loop sets held before the drive could fill it. Paced production never spins.
const outBufTestCap = 4096

// drainForFirstReal receives from out and reports the first non-sentinel (real
// held) value. A dedicated drainer keeps the chan-mode drive alive (see holdflip).
func drainForFirstReal(ctx context.Context, out <-chan int) <-chan int {
	res := make(chan int, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case v := <-out:
				if v == gatecommon.NoValue {
					continue
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

// SPEC contract (pulse/SPEC.md): sample-and-hold. Holds one int (init -1) and
// drives it out continuously — even before input. When a value arrives on
// FromInput the held value updates and subsequent Out pulses carry the new value.
// firstPulse feeds one value and returns the first non-sentinel value driven on Out.
func firstPulse(t *testing.T, value int) int {
	t.Helper()
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, outBufTestCap)
	node := &Node{
		Fire:      func() {},
		FromInput: Wiring.NewIn(in, "pulse", "FromInput", tr),
		Out:       Wiring.NewOut(out, "pulse", "Out", tr),
	}
	in <- value // pre-load: chan-mode TryRecv is non-blocking

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	res := drainForFirstReal(ctx, out)
	select {
	case v := <-res:
		cancel()
		wg.Wait()
		return v
	case <-time.After(testHangGuard):
		cancel()
		wg.Wait()
		t.Fatal("drive never delivered the held value")
		return gatecommon.NoValue
	}
}

// The held value the node drives out equals the sampled input value.
func TestPulseDrivesHeldInput(t *testing.T) {
	if got := firstPulse(t, 5); got != 5 {
		t.Fatalf("expected Out to drive held value 5, got %d", got)
	}
	if got := firstPulse(t, 42); got != 42 {
		t.Fatalf("expected Out to drive held value 42, got %d", got)
	}
}

// Before any input the node self-emits the gatecommon.NoValue sentinel on Out (not
// precondition-gated). driveOutput must run from startup.
func TestPulseEmitsSentinelBeforeInput(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1) // never fed
	out := make(chan int, outBufTestCap)
	node := &Node{
		Fire:      func() {},
		FromInput: Wiring.NewIn(in, "pulse", "FromInput", tr),
		Out:       Wiring.NewOut(out, "pulse", "Out", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	select {
	case v := <-out:
		if v != gatecommon.NoValue {
			t.Fatalf("expected startup sentinel %d before any input, got %d", gatecommon.NoValue, v)
		}
	case <-time.After(testHangGuard):
		t.Fatal("no sentinel emitted before input (drive did not start)")
	}
	cancel()
	wg.Wait()
}

// The interior held bead updates to the input value the instant input arrives.
func TestPulseHeldBeadUpdatesOnInput(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, outBufTestCap)
	beadCh := make(chan int, 16)
	node := &Node{
		Fire:         func() {},
		FromInput:    Wiring.NewIn(in, "pulse", "FromInput", tr),
		Out:          Wiring.NewOut(out, "pulse", "Out", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	in <- 9

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	// Keep Out drained so the chan-mode drive goroutine stays alive.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-out:
			}
		}
	}()

	// startup sentinel then the input value 9.
	deadline := time.After(testHangGuard)
	var last int = gatecommon.NoValue
	for {
		select {
		case v := <-beadCh:
			if v != gatecommon.NoValue {
				last = v
			}
		case <-deadline:
			cancel()
			wg.Wait()
			t.Fatalf("interior bead never showed input value, last=%d", last)
		}
		if last == 9 {
			break
		}
	}
	cancel()
	wg.Wait()
}
