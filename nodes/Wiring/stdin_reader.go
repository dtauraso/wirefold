// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// Supported message shapes:
//   {"type":"delivered","edge":"<edge-label>"}
//   {"type":"fade","edges":["<edge-id>", ...]}
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

type stdinMsg struct {
	Type  string   `json:"type"`
	Edge  string   `json:"edge"`
	Edges []string `json:"edges"`
}

// RunStdinReader reads JSON lines from r, dispatching messages
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
		if err := sc.Err(); err != nil {
			// Scan encountered an error reading from r; log and continue.
			// The channel close will unblock the main select loop.
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
			var msg stdinMsg
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "delivered":
				if msg.Edge == "" {
					continue
				}
				pw, found := reg[msg.Edge]
				if !found {
					continue
				}
				pw.NotifyDelivered()
			case "fade":
				// Build a set of faded edge ids for O(1) lookup.
				faded := make(map[string]bool, len(msg.Edges))
				for _, id := range msg.Edges {
					faded[id] = true
				}
				// Apply wholesale: set every wire's faded flag.
				reg.ForEach(func(id string, pw *PacedWire) {
					pw.SetFaded(faded[id])
				})
			}
		}
	}
}
