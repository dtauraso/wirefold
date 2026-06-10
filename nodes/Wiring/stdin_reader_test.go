// stdin_reader_test.go — contract test for RunStdinReader.
//
// Phase 1 contract: Go's clock drives delivery, NOT the "delivered" stdin
// message. RunStdinReader parses and ignores "delivered"; the wire delivers when
// the fake clock is advanced past the bead's in-flight time. This test verifies
// (a) feeding a "delivered" line does not crash or hang the reader, and (b)
// delivery is driven by the clock advance, not by the message.

package Wiring

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunStdinReaderDeliveredIgnoredClockDelivers(t *testing.T) {
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
	go func() { sendErr <- pw.Send(ctx, 42, inFlightMs) }()
	time.Sleep(10 * time.Millisecond)

	// Feed a "delivered" line. Under the Phase 1 contract this is parsed and
	// ignored — it must NOT deliver the bead.
	io.WriteString(w, `{"type":"delivered","target":"nodeA","targetHandle":"in"}`+"\n")
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered by stdin 'delivered' message; clock should own delivery")
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

func TestRunStdinReaderUnknownEdgeIgnored(t *testing.T) {
	slotReg := SlotRegistry{}
	reg := WireRegistry{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Unknown slot → no panic, reader exits cleanly on ctx cancel.
	r := strings.NewReader(`{"type":"delivered","target":"unknown","targetHandle":"in"}` + "\n")
	RunStdinReader(ctx, r, slotReg, reg, nil, nil) // should return without hanging
}
