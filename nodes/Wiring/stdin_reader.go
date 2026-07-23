// stdin_reader.go — reads FRAMED BINARY records from stdin and dispatches them.
//
// The editor→Go bridge is a purely BINARY buffer (symmetric with the Go→TS content
// buffer on fd 3): each message is a binary RECORD written FRAMED as [len:u32-LE][record]
// to stdin. input_codec.go decodes a record into the stdinMsg below; the dispatch switch
// and every handler (applyEdit / HandleRawInput) are UNCHANGED —
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
//  1. "edit" — geometry-CRUD. The sole op is update, which sets an ATTRIBUTE on a
//     typed entity; the live entities are overlays and clock:
//       update overlays attr=toggle: flip one named overlay flag.
//       update clock attr=speed: set the playback multiplier.
//     A create/delete op pair (records 20/21) once added or removed an edge by
//     destination slot; both were removed end-to-end (no live TS sender, and create's
//     only trigger tore down a live wire's beads via PacedWire.Restore) — records 20/21
//     are now GAPS. Camera / node-move / port-anchor are NOT edits: the gesture FSM
//     produces them in-process from raw-input, so they never cross this seam as an edit op.
//
//  2. "save" — Go persists its OWN authoritative scene state (overlay visibility →
//     scene.json, preserving the Go-owned cameraPolar). Bare command, no payload; the
//     editor holds no authoritative scene document.
//
//  3. "raw-input" — a raw pointer/wheel event + stateless raycast hit, handed to the
//     gesture FSM.
//
// A remounted webview that has nothing new to render (Go idle) is served from the
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

// stdinMsg is the single editor→Go bridge shape. For type=="edit", op is the sole
// remaining value "update", which sets an attribute on a typed entity — the sole live
// entity is overlays: Attr=="toggle" (Flag names one overlay). The other top-level types
// are raw-input (Event) and the bare command (save).
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
	// Num is the numeric payload for an op=="update" that carries a value rather than a
	// flag name — currently only clock/speed (the playback multiplier). Zero otherwise.
	Num int
	// Event is the payload for the top-level type=="raw-input" message; nil otherwise.
	Event *rawInputMsg
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

// rawHit is the classified raycast hit: which rendered entity is under the pointer. Kind ∈
// port|handhold|node|edge|torus|empty. Topology facts (e.g. connected?) are NOT carried —
// Go's FSM decides those from its own held state. There is no world point on this record:
// any ray/plane unprojection Go needs is computed Go-side from the raw pointer NDC + Go's
// own camera/surface state (pointerOnRingPlane / rayDirThroughNDC in gesture.go).
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
}

// SlotRegistry maps "targetNodeId.targetHandle" → *PacedWire.
// It is the stable, slot-keyed identity for the wire owned by each destination port,
// consumed by md.Bind to seed edgeMovers (the create/delete edit ops that once indexed
// it were removed end-to-end).
type SlotRegistry map[string]*PacedWire

// RunStdinReader reads FRAMED BINARY records from r, dispatching geometry-CRUD "edit"
// messages and the bare save command. RunStdinReader itself returns
// when ctx is done or r reaches EOF. Its background frame-reader goroutine (which
// blocks in io.ReadFull) unwinds on ctx-done too: if r implements io.Closer,
// RunStdinReader closes it on ctx-done, which unblocks the parked read (returns an
// error) so the reader goroutine exits via its close(recCh); return path. In production
// r is os.Stdin and this only runs as the process is already exiting, so the close is
// harmless. Call in a goroutine alongside the node run loop.
//
// slotReg is keyed by "target.targetHandle" (the destination port's wire); it stays
// live for delivery/movers though no edit op indexes it any longer. md may be nil; if
// non-nil, update (node-move) ops mail-sort each entry to the owning node/edge goroutine's inbox.
// tr emits control breadcrumbs for the edit ops.
// maxFrameBytes bounds a single framed-binary record: the reader buffer size and the
// upper limit a decoded [len:u32] is allowed to request, so a corrupt/hostile length can't
// drive an unbounded allocation. Matches the 1 MB headroom of the pre-frame line buffer.
const maxFrameBytes = 1 << 20

