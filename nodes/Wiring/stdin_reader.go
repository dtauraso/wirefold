// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// The editor→Go bridge carries two top-level message kinds:
//
//  1. Geometry-CRUD edits (type=="edit") — op discriminates the operation:
//     {"type":"edit","op":"create","target":"<node-id>","targetHandle":"<port>"}
//     {"type":"edit","op":"delete","target":"<node-id>","targetHandle":"<port>"}
//     {"type":"edit","op":"update","nodeId":"<id>","x":<f64>,"y":<f64>,"z":<f64>}
//     {"type":"edit","op":"fade","edges":{"<edge-id>":true|false,...}}
//
//  2. Play/pause control (type=="play" / type=="pause") — routes directly to the
//     clock's global gate (Halt/Resume). The process starts halted; the first
//     "play" message resumes bead delivery. "pause" re-halts.
//
// Go owns the clock and delivery; nothing on this seam triggers delivery or
// carries animation internals.
//
// One goroutine; cancellable via context. On EOF or context cancel, exits
// cleanly. Unknown message types and ops are silently ignored (forward-compat).

package Wiring

import (
	"bufio"
	"context"
	"encoding/json"
	"io"

	T "github.com/dtauraso/wirefold/Trace"
)

// EdgeEndpoints identifies the source and target node IDs (and the port handles)
// for one edge. Handles are needed to recompute the port-to-port arc length.
type EdgeEndpoints struct {
	Source       string
	Target       string
	SourceHandle string
	TargetHandle string
}

// moveEntry is one (key → position) value in a node-move "update" message. NodeId is
// the node that moved; the key it is routed under is either that node's id or an
// incident edge id (the dispatch is a mail-sort, see RunStdinReader / MoveDispatch).
type moveEntry struct {
	NodeId string  `json:"nodeId"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
}

// stdinMsg is the single editor→Go bridge shape. type is always "edit"; op
// discriminates the CRUD/animation operation. The remaining fields are the union
// of every op's payload (only the fields for the active op are populated).
//
// For op=="update" (node-move), Entries maps each routing key (the moved node id
// AND each incident edge id) to the moved node's new position. The reader mail-sorts
// each entry to channels[key]; the owning node/edge goroutine recomputes.
type stdinMsg struct {
	Type         string               `json:"type"`
	Op           string               `json:"op"`
	Target       string               `json:"target"`
	TargetHandle string               `json:"targetHandle"`
	Edges        map[string]bool      `json:"edges"`
	Entries      map[string]moveEntry `json:"entries"`
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity used to resolve an edit's create/delete op
// to the wire owned by that destination port.
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads JSON lines from r, dispatching geometry-CRUD "edit"
// messages and play/pause clock-gate control messages. Returns when ctx is done
// or r reaches EOF. Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" and resolves create/delete ops to the
// destination port's wire. md may be nil; if non-nil, update (node-move) and
// fade ops mail-sort each entry to the owning node/edge goroutine's inbox.
// tr emits control breadcrumbs for the edit ops.
// clk may be nil; if non-nil, "play" calls clk.Resume() and "pause" calls clk.Halt().
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, clk Clock) {
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
			// Two top-level bridge kinds:
			//   "edit"  — geometry-CRUD; op discriminates the operation (internal axis).
			//   "play"  — resume the clock's global gate (bead delivery starts).
			//   "pause" — halt the clock's global gate (bead delivery freezes).
			switch msg.Type {
			case "edit":
				applyEdit(msg, slotReg, md, tr)
			case "play":
				if clk != nil {
					clk.Resume()
				}
			case "pause":
				if clk != nil {
					clk.Halt()
				}
			}
		}
	}
}

// applyEdit dispatches one geometry-CRUD/animation edit by its op. It is the
// internal op-axis of the single "edit" bridge shape; the op values are matched by
// value (==) rather than a case-literal switch so they stay invisible to the
// message-kind-parity guard, which fences only top-level msg.Type kinds.
//
// Ops:
//   - create: un-silence the destination port's wire (edge re-added) — Restore.
//   - delete: silence the wire AND cancel any in-flight bead's clock-delivery,
//     echoing pulse-cancelled (PacedWire.Delete owns both, atomically).
//   - update: mail-sort the node-move entries to the owning node/edge inboxes; each
//     owning goroutine recomputes its own geometry (no central recompute here).
//   - fade:   mail-sort each (edgeId,faded) entry to the owning edgeMover via
//     md.dispatch; each wire sets its own flag.
//
// Unknown ops are ignored (forward-compat).
func applyEdit(msg stdinMsg, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace) {
	switch {
	case msg.Op == "create":
		if msg.Target == "" || msg.TargetHandle == "" {
			return
		}
		tr.Breadcrumb("edit-create-recv", msg.Target, msg.TargetHandle, "")
		destKey := msg.Target + "." + msg.TargetHandle
		pw, found := slotReg[destKey]
		if !found {
			tr.Breadcrumb("edit-create-notfound", msg.Target, msg.TargetHandle, destKey)
			return
		}
		tr.Breadcrumb("edit-create-restore", pw.Target, pw.TargetHandle, "")
		pw.Restore()
	case msg.Op == "delete":
		if msg.Target == "" || msg.TargetHandle == "" {
			return
		}
		tr.Breadcrumb("edit-delete-recv", msg.Target, msg.TargetHandle, "")
		destKey := msg.Target + "." + msg.TargetHandle
		pw, found := slotReg[destKey]
		if !found {
			tr.Breadcrumb("edit-delete-notfound", msg.Target, msg.TargetHandle, destKey)
			return
		}
		// "delete" breadcrumb emitted here (PacedWire.Delete has no Trace reference)
		// carrying the wire's authoritative slot identity. Delete cancels any
		// in-flight bead's clock-delivery and echoes pulse-cancelled atomically.
		tr.Breadcrumb("delete", pw.Target, pw.TargetHandle, "")
		tr.Breadcrumb("edit-delete-delete", msg.Target, msg.TargetHandle, destKey)
		pw.Delete()
	case msg.Op == "update":
		if md == nil || len(msg.Entries) == 0 {
			return
		}
		// Mail-sort: push each entry to its key's inbox. Unknown keys are ignored.
		// No recompute, no topology logic here — the owning goroutine does the work.
		for key, e := range msg.Entries {
			if ch, ok := md.dispatch[key]; ok {
				ch <- moveMsg{NodeID: e.NodeId, X: e.X, Y: e.Y, Z: e.Z}
			}
		}
	case msg.Op == "fade":
		// Mail-sort: push each (edgeId, faded) entry to its edge's inbox. Each
		// edgeMover sets its OWN wire's faded flag — no central fan-out. Unknown
		// keys are ignored (forward-compat).
		if md == nil || len(msg.Edges) == 0 {
			return
		}
		for edgeID, faded := range msg.Edges {
			if ch, ok := md.dispatch[edgeID]; ok {
				ch <- moveMsg{Kind: moveMsgKindFade, Faded: faded}
			}
		}
	}
}
