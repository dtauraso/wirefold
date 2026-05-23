// stdin_reader_test.go — contract test for RunStdinReader.
//
// Verifies that a "delivered" JSON line on stdin calls NotifyDelivered on the
// matching PacedWire, unblocking Recv; Send unblocks after Done is called.

package Wiring

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunStdinReaderDelivered(t *testing.T) {
	pw := NewPacedWire()
	reg := WireRegistry{"e1": pw}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Feed one "delivered" message then close.
	r, w := io.Pipe()
	go RunStdinReader(ctx, r, reg)

	// Send a value on pw in the background; it will block until delivered.
	sendErr := make(chan error, 1)
	go func() {
		sendErr <- pw.Send(ctx, 42)
	}()

	// Wait briefly to let the send goroutine block.
	time.Sleep(10 * time.Millisecond)

	// Write the delivered message — unblocks Recv (visual delivery).
	io.WriteString(w, `{"type":"delivered","edge":"e1"}`+"\n")
	time.Sleep(10 * time.Millisecond)

	// Recv the value, then call Done — Done unblocks Send.
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
		t.Fatal("Send did not unblock after delivered message")
	}
	w.Close()
}

func TestRunStdinReaderUnknownEdgeIgnored(t *testing.T) {
	reg := WireRegistry{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Unknown edge label → no panic, reader exits cleanly on ctx cancel.
	r := strings.NewReader(`{"type":"delivered","edge":"unknown"}` + "\n")
	RunStdinReader(ctx, r, reg) // should return without hanging
}
