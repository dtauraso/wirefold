// stdin_reader.go — reads JSON-line "delivered" messages from stdin and
// unblocks the corresponding PacedWire.
//
// Message shape: {"type":"delivered","edge":"<edge-label>"}
//
// One goroutine; cancellable via context. On EOF or context cancel, exits
// cleanly. Unknown message types are silently ignored (forward-compat).

package Wiring

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
)

type deliveredMsg struct {
	Type string `json:"type"`
	Edge string `json:"edge"`
}

// RunStdinReader reads JSON lines from r, dispatching "delivered" messages
// to the WireRegistry. Returns when ctx is done or r reaches EOF.
// Call in a goroutine alongside the node run loop.
func RunStdinReader(ctx context.Context, r io.Reader, reg WireRegistry) {
	sc := bufio.NewScanner(r)
	done := ctx.Done()
	lineCh := make(chan string, 8)
	go func() {
		for sc.Scan() {
			lineCh <- sc.Text()
		}
		close(lineCh)
	}()
	for {
		select {
		case <-done:
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			var msg deliveredMsg
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg.Type != "delivered" || msg.Edge == "" {
				continue
			}
			pw, found := reg[msg.Edge]
			if !found {
				continue
			}
			pw.NotifyDelivered()
		}
	}
}
