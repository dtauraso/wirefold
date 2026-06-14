package Wiring

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// testInFlightMs is the per-bead in-flight time used across these wire tests.
// Delivery is timed on the (fake) clock: place a bead, Advance the clock by this
// amount, and the wire delivers it into the delivered FIFO.
const testInFlightMs = 50

// newFakeWire builds a PacedWire backed by a FakeClock the test advances.
func newFakeWire() (*PacedWire, *FakeClock) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	return pw, clk
}

// placeAndDrive places a bead WITHOUT a walker and drives it to delivery on a
// background goroutine — the test-side replacement for the deleted walker. clk.Advance
// then moves the bead into the delivered FIFO exactly as before.
func placeAndDrive(pw *PacedWire, val int, bp beadPlacement) bool {
	return pw.PlaceAndDrive(context.Background(), val, bp)
}

// waitDelivered advances clk past one bead's in-flight time and waits until at
// least `want` values sit in the delivered FIFO (a bead landed). timeout guards
// against a missed wake.
func waitDelivered(t *testing.T, pw *PacedWire, clk *FakeClock, want int) {
	t.Helper()
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("clock delivery did not produce %d delivered values (have %d)", want, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestMultiBeadFIFO: a wire carries several beads at once. Send 3 distinct
// values, advance the clock past all deadlines, and Recv must return them in
// SEND ORDER with none dropped.
func TestMultiBeadFIFO(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	for _, v := range []int{10, 20, 30} {
		if !placeAndDrive(pw, v, beadPlacement{InFlightMs: testInFlightMs}) {
			t.Fatalf("placeAndDrive %d: returned false", v)
		}
	}
	if !pw.InFlight() {
		t.Fatal("expected beads in flight after 3 sends")
	}

	// Advance past every bead's deadline; all three deliver.
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 3 delivered, have %d", n)
		}
		time.Sleep(time.Millisecond)
	}

	// Recv returns all three in FIFO send order.
	for _, want := range []int{10, 20, 30} {
		v, err := pw.Recv(ctx)
		if err != nil || v != want {
			t.Fatalf("Recv: v=%v err=%v want %d", v, err, want)
		}
	}
}

// TestSendNeverBlocks: place 5 beads with no Recv. All 5 are placed (inflight grows,
// none dropped) and each placeAndDrive returns immediately.
func TestSendNeverBlocks(t *testing.T) {
	pw, _ := newFakeWire()

	for i := 0; i < 5; i++ {
		if !placeAndDrive(pw, i, beadPlacement{InFlightMs: testInFlightMs}) {
			t.Fatalf("placeAndDrive %d returned false — wire must never park", i)
		}
	}

	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 5 {
		t.Fatalf("expected 5 in-flight beads (none dropped), got %d", n)
	}
}

// TestSendRecvClockDelivery: happy-path send→clock-deliver→recv.
func TestSendRecvClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if !placeAndDrive(pw, 42, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}

	recvDone := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvDone <- v
	}()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvDone:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvDone:
		if v != 42 {
			t.Fatalf("got %v, want 42", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advanced past in-flight time")
	}
}

