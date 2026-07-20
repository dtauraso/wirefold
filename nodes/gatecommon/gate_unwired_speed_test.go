package gatecommon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// syncBuffer is a concurrency-safe io.Writer, since Trace.Breadcrumb writes
// from the gate's own goroutine while the test reads on the main goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestGateWithUnwiredOutputStillObeysSpeed reproduces the reported bug: a gate
// whose ToPassed output has NO live wire (Paced()==false — exactly node 9/10's
// WindowAndInhibit*Gate in the shipped topology, whose ToPassed has no
// consuming edge) must still measure its window/dwell timing — and therefore
// its interior-bead present/absent flicker — off a SPEED-AWARE clock, not the
// loader's origin clock (which nothing ever applies a speed change to; see
// per-goroutine-clock.md). Before the fix, RunGate picked its "now" source
// off `g.ToPassed.Paced()`, so an unwired gate silently fell back to the deaf
// origin clock and its window timer ran at wall speed regardless of the
// slider.
//
// This only opens the window (feeds FromLeft, never FromRight) so the gate
// never fires; the window-clear timeout (WindowMs=3000) is the only visible
// timing event, and it is entirely driven by the "now" source under test.
func TestGateWithUnwiredOutputStillObeysSpeed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dbg syncBuffer
	tr := T.NewWithSink(0, &dbg)
	defer tr.Close()

	const latMs = 10.0
	leftPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	rightPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)

	clk := Wiring.NewRealClock()
	go stepPacedWire(ctx, leftPw, clk.Copy())
	go stepPacedWire(ctx, rightPw, clk.Copy())

	leftSrc := Wiring.NewPacedOutNoGeom(leftPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")

	speedCh := make(chan float64, 1)
	g := &GateNode{
		Fire:      func() {},
		Clock:     clk,
		SpeedCh:   speedCh,
		FromLeft:  Wiring.NewInPaced(leftPw, ctx, "g9", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(rightPw, ctx, "g9", "FromRight", tr),
		// ToPassed is a chan-mode Out (nil PacedWire) — Paced() reports false,
		// matching node 9/10's unwired output in the shipped topology.
		ToPassed: Wiring.NewOutChanForTest(make(chan int, 1), "g9", "ToPassed", tr),
	}

	done := make(chan struct{})
	go func() { RunGate(ctx, g, true); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("RunGate did not exit after cancel")
		}
	}()

	// Freeze speed at 0 BEFORE opening the window, so the window never even
	// gets a chance to start advancing on a not-yet-scaled clock.
	speedCh <- 0

	if !leftSrc.PlaceDrivenAt(1).Live() {
		t.Fatal("PlaceDrivenAt(left) returned false")
	}

	// Wait for the window to open (real wall time; cheap and doesn't depend on
	// the clock speed we're testing).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(dbg.String(), "window_open") {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(dbg.String(), "window_open") {
		t.Fatal("window never opened")
	}

	// WindowMs is 3000 (windowTicks*MsPerTick). At speed 0 the gate's own tick
	// must not advance, so window_clear must NOT appear even after well past
	// that real-time budget.
	time.Sleep(3800 * time.Millisecond)
	if strings.Contains(dbg.String(), "window_clear") {
		t.Fatal("window cleared at speed 0 — gate's window/dwell timing is not speed-aware " +
			"(reproduces the node-9/10 unwired-output bug: RunGate fell back to the deaf " +
			"origin clock instead of its own Clock.Copy())")
	}
}

// stepPacedWire mirrors windowandinhibitleftgate's firing_rule_lean_test.go
// stepWire helper: continuously StepOnceAts pw on a short wall-clock poll,
// matching the production per-cycle StepOnceAt delivery path.
func stepPacedWire(ctx context.Context, pw *Wiring.PacedWire, clk Wiring.Clock) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		pw.DriveOneCycle(ctx, clk.Tick())
		time.Sleep(time.Millisecond)
	}
}