// speedSinks is the build-wide list of every clock-owning goroutine's speed
// channel (LoadTopology's 4th return value, per-goroutine-clock.md
// "Delivery"), collected ONCE at load before any goroutine spawned. This
// RunStdinReader goroutine is the sole writer of every channel in it from here
// on — nothing else sends on them — so broadcasting a speed change by looping
// over the slice and calling SendSpeedNonBlocking needs no lock. nil (or an
// empty slice) is fine: the speed edit then simply reaches nobody, same as
// today's known-inert slider before this delivery path existed.
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, speedSinks []chan float64) {
	// Every persister now writes synchronously the moment its value changes (see
	// scene_persist.go's header comment for why the prior debounce/clean-shutdown-flush
	// machinery was removed), so there is nothing pending to flush on exit here anymore.
	// Framed-binary reader: each record is [len:u32-LE][record bytes]. A background
	// goroutine reads whole frames (io.ReadFull handles partial reads — a frame split
	// across TCP/pipe chunks is reassembled before the record is decoded) and hands the
	// record bytes to the dispatch loop over a channel. The channel keeps the dispatch
	// ctx-aware exactly as the old line reader did.
	br := bufio.NewReaderSize(r, maxFrameBytes)
	done := ctx.Done()
	recCh := make(chan []byte, 8)
	// Unblock the background frame-reader's io.ReadFull on ctx-cancel even when r stays
	// open (no EOF): if r is an io.Closer, close it once ctx is done so the parked read
	// returns an error and the reader goroutine can exit via its close(recCh); return path.
	// In production r is os.Stdin and this runs only as the process is already exiting, so
	// closing it is harmless; the goroutine simply outlives it until then.
	if c, ok := r.(io.Closer); ok {
		go func() {
			<-done
			c.Close()
		}()
	}
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
	// abcDragCh is this goroutine's read end of the abc-drag-count bridge (view_stream.go's
	// doc comment): every abc-drag RECIPIENT's own nodeMover goroutine sends a tick here,
	// non-blocking; THIS single goroutine (the VIEW-stream owner) drains it and increments
	// its own plain abcDragCount. nil (md == nil, or SetViewStream never ran) is fine — a
	// nil channel in the select below never becomes ready, matching the "no dedicated view
	// stream" fallback everywhere else in this file.
	var abcDragCh chan struct{}
	if md != nil {
		abcDragCh = md.abcDragCh
	}
	for {
		select {
		case <-done:
			return
		case <-abcDragCh:
			// One or more abc-drag ticks arrived on this same wake — drain them all in one
			// pass (mirrors nodeMover/edgeMover's own "drain to empty" shape) and re-emit
			// the view frame once per drained tick, so the .probe log's abc-drag COUNT
			// still matches one event per recipient (buffer-log-equivalence.test.ts).
			for n := md.DrainAbcDragChan(); n > 0; n-- {
				md.emitViewFrame([]RowEvent{{Kind: T.KindAbcDrag, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1}})
			}
		case rec, ok := <-recCh:
			if !ok {
				return
			}
			// Drain the latest port/edge/node row tables the Trace-drain goroutine has
			// published since our last iteration BEFORE any hit resolution below (a
			// "raw-input" record's rawHit carries only numeric rows; portFromHit/
			// edgeFromHit/nodeFromHit in gesture.go resolve them against md.portTbl/
			// edgeTbl/nodeTbl) — this is the ownership-handoff replacement for the old
			// atomic.Load-on-demand: same "always resolve against the newest table"
			// guarantee, now via a depth-1 replace-latest channel this goroutine alone
			// drains. Once per dispatch iteration is the right cadence: a single record
			// carries at most one hit to resolve, so draining any more often would be
			// wasted work and draining less often would resolve against a stale table.
			md.drainRowTables()
			// Drain every mover's pending position report (movers → gesture, see
			// posReport/drainPositions doc comments) into md.positions BEFORE hit
			// resolution/heldCenters below, same cadence as drainRowTables above.
			md.drainPositions()
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
				applyEdit(msg, md, tr, speedSinks)
			case "raw-input":
				handleRawInputMsg(msg, slotReg, md, tr)
			case "save":
				handleSaveMsg(md)
			}
			// MSG_TYPES_END
		}
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
	md.persist.overlays.schedule(md.ov)
	// Persist the scene sphere immediately (not debounced) so save reliably activates
	// the polar-load path (scene_sphere_persist.go LoadSceneSphere) — until the sphere
	// is in sphere.json, reload stays on cartesian x/y/z.
	md.persist.sphere.flushNow(md.sceneSphere)
}

// overlayToggles (the FLAG name → MoveDispatch flip-method table) is GENERATED into
// overlay_gen.go from OVERLAY_FLAG_NAMES. Parity guarded by check-edit-op-parity.sh via
// the OVERLAY_TOGGLES sentinels in overlay_gen.go.

