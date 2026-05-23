// paced_wire_novisual.go — Stage 2 stub pacer.
//
// NoVisualBridge wraps a PacedWire between two raw channels so that
// existing In/Out port wrappers require no changes. A bridge goroutine
// drains the source channel, calls pw.Send (which blocks until
// NotifyDelivered), then forwards the value to the sink channel.
// The stub pacer calls NotifyDelivered immediately, making the bridge
// behave identically to a direct channel connection for now.
// Stage 3 replaces the stub with a real webview round-trip.

package Wiring

import "context"

// NoVisualBridge connects src → PacedWire → dst and starts the bridge
// goroutine. The goroutine exits when ctx is cancelled.
// Returns the PacedWire so Stage 3 can replace the stub pacer.
func NoVisualBridge(ctx context.Context, src <-chan int, dst chan<- int) *PacedWire {
	pw := NewPacedWire()
	go func() {
		for {
			// Wait for a value from the upstream channel.
			var v int
			select {
			case <-ctx.Done():
				return
			case v = <-src:
			}
			// Place value into the paced wire; blocks until NotifyDelivered.
			if err := pw.Send(ctx, v); err != nil {
				return
			}
			// Forward to downstream channel.
			select {
			case <-ctx.Done():
				return
			case dst <- v:
			}
		}
	}()
	// Stub pacer: acknowledge delivery immediately.
	// Waits under mu until hasSend is true (value placed by bridge Send),
	// then calls NotifyDelivered so Send unblocks. Loops for the next value.
	go func() {
		for {
			pw.mu.Lock()
			for !pw.hasSend && ctx.Err() == nil {
				pw.cond.Wait()
			}
			if ctx.Err() != nil {
				pw.mu.Unlock()
				return
			}
			pw.mu.Unlock()
			pw.NotifyDelivered()
		}
	}()
	return pw
}
