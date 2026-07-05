// stdin_reader.go — reads FRAMED BINARY records from stdin and dispatches them.
//
// The editor→Go bridge is a purely BINARY buffer (symmetric with the Go→TS content
// buffer on fd 3): each message is a binary RECORD written FRAMED as [len:u32-LE][record]
// to stdin. input_codec.go decodes a record into the stdinMsg below; the dispatch switch
// and every handler (applyEdit / HandleRawInput / play-pause / resend) are UNCHANGED —
// only the wire decode moved from newline-JSON to framed binary.
//
// The editor→Go bridge carries these top-level message kinds (all fully binary; no JSON
// on the wire — see input_codec.go):
//
//  1. Geometry-CRUD edits (type=="edit") — EXACTLY THREE ops: create/update/delete.
//     create/delete add or remove an edge by destination slot. update sets an ATTRIBUTE on
//     a typed entity; the sole live entity is overlays:
//       create (record 20): un-silence the destination slot's wire.
//       delete (record 21): silence it + cancel any in-flight bead.
//       update overlays attr=toggle: flip one named overlay flag.
//     (Camera / node-move / port-anchor / edge-fade edits are produced in-process by the
//     gesture FSM from raw-input, so they no longer cross this seam.)
//
//  2. Play/pause control (type=="play" / type=="pause") — routes directly to the
//     clock's global gate (Halt/Resume). The process starts halted; the first
//     "play" message resumes bead delivery. "pause" re-halts.
//
//  3. Geometry resend (type=="resend") — re-emits the full current node + edge
//     geometry from the held authoritative state (MoveDispatch.ResendGeometry).
//     A freshly-(re)mounted webview that lost its module-level edge-geometry store
//     requests this to rebuild it without restarting Go. Safe to repeat / while running.
//
//  4. Save (type=="save") — Go persists its OWN authoritative scene state (overlay
//     visibility → scene.json, preserving the Go-owned cameraPolar). Bare command, no
//     payload; the editor holds no authoritative scene document.
//
//  5. Raw input (type=="raw-input") — a raw pointer/wheel event + stateless raycast hit,
//     handed to the gesture FSM.
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
	Target       string `json:"target"`
	TargetHandle string `json:"targetHandle"`
}

// stdinMsg is the single editor→Go bridge shape. For type=="edit", op is one of exactly
// three values (create/update/delete). create/delete name a destination slot (Target/
// TargetHandle). op=="update" sets an attribute on a typed entity — the sole live entity is
// overlays: Attr=="toggle" (Flag names one overlay). The other top-level types are
// raw-input (Event), the bare save command, and play/pause/resend.
type stdinMsg struct {
	Type string `json:"type"`
	Op   string `json:"op"`
	Kind string `json:"kind"`
	Attr string `json:"attr"`
	Flag string `json:"flag"`
	// Index carries the md.polarEqs index for op=="update" kind=="lock" (attr active/selected;
	// locks.go ToggleLockActive/SelectLock). Unused by every other message shape.
	Index int `json:"index,omitempty"`
	// Event is the payload for the top-level type=="raw-input" message; nil otherwise.
	Event *rawInputMsg `json:"event,omitempty"`
	stdinCRUDPayload
	// authorPreviewPayload carries op=="update" kind=="lock" attr=="author"/"preview" fields —
	// the keyboard-authoring channel (gesture.go's Author*/SetHover*ByRow). Already-resolved
	// tokens only (see input_codec.go decodeInputRecord); no free-text parsing crosses here.
	authorPreviewPayload
}

// authorPreviewPayload holds the keyboard-authoring channel's payload fields. Action names
// (for attr=="author") the atomic builder step: "begin"|"center"|"term"|"port"|"torus".
// EqKind (action=="begin") is the eqKind to build. NodeRow is the buffer NODE-ROW index
// (resolved via md.nodeRows exactly like a raycast hit's NodeRow — see nodeFromHit).
// Comp/Sign (action=="term") are the polarComp index and +1/-1 sign. PortName/IsInput
// (action=="port", or attr=="preview" for a port preview) name the port on NodeRow.
type authorPreviewPayload struct {
	Action   string  `json:"action,omitempty"`
	EqKind   int     `json:"eqKind,omitempty"`
	NodeRow  int     `json:"nodeRow,omitempty"`
	Comp     int     `json:"comp,omitempty"`
	Sign     float64 `json:"sign,omitempty"`
	PortName string  `json:"portName,omitempty"`
	IsInput  bool    `json:"isInput,omitempty"`
}