// overlayFlagTraceKind maps the wire FLAG name (same keys as overlayToggles) to its
// Trace.Kind* string, so applyUpdate's toggle case can hand emitViewFrame the ONE event
// that flag's toggle logged (matching the fd-3 fallback's per-toggle tr.X(bool) call).
// Hand-authored (overlay_gen.go is GENERATED and does not carry Trace kinds) — kept in
// sync with overlayToggles by check-edit-op-parity.sh's OVERLAY_TOGGLES sentinels, which
// already assert this exact flag-name set.
var overlayFlagTraceKind = map[string]string{
	"tori":           T.KindSceneTori,
	"scenePoles":     T.KindScenePoles,
	"nodePoles":      T.KindNodePoles,
	"selSpherePoles": T.KindSelSpherePoles,
	"handholds":      T.KindHandholds,
	"labelsGlobal":   T.KindLabelsGlobal,
	"overlays":       T.KindOverlaysVis,
	"doubleLinks":    T.KindDoubleLinks,
}

// applyEdit dispatches one geometry-CRUD edit by its op. The sole op is update
// (matched by value so it stays invisible to the message-kind-parity guard, which
// fences only top-level msg.Type kinds).
//
//   - update: set an ATTRIBUTE on a typed entity (msg.Kind). Live entities are
//     overlays (attr "toggle": flip the named flag) and clock (attr "speed").
//     (Camera / node-move / port-anchor edits are produced in-process by the
//     gesture FSM from raw-input, so they never cross this seam.)
//
// The create/delete edge ops were removed end-to-end: no TS sender ever emitted them,
// and the create path's only live trigger — a port-drop gesture — tore down a live
// wire's in-flight beads via PacedWire.Restore. The destination-keyed SlotRegistry
// stays live for delivery/movers (md.Bind), but the reader no longer indexes it here.
//
// Unknown ops/kinds/attrs are ignored (forward-compat).
func applyEdit(msg stdinMsg, md *MoveDispatch, tr *T.Trace, speedSinks []chan float64) {
	// EDIT_OPS_START
	switch msg.Op {
	case "update":
		applyUpdate(msg, md, tr, speedSinks)
	}
	// EDIT_OPS_END
}

// applyUpdate routes an op=="update" edit to the entity named by msg.Kind, setting the
// requested attribute. Live entities: overlays (toggle one flag) and clock (set the
// playback-speed multiplier — Go-owned state, the slider just signals the value).
// Unknown kinds/attrs are ignored (forward-compat).
func applyUpdate(msg stdinMsg, md *MoveDispatch, tr *T.Trace, speedSinks []chan float64) {
	// EDIT_UPDATE_KINDS_START
	switch msg.Kind {
	case "clock":
		switch msg.Attr {
		case "speed":
			// The playback multiplier (0/1/2 from the slider). SetSpeed left the Clock
			// INTERFACE in the per-goroutine-clock demolition (item 4): nothing outside a goroutine's own
			// copy may mutate it anymore, since a copy is owned by exactly one goroutine.
			// Delivery (per-goroutine-clock.md "Delivery"): broadcast the new speed to
			// EVERY clock-owning goroutine's own channel (collected once, at load,
			// before any goroutine spawned — see LoadTopology's speedSinks return
			// value). This RunStdinReader goroutine is the sole writer of any of these
			// channels, so no lock is needed; SendSpeedNonBlocking never blocks on a
			// receiver that is asleep or never reads (latest-wins coalescing).
			for _, ch := range speedSinks {
				SendSpeedNonBlocking(ch, float64(msg.Num))
			}
		}
	case "overlays":
		if md == nil {
			return
		}
		switch msg.Attr {
		case "toggle":
			// Flip the named flag — Go owns the state; TS just signals the flip.
			if fn, ok := overlayToggles[msg.Flag]; ok {
				fn(md, tr)
				// Decentralized (Step C, per-owner-buffer-rows.md): this goroutine (the sole
				// caller of every overlay Toggle*) also writes its own VIEW frame directly,
				// carrying the one flag that just changed — matches the ONE tr.X(bool) event
				// the toggle already logged on the fd-3 fallback path.
				if kind, ok := overlayFlagTraceKind[msg.Flag]; ok {
					md.emitViewFrame([]RowEvent{{Kind: kind, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1}})
				}
			}
		}
		// Persist ON CHANGE (mirrors camera): schedule a debounced write of the new
		// overlay snapshot so toggles survive a reload without an explicit save. No-op until
		// EnableEditPersist arms the writer (nil-receiver / empty-treeRoot guard in schedule).
		md.persist.overlays.schedule(md.ov)
	}
	// EDIT_UPDATE_KINDS_END
}
