// node.go — Node interface for the Go network.
//
// Node is the single interface every node kind must implement.
// The runner (main.RunTest) launches each node in its own goroutine,
// wraps it with defer wg.Done(), and passes a cancellable context.

package Wiring

import "context"

// Node is the Go node interface. Update runs the node's event
// loop until ctx is cancelled.
type Node interface {
	Update(ctx context.Context)
}
