// stdin_reader.go — reads FRAMED BINARY records from stdin and dispatches them.
//
// The editor→Go bridge is a purely BINARY buffer (symmetric with the Go→TS content
// buffer on fd 3): each message is a binary RECORD written FRAMED as [len:u32-LE][record]
// to stdin. input_codec.go decodes a record into the stdinMsg below; the dispatch switch
// and every handler (applyEdit / HandleRawInput / play-pause) are UNCHANGED —
// only the wire decode moved from newline-JSON to framed binary.
//
// The editor→Go bridge carries these top-level message kinds (all fully binary; no JSON
// on the wire — see input_codec.go). This list is the AUTHORITATIVE doc for the dispatch
// switch below and is checked against it by tools/check-message-kind-parity.sh: every type
// fenced by MSG_TYPES_START/END must be declared here and vice versa. Adding a case without
// adding a numbered entry (or the reverse) fails the guard.
//
// MSG_TYPES_DOC_START
//
//  1. "edit" — geometry-CRUD. EXACTLY THREE ops: create/update/delete.
//     create/delete add or remove an edge by destination slot. update sets an ATTRIBUTE on
//     a typed entity; the sole live entity is overlays:
//       create (record 20): un-silence the destination slot's wire.
//       delete (record 21): silence it + cancel any in-flight bead.
//       update overlays attr=toggle: flip one named overlay flag.
//     Camera / node-move / port-anchor / edge-fade are NOT edits: the gesture FSM produces
//     them in-process from raw-input, so they never cross this seam as an edit op. (Fade
//     still crosses — as the bare fade-toggle command in 6, not as an edit.)
//
//  2. "play" / "pause" — control. Routes directly to the clock's global gate (Halt/Resume).
//     The process starts halted; the first play message resumes bead delivery; pause
//     re-halts.
//
//  3. "save" — Go persists its OWN authoritative scene state (overlay visibility →
//     scene.json, preserving the Go-owned cameraPolar). Bare command, no payload; the
//     editor holds no authoritative scene document.
//
//  4. "raw-input" — a raw pointer/wheel event + stateless raycast hit, handed to the
//     gesture FSM.
//
//  5. "fade-toggle" — flips fade on the CURRENT SELECTION. A bare command carrying no
//     entity id BY DESIGN: Go owns the selection, so there is nothing for TS to address.
//     That is why fade is not an edit op=update on an entity.
//
// A remounted webview that has nothing new to render (Go paused/idle) is served from the
// EXT HOST's cached last fd3 snapshot instead of asking Go to manufacture one — see
// runCommand.ts's BuildAndRunRunner.lastSnapshot/getLastSnapshot. Go has no "resend"
// concept: it emits a frame only when something changes, and that stays true here.
//
// MSG_TYPES_DOC_END
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
	"encoding/binary"
	"fmt"
	"io"
	"os"

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

// stdinCRUDPayload holds the destination-slot fields for the create/delete ops.
type stdinCRUDPayload struct {
	Target       string
	TargetHandle string
}

// stdinMsg is the single editor→Go bridge shape. For type=="edit", op is one of exactly
// three values (create/update/delete). create/delete name a destination slot (Target/
// TargetHandle). op=="update" sets an attribute on a typed entity — the sole live entity is
// overlays: Attr=="toggle" (Flag names one overlay). The other top-level types are
// raw-input (Event) and the bare commands (play/pause/save/fade-toggle).
//
// These structs carry NO json tags: this seam is framed binary end to end and nothing
// unmarshals them (input_codec.go decodes the record). The wire field order is the
// INPUT_LAYOUT_FINGERPRINT, not a struct tag.
type stdinMsg struct {
	Type string
	Op   string
	Kind string
	Attr string
	Flag string
	// Event is the payload for the top-level type=="raw-input" message; nil otherwise.
	Event *rawInputMsg
	stdinCRUDPayload
}

