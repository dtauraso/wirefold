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

// --- Paced-path rig -----------------------------------------------------
//
// Production wires pulse with PacedWire + a shared FakeClock/RealClock (see
// holdflip's pacedFlipRig). This rig wires a pulse Node with a paced In and
// paced Out(s) sharing one FakeClock, plus an observer In on the output wire
// to read delivered pulses.

type pacedPulseRig struct {
	clk      *Wiring.FakeClock
	inPw     *Wiring.PacedWire
	observer *Wiring.In
	beadCh   chan int
	cancel   context.CancelFunc
	wg       *sync.WaitGroup
	ctx      context.Context
}

func newPacedPulseRig(t *testing.T) *pacedPulseRig {
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

	beadCh := make(chan int, 16)
	node := &Node{
		Fire:      func() { tr.Fire("pulse") },
		FromInput: Wiring.NewInPaced(inPw, ctx, "pulse", "FromInput", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "pulse", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	// observer reads the pulses the node drives onto the output wire.
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedPulseRig{clk: clk, inPw: inPw, observer: observer, beadCh: beadCh, cancel: cancel, wg: &wg, ctx: ctx}
}

func (r *pacedPulseRig) close() { r.cancel(); r.wg.Wait() }

// feed places value v on the paced input wire and advances the shared clock so
// the bead is delivered into the node's input slot.
func (r *pacedPulseRig) feed(t *testing.T, v int) {
	t.Helper()
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, v, 0) { // 0 latency: delivered on next advance
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.AdvanceTicks(1)
}

// expectDrive advances the shared clock in wire-latency steps, draining the
// observer, until a pulse carrying want is delivered.
func (r *pacedPulseRig) expectDrive(t *testing.T, want int) {
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
		time.Sleep(50 * time.Microsecond)
	}
	t.Fatalf("paced drive never delivered value %d", want)
}

// The held value the node drives out equals the sampled input value.
func TestPulseDrivesHeldInput(t *testing.T) {
	r := newPacedPulseRig(t)
	defer r.close()
	r.feed(t, 5)
	r.expectDrive(t, 5)

	r2 := newPacedPulseRig(t)
	defer r2.close()
	r2.feed(t, 42)
	r2.expectDrive(t, 42)
}

// Before any input the node self-emits the gatecommon.NoValue sentinel on Out (not
// precondition-gated). driveOutput must run from startup.
func TestPulseEmitsSentinelBeforeInput(t *testing.T) {
	r := newPacedPulseRig(t)
	defer r.close()
	r.expectDrive(t, gatecommon.NoValue)
}

// The interior held bead updates to the input value the instant input arrives.
func TestPulseHeldBeadUpdatesOnInput(t *testing.T) {
	r := newPacedPulseRig(t)
	defer r.close()
	r.feed(t, 9)

	deadline := time.After(testHangGuard)
	var last int = gatecommon.NoValue
	for {
		select {
		case v := <-r.beadCh:
			if v != gatecommon.NoValue {
				last = v
			}
		case <-deadline:
			t.Fatalf("interior bead never showed input value, last=%d", last)
		}
		if last == 9 {
			break
		}
	}
}