// rawInputMsg carries the payload for a top-level type=="raw-input" message (Phase 6):
// a single RAW pointer/wheel event plus the stateless three.js raycast hit. Go's gesture
// state machine (gesture.go) decides what it means — TS does not interpret it. Mirrors the
// TS RawInputEvent (messages.ts). Field names match the JSON wire format exactly.
type rawInputMsg struct {
	Kind       string  `json:"kind"` // pointerdown | pointermove | pointerup | wheel | home
	X          float64 `json:"x"`    // client pixel X
	Y          float64 `json:"y"`    // client pixel Y
	RectLeft   float64 `json:"rectLeft"`
	RectTop    float64 `json:"rectTop"`
	RectWidth  float64 `json:"rectWidth"`
	RectHeight float64 `json:"rectHeight"`
	Button     int     `json:"button"` // 0 primary, 2 secondary; -1 for move/wheel
	Ctrl       bool    `json:"ctrl"`
	Shift      bool    `json:"shift"`
	Alt        bool    `json:"alt"`
	Meta       bool    `json:"meta"`
	DeltaX     float64 `json:"deltaX"`
	DeltaY     float64 `json:"deltaY"`
	Fov        float64 `json:"fov"`
	Hit        rawHit  `json:"hit"`
}

// rawHit is the classified raycast hit: which rendered entity is under the pointer and its
// world point. Kind ∈ port|handhold|node|empty; Id is the entity id (node id, or
// "nodeId:in|out:portName" for a port). Topology facts (e.g. connected?) are NOT carried —
// Go's FSM decides those from its own held state.
type rawHit struct {
	Kind string `json:"kind"`
	Id   string `json:"id"`
	// PortRow is the numeric buffer PORT-ROW index for a new-system port hit (the port
	// InstancedMesh instanceId == its buffer port row). -1 (or absent) on the old path, whose
	// port identity rides the Id string ("nodeId:in|out:portName") instead. Go resolves this
	// row → (node, port) via its own port-row table (portFromHit); no port name crosses the
	// bridge.
	PortRow int `json:"portRow"`
	// EdgeRow is the numeric buffer EDGE-ROW index for a new-system edge hit (the edge's
	// pick-halo carries its buffer edge row). -1 (or absent) when not an edge hit. Go
	// resolves this row → edge label via its own edge-row table (edgeFromHit); no edge
	// label crosses the bridge.
	EdgeRow int `json:"edgeRow"`
	// NodeRow is the numeric buffer NODE-ROW index for a new-system node hit (the node
	// InstancedMesh instanceId == its buffer node row). -1 (or absent) on the old path /
	// unit tests, which carry the node id in the Id string instead. Go resolves this row →
	// node id via its own node-row table (nodeFromHit); no node id crosses the bridge on the
	// new-system path.
	NodeRow int `json:"nodeRow"`
	// HandholdTerm is the term-id for a handhold hit (+θ=0, +φ=1, -θ=2, -φ=3; see
	// NavGuides.tsx HANDHOLD_TERM_TAG); -1 (or absent) when not a handhold hit. Decoded into
	// (comp, sign) by the gesture FSM's rule-builder (gesture.go).
	HandholdTerm int     `json:"handholdTerm"`
	IsInput      bool    `json:"isInput"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Z            float64 `json:"z"`
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity used to resolve an edit's create/delete op
// to the wire owned by that destination port.
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads FRAMED BINARY records from r, dispatching geometry-CRUD "edit"
// messages and play/pause clock-gate control messages. Returns when ctx is done
// or r reaches EOF. Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" and resolves create/delete ops to the
// destination port's wire. md may be nil; if non-nil, update (node-move) and
// fade ops mail-sort each entry to the owning node/edge goroutine's inbox.
// tr emits control breadcrumbs for the edit ops.
// clk may be nil; if non-nil, "play" calls clk.Resume() and "pause" calls clk.Halt().
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, clk Clock, treeRoot string) {
	// Framed-binary reader: each record is [len:u32-LE][record bytes]. A background
	// goroutine reads whole frames (io.ReadFull handles partial reads — a frame split
	// across TCP/pipe chunks is reassembled before the record is decoded) and hands the
	// record bytes to the dispatch loop over a channel. The channel keeps the dispatch
	// ctx-aware exactly as the old line reader did.
	br := bufio.NewReaderSize(r, 1<<20)
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
			if n == 0 || n > (1<<20) {
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
			// Two top-level bridge kinds:
			//   "edit"  — geometry-CRUD; op discriminates the operation (internal axis).
			//   "play"   — resume the clock's global gate (bead delivery starts).
			//   "pause"  — halt the clock's global gate (bead delivery freezes).
			//   "resend" — re-emit full current node + edge geometry (remount recovery).
			switch msg.Type {
			case "edit":
				applyEdit(msg, slotReg, md, tr, treeRoot)
			case "play":
				handlePlayMsg(clk)
			case "pause":
				handlePauseMsg(clk)
			case "resend":
				handleResendMsg(ctx, md, tr)
			case "raw-input":
				handleRawInputMsg(msg, slotReg, md, tr)
			case "save":
				handleSaveMsg(md)
			case "fade-toggle":
				handleFadeToggleMsg(md, tr)
			case "clear-rule":
				handleClearRuleMsg(md, tr)
			case "delete-selected-lock":
				handleDeleteSelectedLockMsg(md, tr)
			}
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

// handleResendMsg re-emits the full current node + edge geometry from the held
// authoritative state, for a freshly-(re)mounted webview recovering its edge-geometry
// store without restarting Go.
func handleResendMsg(ctx context.Context, md *MoveDispatch, tr *T.Trace) {
	if md != nil {
		md.ResendGeometry(ctx, tr)
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

// handleSaveMsg persists Go's OWN authoritative scene state (overlay visibility,
// polar-equation locks, and the scene sphere) in response to the bare "save" command.
// The camera pose is already continuously flushed elsewhere (scene_camera_persist.go).
func handleSaveMsg(md *MoveDispatch) {
	if md == nil {
		return
	}
	md.overlaysPersist.schedule(md.ov)
	md.locksPersist.schedule(md.polarEqsSnap())
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

// handleClearRuleMsg discards the in-progress polar equation (pending term +
// accumulated terms) the rule-builder is authoring. Go owns the state; it resets it and
// re-emits the RuleBuilder block so the panel clears.
func handleClearRuleMsg(md *MoveDispatch, tr *T.Trace) {
	if md != nil {
		md.clearRuleBuilding(tr)
	}
}

// handleDeleteSelectedLockMsg deletes the panel-focused committed polar-equation lock
// (selectedLocks). Go re-guards (only deletes when deactivated).
func handleDeleteSelectedLockMsg(md *MoveDispatch, tr *T.Trace) {
	if md != nil {
		md.DeleteSelectedLock(tr)
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

func applyEdit(msg stdinMsg, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, treeRoot string) {
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
		applyUpdate(msg, md, tr, treeRoot)
	}
	// EDIT_OPS_END
}

// applyUpdate routes an op=="update" edit to the entity named by msg.Kind, setting the
// requested attribute. The sole live entity is overlays (toggle one flag).
// Unknown kinds/attrs are ignored (forward-compat).
func applyUpdate(msg stdinMsg, md *MoveDispatch, tr *T.Trace, treeRoot string) {
	_ = treeRoot // overlay persistence rides the armed overlaysPersist writer, not treeRoot here.
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
				// Turning the rule-builder's overlay off ends the authoring session:
				// clear any half-finished pending term / accumulated ruleTerms.
				if msg.Flag == "selSpherePoles" && !md.ov.selSpherePolesVisible {
					md.clearRuleBuilding(tr)
				}
			}
		}
		// Persist ON CHANGE (mirrors fade/camera): schedule a debounced write of the new
		// overlay snapshot so toggles survive a reload without an explicit save. No-op until
		// EnableEditPersist arms the writer (nil-receiver / empty-treeRoot guard in schedule).
		md.overlaysPersist.schedule(md.ov)
	case "lock":
		if md == nil {
			return
		}
		switch msg.Attr {
		case "active":
			md.ToggleLockActive(msg.Index, tr)
		case "selected":
			md.SelectLock(msg.Index, tr)
		case "author":
			// Keyboard-authoring channel: an already-resolved token drives the SAME builder
			// the click path drives (gesture.go's Author* methods). Fire-and-forget.
			switch msg.Action {
			case "begin":
				md.AuthorBegin(eqKind(msg.EqKind), tr)
			case "node":
				md.AuthorNode(msg.NodeRow, tr)
			case "latch":
				md.AuthorLatchHalfTerm(polarComp(msg.Comp), msg.Sign, tr)
			case "port":
				md.AuthorPort(msg.NodeRow, msg.PortName, msg.IsInput, tr)
			case "torus":
				md.AuthorTorus(msg.NodeRow, tr)
			}
		case "preview":
			// Preview highlight for the keyboard-authoring channel: mirrors updateHover's
			// pointer-hover write so a typed token's target highlights in the streamed buffer.
			if msg.PortName != "" {
				md.SetHoverPortByRow(msg.NodeRow, msg.PortName, msg.IsInput, tr)
			} else {
				md.SetHoverNodeByRow(msg.NodeRow, tr)
			}
		}
	}
	// EDIT_UPDATE_KINDS_END
}