// rawInputMsg carries the payload for a top-level type=="raw-input" message (Phase 6):
// a single RAW pointer/wheel event plus the stateless three.js raycast hit. Go's gesture
// state machine (gesture.go) decides what it means — TS does not interpret it. Mirrors the
// TS RawInputEvent (messages.ts); the field ORDER is pinned by INPUT_LAYOUT_FINGERPRINT
// (input_codec.go), not by struct tags — this seam is framed binary, never JSON.
type rawInputMsg struct {
	Kind       string // pointerdown | pointermove | pointerup | wheel | home
	X          float64
	Y          float64 // client pixel X/Y
	RectLeft   float64
	RectTop    float64
	RectWidth  float64
	RectHeight float64
	Button     int // 0 primary, 2 secondary; -1 for move/wheel
	Ctrl       bool
	Shift      bool
	Alt        bool
	Meta       bool
	DeltaX     float64
	DeltaY     float64
	Fov        float64
	Hit        rawHit
}

// rawHit is the classified raycast hit: which rendered entity is under the pointer and its
// world point. Kind ∈ port|handhold|node|edge|torus|empty. Topology facts (e.g. connected?)
// are NOT carried — Go's FSM decides those from its own held state.
type rawHit struct {
	Kind string
	// PortRow is the numeric buffer PORT-ROW index for a port hit (the port InstancedMesh
	// instanceId == its buffer port row). -1 (or absent) when not a port hit. Go resolves
	// this row → (node, port) via its own port-row table (portFromHit); no port name crosses
	// the bridge.
	PortRow int
	// EdgeRow is the numeric buffer EDGE-ROW index for an edge hit (the edge's pick-halo
	// carries its buffer edge row). -1 (or absent) when not an edge hit. Go resolves this
	// row → edge label via its own edge-row table (edgeFromHit); no edge label crosses the
	// bridge.
	EdgeRow int
	// NodeRow is the numeric buffer NODE-ROW index for a node hit (the node InstancedMesh
	// instanceId == its buffer node row). -1 (or absent) when not a node hit. Go resolves
	// this row → node id via its own node-row table (nodeFromHit); no node id crosses the
	// bridge.
	NodeRow int
	// HandholdTerm is the term-id for a handhold hit (+θ=0, +φ=1, -θ=2, -φ=3; see
	// NavGuides.tsx HANDHOLD_TERM_TAG); -1 (or absent) when not a handhold hit. Decoded into
	// (comp, sign) by the gesture FSM's rule-builder (gesture.go).
	HandholdTerm int
	IsInput      bool
	X            float64
	Y            float64
	Z            float64
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity used to resolve an edit's create/delete op
// to the wire owned by that destination port.
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads FRAMED BINARY records from r, dispatching geometry-CRUD "edit"
// messages and play/pause clock-gate control messages. RunStdinReader itself returns
// when ctx is done or r reaches EOF. CAVEAT: its background frame-reader goroutine
// (which blocks in io.ReadFull) has NO ctx-cancel exit path — on ctx-done it keeps
// parked in the read until r reaches EOF or is closed. Benign in production (process
// exit reclaims it), but a caller that wants the goroutine itself to unwind on cancel
// must close r. Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" and resolves create/delete ops to the
// destination port's wire. md may be nil; if non-nil, update (node-move) and
// fade ops mail-sort each entry to the owning node/edge goroutine's inbox.
// tr emits control breadcrumbs for the edit ops.
// clk may be nil; if non-nil, "play" calls clk.Resume() and "pause" calls clk.Halt().
// maxFrameBytes bounds a single framed-binary record: the reader buffer size and the
// upper limit a decoded [len:u32] is allowed to request, so a corrupt/hostile length can't
// drive an unbounded allocation. Matches the 1 MB headroom of the pre-frame line buffer.
const maxFrameBytes = 1 << 20

func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, clk Clock) {
	// Framed-binary reader: each record is [len:u32-LE][record bytes]. A background
	// goroutine reads whole frames (io.ReadFull handles partial reads — a frame split
	// across TCP/pipe chunks is reassembled before the record is decoded) and hands the
	// record bytes to the dispatch loop over a channel. The channel keeps the dispatch
	// ctx-aware exactly as the old line reader did.
	br := bufio.NewReaderSize(r, maxFrameBytes)
	done := ctx.Done()
	recCh := make(chan []byte, 8)
	go func() {
		var lenBuf [4]byte
		for {
			if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					fmt.Fprintf(os.Stderr, "stdin_reader: frame-length read error: %v\n", err)
				}
				close(recCh)
				return
			}
			n := binary.LittleEndian.Uint32(lenBuf[:])
			// Cap the frame size to the same 1 MB headroom the old line buffer had, so a
			// corrupt/hostile length can't drive an unbounded allocation and deafen the bridge.
			if n == 0 || n > maxFrameBytes {
				fmt.Fprintf(os.Stderr, "stdin_reader: bad frame length %d; stopping reader\n", n)
				close(recCh)
				return
			}
			rec := make([]byte, n)
			if _, err := io.ReadFull(br, rec); err != nil {
				if err != io.EOF && err != io.ErrUnexpectedEOF {
					fmt.Fprintf(os.Stderr, "stdin_reader: frame body read error: %v\n", err)
				}
				close(recCh)
				return
			}
			select {
			case recCh <- rec:
			case <-done:
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			return
		case rec, ok := <-recCh:
			if !ok {
				return
			}
			msg, decoded := decodeInputRecord(rec)
			if !decoded {
				continue
			}
			// The authoritative per-type doc is the MSG_TYPES_DOC block in this file's
			// header. check-message-kind-parity.sh holds the two in parity — do not
			// document a type only here, and do not add a case without a doc entry.
			// MSG_TYPES_START
			switch msg.Type {
			case "edit":
				applyEdit(msg, slotReg, md, tr)
			case "play":
				handlePlayMsg(clk)
			case "pause":
				handlePauseMsg(clk)
			case "raw-input":
				handleRawInputMsg(msg, slotReg, md, tr)
			case "save":
				handleSaveMsg(md)
			case "fade-toggle":
				handleFadeToggleMsg(md, tr)
			}
			// MSG_TYPES_END
		}
	}
}

