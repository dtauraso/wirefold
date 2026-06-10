// stdin_reader_test.go — contract test for RunStdinReader.
//
// Contract: Go's clock drives delivery; the editor→Go bridge is the single "edit"
// CRUD shape (op = create/update/delete/fade) and carries NO delivery signal. This
// test verifies (a) feeding bridge "edit" lines does not crash or hang the reader,
// and (b) delivery is driven by the clock advance, never by a stdin message.

package Wiring

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

func TestRunStdinReaderClockOwnsDelivery(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	slotReg := SlotRegistry{"nodeA.in": pw}
	reg := WireRegistry{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, w := io.Pipe()
	go RunStdinReader(ctx, r, slotReg, reg, nil, nil)

	// Place a bead with a 50ms in-flight time; delivery is timed by the clock.
	const inFlightMs = 50
	sendErr := make(chan error, 1)
	go func() { sendErr <- pw.Send(ctx, 42, beadPlacement{InFlightMs: inFlightMs}) }()
	time.Sleep(10 * time.Millisecond)

	// Feed a benign "edit" fade line. No bridge message delivers a bead — the
	// clock owns delivery — so the bead must stay in flight.
	io.WriteString(w, `{"type":"edit","op":"fade","edges":[]}`+"\n")
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered by a stdin edit message; clock should own delivery")
	}

	// Advance the clock past the in-flight time → the wire delivers.
	clk.Advance(inFlightMs * time.Millisecond)

	v, err := pw.Recv(ctx)
	if err != nil || v != 42 {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}
	pw.Done()

	select {
	case err := <-sendErr:
		if err != nil {
			t.Fatalf("Send returned error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Send did not return after bead placement")
	}
	w.Close()
}

func TestRunStdinReaderUnknownTargetIgnored(t *testing.T) {
	slotReg := SlotRegistry{}
	reg := WireRegistry{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Unknown slot on a delete edit → no panic, reader exits cleanly on ctx cancel.
	r := strings.NewReader(`{"type":"edit","op":"delete","target":"unknown","targetHandle":"in"}` + "\n")
	RunStdinReader(ctx, r, slotReg, reg, nil, nil) // should return without hanging
}

// TestRunStdinReaderEditDeleteCancelsInFlight drives a delete through the NEW
// edit/delete bridge path (not pw.Delete directly) and asserts the Phase-3
// guarantees ride along: an in-flight bead's clock-delivery is canceled and a
// pulse-cancelled is echoed, keyed by the bead's source. This locks in the
// riskiest Phase-5 item — folding deleteEdge into the generic CRUD edit must not
// drop the clock-cancel + cancel-echo behavior.
func TestRunStdinReaderEditDeleteCancelsInFlight(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr
	pw.Target, pw.TargetHandle = "nodeA", "in"
	slotReg := SlotRegistry{"nodeA.in": pw}
	reg := WireRegistry{"e0": pw}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	go RunStdinReader(ctx, r, slotReg, reg, nil, tr)

	// Place a bead with full position-stream identity, advance partway (in flight).
	const inFlightMs = 50.0
	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{2, 4, 0}, P2: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "src", Port: "out"}
	if !pw.TryPlace(33, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}
	clk.Advance(20 * time.Millisecond)

	// Delete through the bridge: edit/delete keyed by the destination slot identity.
	io.WriteString(w, `{"type":"edit","op":"delete","target":"nodeA","targetHandle":"in"}`+"\n")

	// Wait for the reader to apply the delete (it drops the in-flight bead).
	deadline := time.Now().Add(time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("edit/delete did not drop the in-flight bead")
		}
		time.Sleep(time.Millisecond)
	}

	// Advancing past the original deadline must NOT deliver — delivery was canceled.
	clk.Advance(inFlightMs * time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	if _, ok := pw.PollRecv(); ok {
		t.Fatal("edit/delete left a value in the slot; delete must cancel clock-delivery")
	}

	w.Close()
	cancel()
	tr.Close()
	cs := cancelEvents(tr.Events())
	if len(cs) != 1 {
		t.Fatalf("edit/delete emitted %d pulse-cancelled events, want exactly 1; got %+v", len(cs), cs)
	}
	if c := cs[0]; c.Node != "src" || c.Port != "out" || c.Value != 33 {
		t.Fatalf("pulse-cancelled = (%q,%q,%d), want (\"src\",\"out\",33)", c.Node, c.Port, c.Value)
	}
}
