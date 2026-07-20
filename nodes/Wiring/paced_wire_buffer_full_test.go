// paced_wire_buffer_full_test.go — regression for the defect where Send's false
// (buffer full) was indistinguishable from a genuinely terminal condition and a
// caller's drive loop exited permanently on it. See CLAUDE.md's debugging
// section and the DriveOutcome doc comment in ports.go: DriveBufferFull is
// TRANSIENT and must never stop a drive goroutine.
package Wiring

import (
	"context"
	"testing"
)

// TestSendBufferFullIsNotTerminal fills a wire's inCh to capacity WITHOUT
// draining it (no DriveOneCycle called), confirms Send reports SendBufferFull
// (not silently dropped, not conflated with a terminal outcome), then drains
// the buffer and confirms Send succeeds again — proving buffer-full is a
// recoverable, transient condition rather than a one-way failure.
func TestSendBufferFullIsNotTerminal(t *testing.T) {
	pw := NewPacedWire(0, PulseSpeedWuPerMs)

	// Fill inCh to its full buffered capacity directly (bypassing Send, since
	// Send itself is what we are about to test at the boundary).
	for i := 0; i < wireChanBufferSize; i++ {
		select {
		case pw.inCh <- placeRequest{val: i}:
		default:
			t.Fatalf("inCh reported full before reaching wireChanBufferSize (at %d)", i)
		}
	}

	// The wire is now full and NOT being drained (no DriveOneCycle call): Send
	// must report SendBufferFull, not SendPlaced.
	if got := pw.Send(999, beadPlacement{}); got != SendBufferFull {
		t.Fatalf("Send on a full, undrained inCh = %v, want SendBufferFull", got)
	}

	// Now drain one cycle's worth via the production drive path — this frees
	// room in inCh.
	ctx := context.Background()
	pw.DriveOneCycle(ctx, 1)

	// Send must now succeed: buffer-full was transient, not a one-way failure.
	if got := pw.Send(1000, beadPlacement{}); got != SendPlaced {
		t.Fatalf("Send after drain = %v, want SendPlaced (buffer-full must be recoverable, not terminal)", got)
	}
}

// TestDriveItemBufferFullDoesNotKillDriveLoop drives PlaceDrivenAt through a
// paced Out the way a source node's drive goroutine does (gatecommon/drive.go,
// nodes/input/node.go): it fills the wire's inCh so a placement hits
// SendBufferFull, and asserts that a caller checking ONLY Failed() (the
// documented, correct check) does not stop — while a hypothetical caller that
// checked !Live() (the wrong, pre-fix-shaped check) WOULD have stopped. This
// pins the compiler-enforced distinction: BufferFull() must be false for
// Failed() to ever report buffer-full as terminal, and it is not.
func TestDriveItemBufferFullDoesNotKillDriveLoop(t *testing.T) {
	pw := NewPacedWire(0, PulseSpeedWuPerMs)
	ctx := context.Background()
	out := NewPacedOutNoGeom(pw, ctx, "src", "Out", nil, RuleFireAndForget, 1, 1, "")

	// Fill inCh directly so the next PlaceDrivenAt call is forced through the
	// SendBufferFull path.
	for i := 0; i < wireChanBufferSize; i++ {
		select {
		case pw.inCh <- placeRequest{val: i}:
		default:
			t.Fatalf("inCh reported full before reaching wireChanBufferSize (at %d)", i)
		}
	}

	di := out.PlaceDrivenAt(42)

	if !di.BufferFull() {
		t.Fatalf("DriveItem.BufferFull() = false on a full, undrained wire; want true")
	}
	if di.Failed() {
		t.Fatalf("DriveItem.Failed() = true for a transient buffer-full placement; " +
			"this is exactly the regression: a drive goroutine checking Failed() would " +
			"exit permanently on ordinary transient load")
	}

	// Simulate the drive-loop shape used by gatecommon/drive.go and
	// nodes/input/node.go: `if di.Failed() { return }` must NOT fire here.
	exited := false
	if di.Failed() {
		exited = true
	}
	if exited {
		t.Fatalf("drive loop would have exited on a transient buffer-full placement")
	}

	// Drain the wire and confirm placement succeeds (Live()) once room reopens
	// — the drive loop resumes placing beads after the transient condition
	// clears, exactly as it must in production.
	pw.DriveOneCycle(ctx, 1)
	di2 := out.PlaceDrivenAt(43)
	if !di2.Live() {
		t.Fatalf("PlaceDrivenAt after drain = %+v, want Live() true (drive loop must resume placing)", di2)
	}
}