// handlePlayMsg resumes the clock's global gate (bead delivery starts). clk may be nil.
func handlePlayMsg(clk Clock) {
	if clk != nil {
		clk.Resume()
	}
}

// handlePauseMsg halts the clock's global gate (bead delivery freezes). clk may be nil.
func handlePauseMsg(clk Clock) {
	if clk != nil {
		clk.Halt()
	}
}

// handleRawInputMsg hands a raw pointer/wheel event + stateless raycast hit to the
// gesture state machine, which owns gesture bookkeeping and produces camera/topology
// changes. Fire-and-forget — nothing on this seam triggers delivery.
func handleRawInputMsg(msg stdinMsg, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace) {
	if md != nil && msg.Event != nil {
		md.HandleRawInput(*msg.Event, slotReg, tr)
	}
}

// handleSaveMsg persists Go's OWN authoritative scene state (overlay visibility and the
// scene sphere) in response to the bare "save" command. The camera pose is already
// continuously flushed elsewhere (scene_camera_persist.go).
func handleSaveMsg(md *MoveDispatch) {
	if md == nil {
		return
	}
	md.overlaysPersist.schedule(md.ov)
	// Persist the scene sphere immediately (not debounced) so save reliably activates
	// the polar-load path (scene_sphere_persist.go LoadSceneSphere) — until the sphere
	// is in scene.json, reload stays on cartesian x/y/z.
	md.spherePersist.flushNow(md.sceneSphere)
}

// handleFadeToggleMsg toggles fade on the Go-owned current selection. Go owns selection
// + topology, so TS sends no id — MoveDispatch resolves the selected node/edge, flips
// its fade seed, and emits the new faded sets. Fire-and-forget.
func handleFadeToggleMsg(md *MoveDispatch, tr *T.Trace) {
	if md != nil {
		md.ToggleFadeSelection(tr)
	}
}

// overlayToggles (the FLAG name → MoveDispatch flip-method table) is GENERATED into
// overlay_gen.go from OVERLAY_FLAG_NAMES. Parity guarded by check-edit-op-parity.sh via
// the OVERLAY_TOGGLES sentinels in overlay_gen.go.

