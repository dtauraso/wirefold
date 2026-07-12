package Wiring

import (
	"context"
	"testing"
	"time"
)

// TestLayoutGoroutineAppliesDragWithNoBeadLoop pins the two-goroutine node model
// (split-layout-bead-goroutines.md): position/drag handling lives in a node's
// dedicated always-on layout goroutine (LayoutPort.run), NOT in its pausable bead
// Update loop. The whole node-1 sphere-not-moving-on-drag bug class is made
// unrepresentable by this split — there is simply no bead loop involved in
// draining the layout port, so nothing the bead loop waits on (feedback, a frozen
// tick while paused) can stall a drag.
//
// The test runs the layout goroutine with NO node/bead goroutine present at all
// and asserts an injected drag is applied. If a drag lands even with zero bead
// activity, then a fortiori it lands while the bead loop is paused.
func TestLayoutGoroutineAppliesDragWithNoBeadLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	applied := make(chan vec3, 1)
	p := NewLayoutPort("1")
	// applyDirect is what Handle invokes for a drag-origin Direct message; the
	// loader wires it to nodeMover.applyCenter. Here it just records the center so
	// the test can observe that the layout goroutine drained and applied the drag.
	p.applyDirect = func(center vec3, reach float64) {
		select {
		case applied <- center:
		default:
		}
	}

	// Only the layout goroutine runs — deliberately no bead/Update goroutine.
	go p.run(ctx)

	want := vec3{X: 42, Y: 7, Z: -3}
	p.InjectDirect(want, 5)

	select {
	case got := <-applied:
		if got != want {
			t.Fatalf("layout goroutine applied center %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("layout goroutine never applied the injected drag: " +
			"position handling must be owned by the always-on layout goroutine, " +
			"independent of any bead loop")
	}
}
