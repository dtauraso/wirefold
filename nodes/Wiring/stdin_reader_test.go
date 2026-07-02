// stdin_reader_test.go — contract test for RunStdinReader.
//
// Contract: Go's clock drives delivery; the editor→Go bridge is the single "edit"
// CRUD shape (exactly three ops: create/update/delete) and carries NO delivery signal. This
// test verifies (a) feeding bridge "edit" lines does not crash or hang the reader,
// and (b) delivery is driven by the clock advance, never by a stdin message.

package Wiring

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// TestRunStdinReaderLargeLineNotDropped feeds a single stdin line well over the
// default 64 KB bufio.Scanner token limit and asserts the message is parsed (its
// side effect — a scene write — lands) rather than silently dropped. Without the
// raised sc.Buffer, bufio.ErrTooLong would close lineCh and deafen the bridge.
func TestRunStdinReaderLargeLineNotDropped(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	go RunStdinReader(ctx, r, SlotRegistry{}, nil, nil, nil, root)

	// Build a scene blob whose serialized line exceeds 64 KB (~200 KB payload).
	big := strings.Repeat("x", 200*1024)
	blob, err := json.Marshal(map[string]any{"note": big})
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(map[string]any{
		"type":  "edit",
		"op":    "update",
		"kind":  "scene",
		"scene": json.RawMessage(blob),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(line) <= 64*1024 {
		t.Fatalf("test line only %d bytes; must exceed the 64 KB default to exercise the fix", len(line))
	}
	io.WriteString(w, string(line)+"\n")

	scenePath := filepath.Join(root, "view", "scene.json")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(scenePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("large scene line was dropped: scene.json never written (bridge deafened by 64 KB limit)")
		}
		time.Sleep(2 * time.Millisecond)
	}
	got, err := os.ReadFile(scenePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 200*1024 {
		t.Fatalf("scene.json is %d bytes; large payload not fully written", len(got))
	}
	w.Close()
}

func TestRunStdinReaderClockOwnsDelivery(t *testing.T) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	slotReg := SlotRegistry{"nodeA.in": pw}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, w := io.Pipe()
	go RunStdinReader(ctx, r, slotReg, nil, nil, nil, "")

	// Place a bead with a 50ms in-flight time; delivery is timed by the clock.
	const inFlightMs = 50
	if !placeAndDrive(pw, 42, beadPlacement{InFlightMs: inFlightMs}) {
		t.Fatal("placeAndDrive returned false on fresh wire")
	}
	time.Sleep(10 * time.Millisecond)

	// Feed a benign "edit" fade line. No bridge message delivers a bead — the
	// clock owns delivery — so the bead must stay in flight.
	io.WriteString(w, `{"type":"edit","op":"update","kind":"edge","attr":"faded","edges":{}}`+"\n")
	time.Sleep(10 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered by a stdin edit message; clock should own delivery")
	}

	// Advance the clock past the in-flight time → the wire delivers.
	clk.AdvanceTicks(inFlightMs)

	v, err := pw.Recv(ctx)
	if err != nil || v != 42 {
		t.Fatalf("Recv: v=%v err=%v", v, err)
	}
	w.Close()
}

func TestRunStdinReaderUnknownTargetIgnored(t *testing.T) {
	slotReg := SlotRegistry{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Unknown slot on a delete edit → no panic, reader exits cleanly on ctx cancel.
	r := strings.NewReader(`{"type":"edit","op":"delete","target":"unknown","targetHandle":"in"}` + "\n")
	RunStdinReader(ctx, r, slotReg, nil, nil, nil, "") // should return without hanging
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := io.Pipe()
	go RunStdinReader(ctx, r, slotReg, nil, tr, nil, "")

	// Place a bead with full position-stream identity, advance partway (in flight).
	const inFlightMs = 50.0
	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{4, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "src", Port: "out"}
	if !placeAndDrive(pw, 33, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}
	clk.AdvanceTicks(20)

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
	clk.AdvanceTicks(int64(inFlightMs))
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