// applyEdit dispatches one geometry-CRUD edit by its op. There are EXACTLY THREE
// ops: create/update/delete (matched by value so they stay invisible to the
// message-kind-parity guard, which fences only top-level msg.Type kinds).
//
//   - create: un-silence the destination port's wire (edge re-added) — Restore.
//   - delete: silence the wire AND cancel any in-flight bead's clock-delivery,
//     echoing pulse-cancelled (PacedWire.Delete owns both, atomically).
//   - update: set an ATTRIBUTE on a typed entity (msg.Kind). The sole live entity is
//     overlays:
//       overlays + attr "toggle": flip the named flag via overlayToggles.
//     (Camera / node-move / port-anchor / edge-fade edits are now produced in-process by
//     the gesture FSM from raw-input, so they no longer cross this seam.
//     The former attr="set" full-visibility install was removed: its only caller, the
//     load-time main.tsx push, was deleted; no live TS sender remains.)
//
// Unknown ops/kinds/attrs are ignored (forward-compat).

// destPortKey is the slot-registry key for an edit's destination port
// ("target.targetHandle"), matching how slotReg is keyed at load.
func destPortKey(msg stdinMsg) string {
	return msg.Target + "." + msg.TargetHandle
}

// createEdgeInSlot un-silences the wire at the given destination slot — the op=create
// path (an existing edge whose slot was silenced is RESTORED so it carries beads again).
// Returns true when a matching slot existed. Shared by applyEdit's create op AND the
// gesture FSM's wire-completion outcome, so a port→port drag reuses the EXACT existing
// create-edge path rather than any new add-edge machinery. tr may be nil (Breadcrumb
// tolerates a nil receiver).
func createEdgeInSlot(slotReg SlotRegistry, dstNode, dstPort string, tr *T.Trace) bool {
	if dstNode == "" || dstPort == "" {
		return false
	}
	tr.Breadcrumb("edit-create-recv", dstNode, dstPort, "")
	destKey := dstNode + "." + dstPort
	pw, found := slotReg[destKey]
	if !found {
		tr.Breadcrumb("edit-create-notfound", dstNode, dstPort, destKey)
		return false
	}
	tr.Breadcrumb("edit-create-restore", pw.Target, pw.TargetHandle, "")
	pw.Restore()
	return true
}

func applyEdit(msg stdinMsg, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace) {
	// EDIT_OPS_START
	switch msg.Op {
	case "create":
		createEdgeInSlot(slotReg, msg.Target, msg.TargetHandle, tr)
	case "delete":
		if msg.Target == "" || msg.TargetHandle == "" {
			return
		}
		tr.Breadcrumb("edit-delete-recv", msg.Target, msg.TargetHandle, "")
		destKey := destPortKey(msg)
		pw, found := slotReg[destKey]
		if !found {
			tr.Breadcrumb("edit-delete-notfound", msg.Target, msg.TargetHandle, destKey)
			return
		}
		// "delete" breadcrumb emitted here (PacedWire.Delete has no Trace reference)
		// carrying the wire's authoritative slot identity and the dest key. Delete cancels
		// any in-flight bead's clock-delivery and echoes pulse-cancelled atomically.
		tr.Breadcrumb("delete", pw.Target, pw.TargetHandle, destKey)
		pw.Delete()
	case "update":
		applyUpdate(msg, md, tr)
	}
	// EDIT_OPS_END
}

// applyUpdate routes an op=="update" edit to the entity named by msg.Kind, setting the
// requested attribute. The sole live entity is overlays (toggle one flag).
// Unknown kinds/attrs are ignored (forward-compat).
func applyUpdate(msg stdinMsg, md *MoveDispatch, tr *T.Trace) {
	// EDIT_UPDATE_KINDS_START
	switch msg.Kind {
	case "overlays":
		if md == nil {
			return
		}
		switch msg.Attr {
		case "toggle":
			// Flip the named flag — Go owns the state; TS just signals the flip.
			if fn, ok := overlayToggles[msg.Flag]; ok {
				fn(md, tr)
			}
		}
		// Persist ON CHANGE (mirrors fade/camera): schedule a debounced write of the new
		// overlay snapshot so toggles survive a reload without an explicit save. No-op until
		// EnableEditPersist arms the writer (nil-receiver / empty-treeRoot guard in schedule).
		md.overlaysPersist.schedule(md.ov)
	}
	// EDIT_UPDATE_KINDS_END
}
