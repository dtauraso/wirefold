// node.go — Node interface for the Go network.
//
// Node is the single interface every node kind must implement.
// The runner (main.RunTest) launches each node in its own goroutine,
// wraps it with defer wg.Done(), and passes a cancellable context.

package Wiring

import "context"

// Node is the Go node interface. Update runs the node's pausable bead event
// loop until ctx is cancelled. UpdateLayout runs the node's pause-INDEPENDENT
// layout-update loop (owns LocalPolars, layout_holder.go) until ctx is
// cancelled; it is not gated by the play/pause clock. Every kind gets
// UpdateLayout for free by embedding Wiring.LayoutHolder.
type Node interface {
	Update(ctx context.Context)
	UpdateLayout(ctx context.Context)
}

// TryEmit calls fn if fn is non-nil. It is the shared nil-guard used by every
// node that has an optional EmitGeometry callback.
func TryEmit(fn func()) {
	if fn != nil {
		fn()
	}
}