// TestPollRecvEmpty: PollRecv returns (nil,false) immediately when empty.
func TestPollRecvEmpty(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	done := make(chan struct{})
	go func() {
		v, ok := pw.PollRecv()
		if ok || v != nil {
			t.Errorf("PollRecv on empty: got (%v,%v), want (nil,false)", v, ok)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("PollRecv blocked on empty slot")
	}
}

// TestPollRecvConsumes: after clock delivery, PollRecv returns the value and
// CONSUMES it (a repeat poll sees the next value, or empty). Recv/PollRecv now
// consume on read — there is no Done step.
func TestPollRecvConsumes(t *testing.T) {
	pw, clk := newFakeWire()

	if !placeAndDrive(pw, 7, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}
	waitDelivered(t, pw, clk, 1)

	v, ok := pw.PollRecv()
	if !ok || v != 7 {
		t.Fatalf("PollRecv: got (%v,%v), want (7,true)", v, ok)
	}
	// Consumed: a repeat poll is empty.
	if v2, ok2 := pw.PollRecv(); ok2 || v2 != nil {
		t.Fatalf("PollRecv after consume: got (%v,%v), want (nil,false)", v2, ok2)
	}
}

// TestMultipleSendsNoDrop: with one bead already in flight, a second Send is NOT
// dropped — both deliver and both are received in order (replaces the old
// single-bead "second send drops/blocks" coverage).
func TestMultipleSendsNoDrop(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if !placeAndDrive(pw, 1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("first placeAndDrive returned false")
	}
	if !placeAndDrive(pw, 2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("second placeAndDrive returned false")
	}
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight (no drop), got %d", n)
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	v1, err1 := pw.Recv(ctx)
	v2, err2 := pw.Recv(ctx)
	if err1 != nil || err2 != nil || v1 != 1 || v2 != 2 {
		t.Fatalf("Recv order: v1=%v v2=%v err1=%v err2=%v", v1, v2, err1, err2)
	}
}

// TestRecvBlocksWhenEmpty: Recv with a short-timeout context times out when
// nothing is sent.
func TestRecvBlocksWhenEmpty(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := pw.Recv(ctx)
	if err != ErrCanceled {
		t.Fatalf("expected ErrCanceled, got %v", err)
	}
}

// TestRecvCancelUnblocks: a Recv blocked on an empty wire returns ErrCanceled
// when its context is canceled.
func TestRecvCancelUnblocks(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	ctx, cancel := context.WithCancel(context.Background())

	result := make(chan error, 1)
	go func() {
		_, err := pw.Recv(ctx)
		result <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err != ErrCanceled {
			t.Fatalf("expected ErrCanceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Recv did not unblock on ctx cancel")
	}
}

// TestClockDeliveryGate: Recv is gated on the clock advance, not on placement.
func TestClockDeliveryGate(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if !placeAndDrive(pw, 5, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}

	recvResult := make(chan any, 1)
	go func() {
		v, _ := pw.Recv(ctx)
		recvResult <- v
	}()
	time.Sleep(5 * time.Millisecond)
	select {
	case <-recvResult:
		t.Fatal("Recv returned before clock advanced to delivery")
	default:
	}

	clk.Advance(testInFlightMs * time.Millisecond)
	select {
	case v := <-recvResult:
		if v != 5 {
			t.Fatalf("Recv: got %v want 5", v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Recv did not unblock after clock advanced to delivery")
	}
}

// TestFadedSendSkips: a faded wire returns false from placeAndDrive immediately
// without placing a bead.
func TestFadedSendSkips(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	pw.SetFaded(true)

	if placeAndDrive(pw, 99, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("faded placed a bead")
	}

	if pw.InFlight() {
		t.Fatal("faded placeAndDrive placed a bead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := pw.Recv(ctx); err != ErrCanceled {
		t.Fatalf("Recv after faded placeAndDrive: expected ErrCanceled, got %v", err)
	}
}

// TestUnfadedAfterSetFaded: after SetFaded(false), Send works normally again.
func TestUnfadedAfterSetFaded(t *testing.T) {
	pw, clk := newFakeWire()
	pw.SetFaded(true)
	pw.SetFaded(false)
	ctx := context.Background()

	if !placeAndDrive(pw, 11, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}
	waitDelivered(t, pw, clk, 1)
	v, err := pw.Recv(ctx)
	if err != nil || v != 11 {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}
}

// TestPauseFreezesDelivery: while halted, advancing the clock does not move
// active elapsed, so a bead's deadline is never reached and it stays in flight.
func TestPauseFreezesDelivery(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if !placeAndDrive(pw, 5, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}

	clk.Halt()
	clk.Advance(10 * testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered while clock was halted; pause must stop the arithmetic")
	}

	clk.Resume()
	waitDelivered(t, pw, clk, 1)
	v, err := pw.Recv(ctx)
	if err != nil || v != 5 {
		t.Fatalf("Recv after resume: v=%v err=%v", v, err)
	}
}

// TestDeliveryAtExactInFlightTime: the bead delivers exactly when active elapsed
// reaches the in-flight time.
func TestDeliveryAtExactInFlightTime(t *testing.T) {
	pw, clk := newFakeWire()
	ctx := context.Background()

	if !placeAndDrive(pw, 9, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}

	clk.Advance((testInFlightMs - 1) * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatalf("bead delivered before elapsed reached in-flight time (%d ms)", testInFlightMs)
	}

	clk.Advance(1 * time.Millisecond)
	v, err := pw.Recv(ctx)
	if err != nil || v != 9 {
		t.Fatalf("Recv at exact in-flight time: v=%v err=%v", v, err)
	}
}

// TestPlaceNeverDrops: placeAndDrive always places (multi-bead model) — a second
// place on a busy wire still succeeds and both beads are in flight.
func TestPlaceNeverDrops(t *testing.T) {
	pw, _ := newFakeWire()

	if !placeAndDrive(pw, 1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive on clear wire: expected true")
	}
	if !placeAndDrive(pw, 2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive on busy wire: expected true (multi-bead, no drop)")
	}
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight, got %d", n)
	}
}

// TestPlaceDrivenFanout: PlaceDriven places each bead on a paced Out; a second
// placement on a busy wire is NOT dropped (multi-bead model). This test only
// asserts placement — beads are not driven to delivery.
func TestPlaceDrivenFanout(t *testing.T) {
	pw, _ := newFakeWire()
	o := NewOutPaced(pw, context.Background(), "n", "p", T.New(16), RuleFireAndForget, 100, 100/PulseSpeedWuPerMs, wireSegment{}, "")

	o.PlaceDriven(7)
	o.PlaceDriven(8)
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 in-flight, got %d", n)
	}
}

// TestDeleteSilencesWire: after Delete(), placeAndDrive places no bead.
func TestDeleteSilencesWire(t *testing.T) {
	pw, _ := newFakeWire()

	pw.Delete()

	if placeAndDrive(pw, 1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("placeAndDrive after Delete placed a bead")
	}
	if pw.InFlight() {
		t.Fatalf("placeAndDrive after Delete placed a bead")
	}
	if placeAndDrive(pw, 2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatalf("placeAndDrive after Delete: got true, want false")
	}
	if pw.InFlight() {
		t.Fatalf("second placeAndDrive after Delete placed a bead")
	}
}

// TestDeleteCancelsClockDelivery: a clock-delivery deadline reached AFTER Delete
// must be a no-op — nothing is delivered.
func TestDeleteCancelsClockDelivery(t *testing.T) {
	pw, clk := newFakeWire()

	if !placeAndDrive(pw, 42, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive returned false")
	}
	pw.Delete()

	clk.Advance(testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	pw.mu.Lock()
	n := len(pw.delivered)
	pw.mu.Unlock()
	if n != 0 {
		t.Fatal("clock delivery fired on deleted wire; Delete must cancel pending deliveries")
	}
}

// TestDeleteCancelsAllInFlight: Delete with multiple beads in flight drops them
// all and none are delivered after the deadline.
func TestDeleteCancelsAllInFlight(t *testing.T) {
	pw, clk := newFakeWire()

	for _, v := range []int{1, 2, 3} {
		if !placeAndDrive(pw, v, beadPlacement{InFlightMs: testInFlightMs}) {
			t.Fatalf("placeAndDrive %d returned false", v)
		}
	}
	pw.Delete()

	clk.Advance(testInFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	pw.mu.Lock()
	in, del := len(pw.inflight), len(pw.delivered)
	pw.mu.Unlock()
	if in != 0 || del != 0 {
		t.Fatalf("Delete left beads: inflight=%d delivered=%d", in, del)
	}
}

func TestRestoreUnsilencesWire(t *testing.T) {
	pw, _ := newFakeWire()

	pw.Delete()
	pw.Restore()

	if !placeAndDrive(pw, 1, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive after Restore returned false")
	}
	if !pw.InFlight() {
		t.Fatalf("placeAndDrive after Restore did not place a bead")
	}

	pw2, _ := newFakeWire()
	pw2.Delete()
	pw2.Restore()
	if !placeAndDrive(pw2, 2, beadPlacement{InFlightMs: testInFlightMs}) {
		t.Fatal("placeAndDrive after Restore (pw2): got false, want true")
	}
	if !pw2.InFlight() {
		t.Fatalf("placeAndDrive after Restore (pw2) did not place a bead")
	}
}

// TestMultipleBeadsAllDelivered: placing multiple beads delivers ALL of them in FIFO
// order — no dedup, no drop. This replaces the old train-collapse test.
func TestMultipleBeadsAllDelivered(t *testing.T) {
	pw, clk := newFakeWire()

	// Place 3 beads via placeAndDrive (no walker goroutines; driven on background goroutines).
	for _, v := range []int{7, 7, 9} {
		placeAndDrive(pw, v, beadPlacement{InFlightMs: testInFlightMs})
	}

	// All 3 in-flight.
	pw.mu.Lock()
	n := len(pw.inflight)
	pw.mu.Unlock()
	if n != 3 {
		t.Fatalf("expected 3 in-flight beads, got %d", n)
	}

	// Advance clock past all deadlines.
	clk.Advance(testInFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		pw.mu.Lock()
		n = len(pw.delivered)
		pw.mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 3 beads delivered", n)
		}
		time.Sleep(time.Millisecond)
	}

	// All 3 beads are delivered in send order — NO dedup.
	for i, want := range []int{7, 7, 9} {
		v, ok := pw.PollRecv()
		if !ok || v != want {
			t.Fatalf("bead %d: got v=%v ok=%v, want v=%d ok=true", i, v, ok, want)
		}
	}
	// Queue is empty.
	if v, ok := pw.PollRecv(); ok {
		t.Fatalf("unexpected extra bead: v=%v", v)
	}
}
