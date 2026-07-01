// stdin_reader.go — reads JSON-line messages from stdin and dispatches them.
//
// The editor→Go bridge carries two top-level message kinds:
//
//  1. Geometry-CRUD edits (type=="edit") — EXACTLY THREE ops: create/update/delete.
//     create/delete add or remove an edge by destination slot. update sets an
//     ATTRIBUTE on a typed entity (kind ∈ node/edge/camera/overlays/scene); the
//     attribute being set is the only thing that varies — there is no per-feature op.
//       {"type":"edit","op":"create","target":"<node-id>","targetHandle":"<port>"}
//       {"type":"edit","op":"delete","target":"<node-id>","targetHandle":"<port>"}
//       {"type":"edit","op":"update","kind":"node","attr":"move","entries":{...}}
//       {"type":"edit","op":"update","kind":"node","attr":"anchor","node":...,"port":...,"isInput":...,"anchor":{x,y,z},"keys":[...]}
//       {"type":"edit","op":"update","kind":"edge","attr":"faded","edges":{"<edge-id>":true|false,...}}
//       {"type":"edit","op":"update","kind":"camera","viewpoint":{...}}
//       {"type":"edit","op":"update","kind":"overlays","attr":"toggle","flag":"<overlayFlag>"}
//       {"type":"edit","op":"update","kind":"overlays","attr":"set","state":{...}}
//       {"type":"edit","op":"update","kind":"scene","scene":{...}}
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
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

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

// stdinCRUDPayload holds the fields for create/delete/update/fade ops.
type stdinCRUDPayload struct {
	Target       string               `json:"target"`
	TargetHandle string               `json:"targetHandle"`
	Edges        map[string]bool      `json:"edges"`
	Entries      map[string]moveEntry `json:"entries"`
}

// stdinAnchorPayload holds the fields for the port-anchor op.
// Node/Port name the port; IsInput selects the input vs output list; Anchor is the
// new direction offset. Keys lists the routing keys the reader mail-sorts to.
type stdinAnchorPayload struct {
	Node    string     `json:"node"`
	Port    string     `json:"port"`
	IsInput bool       `json:"isInput"`
	Anchor  *anchorVec `json:"anchor"`
	Keys    []string   `json:"keys"`
}

// stdinGuideVisPayload holds the explicit-visibility fields for the overlays attr="set"
// op. The json tags are the overlay FLAG vocabulary, shared with the TS OverlayState
// (derived from OverlayFlag) — parity guarded by check-edit-op-parity.sh via the
// GUIDEVIS sentinels below (a flag added/removed on either side fails the guard).
// GUIDEVIS_FIELDS_START
type stdinGuideVisPayload struct {
	Tori           bool `json:"tori"`
	ScenePoles     bool `json:"scenePoles"`
	NodePoles      bool `json:"nodePoles"`
	AngleLabels    bool `json:"angleLabels"`
	SelSpherePoles bool `json:"selSpherePoles"`
	Handholds      bool `json:"handholds"`
	DoubleLinks    bool `json:"doubleLinks"`
	LabelsGlobal   bool `json:"labelsGlobal"`
	BadgesGlobal   bool `json:"badgesGlobal"`
	Overlays       bool `json:"overlays"`
}

// GUIDEVIS_FIELDS_END

// stdinMsg is the single editor→Go bridge shape. type is always "edit"; op is
// one of exactly three values (create/update/delete). For op=="update", Kind
// names the typed entity (node/edge/camera/overlays/scene) and Attr (where
// present) names the attribute being set. The remaining fields are the union of
// every op/kind/attr payload (only the fields for the active shape are populated).
//
// For kind=="node" attr=="move" (node-move), Entries maps each routing key (the
// moved node id AND each incident edge id) to the moved node's new position. The
// reader mail-sorts each entry to channels[key]; the owning node/edge goroutine
// recomputes.
//
// Anonymous embedding preserves flat JSON field names so the wire format is unchanged.
type stdinMsg struct {
	Type  string          `json:"type"`
	Op    string          `json:"op"`
	Kind  string          `json:"kind"`
	Attr  string          `json:"attr"`
	Flag  string          `json:"flag"`
	Scene json.RawMessage `json:"scene"`
	// Viewpoint is the payload for kind=="camera"; nil otherwise.
	Viewpoint *viewpointMsg `json:"viewpoint,omitempty"`
	// State is the explicit-visibility payload for kind=="overlays" attr=="set"; nil otherwise.
	State *stdinGuideVisPayload `json:"state,omitempty"`
	stdinCRUDPayload
	stdinAnchorPayload
}

// anchorVec mirrors the Port.anchor {x,y,z} shape in the port-anchor edit message.
type anchorVec struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// viewpointMsg carries the payload for an op=="update" kind=="camera" edit
// message. Kind discriminates the sub-operation:
//   - "set":   install a known camera state (all fields populated); emits a camera event.
//   - "orbit": apply from→to great-circle rotation (FromTheta/FromPhi, ToTheta/ToPhi).
//   - "zoom":  scale orbit radius by Factor.
//   - "pan":   slide pivot by world delta (Dx/Dy/Dz).
type viewpointMsg struct {
	Kind string `json:"kind"`
	// orbit
	FromTheta float64 `json:"fromTheta,omitempty"`
	FromPhi   float64 `json:"fromPhi,omitempty"`
	ToTheta   float64 `json:"toTheta,omitempty"`
	ToPhi     float64 `json:"toPhi,omitempty"`
	// zoom
	Factor float64 `json:"factor,omitempty"`
	// pan
	Dx float64 `json:"dx,omitempty"`
	Dy float64 `json:"dy,omitempty"`
	Dz float64 `json:"dz,omitempty"`
	// set
	PivotX   float64 `json:"pivotX,omitempty"`
	PivotY   float64 `json:"pivotY,omitempty"`
	PivotZ   float64 `json:"pivotZ,omitempty"`
	R        float64 `json:"r,omitempty"`
	PosTheta float64 `json:"posTheta,omitempty"`
	PosPhi   float64 `json:"posPhi,omitempty"`
	UpTheta  float64 `json:"upTheta,omitempty"`
	UpPhi    float64 `json:"upPhi,omitempty"`
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
func RunStdinReader(ctx context.Context, r io.Reader, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, clk Clock, treeRoot string) {
	sc := bufio.NewScanner(r)
	done := ctx.Done()
	lineCh := make(chan string, 8)
	go func() {
		for sc.Scan() {
			lineCh <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			// Scan encountered an error; write to stderr (stdout is the Go→TS trace stream).
			// The channel close below will unblock the main select loop.
			fmt.Fprintf(os.Stderr, "stdin_reader: scan error: %v\n", err)
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
			//   "play"   — resume the clock's global gate (bead delivery starts).
			//   "pause"  — halt the clock's global gate (bead delivery freezes).
			//   "resend" — re-emit full current node + edge geometry (remount recovery).
			switch msg.Type {
			case "edit":
				applyEdit(msg, slotReg, md, tr, treeRoot)
			case "play":
				if clk != nil {
					clk.Resume()
				}
			case "pause":
				if clk != nil {
					clk.Halt()
				}
			case "resend":
				if md != nil {
					md.ResendGeometry(tr)
				}
			}
		}
	}
}

// overlayToggles maps an overlay FLAG name (the named boolean attribute set by an
// op=="update" kind=="overlays" attr=="toggle" edit) to the MoveDispatch method
// that flips it. The flag names are the wire vocabulary shared with the TS
// OverlayFlag union in messages.ts (guarded by check-edit-op-parity.sh).
//
// OVERLAY_TOGGLES_START
var overlayToggles = map[string]func(*MoveDispatch, *T.Trace){
	"tori":           (*MoveDispatch).ToggleSceneTori,
	"scenePoles":     (*MoveDispatch).ToggleScenePoles,
	"nodePoles":      (*MoveDispatch).ToggleNodePoles,
	"angleLabels":    (*MoveDispatch).ToggleAngleLabels,
	"selSpherePoles": (*MoveDispatch).ToggleSelSpherePoles,
	"handholds":      (*MoveDispatch).ToggleHandholds,
	"labelsGlobal":   (*MoveDispatch).ToggleLabelsGlobal,
	"badgesGlobal":   (*MoveDispatch).ToggleBadgesGlobal,
	"overlays":       (*MoveDispatch).ToggleOverlaysVis,
	"doubleLinks":    (*MoveDispatch).ToggleDoubleLinks,
}

// OVERLAY_TOGGLES_END

// applyEdit dispatches one geometry-CRUD edit by its op. There are EXACTLY THREE
// ops: create/update/delete (matched by value so they stay invisible to the
// message-kind-parity guard, which fences only top-level msg.Type kinds).
//
//   - create: un-silence the destination port's wire (edge re-added) — Restore.
//   - delete: silence the wire AND cancel any in-flight bead's clock-delivery,
//     echoing pulse-cancelled (PacedWire.Delete owns both, atomically).
//   - update: set an ATTRIBUTE on a typed entity (msg.Kind). Routing per kind:
//     node    + attr "move":   mail-sort node-move entries to owning node/edge inboxes.
//     node    + attr "anchor": snap+mail-sort a ring-anchor update.
//     edge    + attr "faded":  mail-sort each (edgeId,faded) to the owning edgeMover.
//     camera:                  apply the viewpoint payload (set/orbit/zoom/pan).
//     overlays+ attr "toggle": flip the named flag via overlayToggles.
//     overlays+ attr "set":    set all overlay visibilities to explicit values.
//     scene:                   persist the scene blob.
//
// Unknown ops/kinds/attrs are ignored (forward-compat).
func applyEdit(msg stdinMsg, slotReg SlotRegistry, md *MoveDispatch, tr *T.Trace, treeRoot string) {
	// EDIT_OPS_START
	switch msg.Op {
	case "create":
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
	case "delete":
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
	case "update":
		applyUpdate(msg, md, tr, treeRoot)
	}
	// EDIT_OPS_END
}

// applyUpdate routes an op=="update" edit to the entity named by msg.Kind, setting
// the requested attribute. See applyEdit's doc for the kind/attr matrix.
func applyUpdate(msg stdinMsg, md *MoveDispatch, tr *T.Trace, treeRoot string) {
	// EDIT_UPDATE_KINDS_START
	switch msg.Kind {
	case "node":
		switch msg.Attr {
		case "move":
			if md == nil || len(msg.Entries) == 0 {
				return
			}
			// POLAR layout (docs/planning/visual-editor/polar-coordinate-model.md): a
			// node-drag updates only the dragged node's OUTER POLAR ROOT (soft membership —
			// no other node moves). The incoming entries all carry the same moved node id +
			// world target (one per incident edge + the node itself), so RootMove runs once
			// per unique node id. Persistence writes each node's world position recovered
			// from its root (the on-disk prism-Cartesian frame, §8a).
			seen := map[string]bool{}
			for _, e := range msg.Entries {
				if math.IsNaN(e.X) || math.IsNaN(e.Y) || math.IsNaN(e.Z) || seen[e.NodeId] {
					continue
				}
				seen[e.NodeId] = true
				md.RootMove(e.NodeId, vec3{X: e.X, Y: e.Y, Z: e.Z})
				if treeRoot != "" {
					for id, w := range md.heldCenters() {
						// meta.json is canonical for Go node geometry; view/nodes is the
						// auxiliary position store the TS spec-emit reads. Write both so the
						// two on-disk stores stay in sync across a drag (they diverged when
						// only meta.json was written).
						_ = writeMetaPos(treeRoot, id, w.X, w.Y, w.Z)
						_ = writeViewNode(treeRoot, id, specPosition{X: w.X, Y: w.Y, Z: w.Z})
					}
				}
			}
		case "anchor":
			// Mail-sort a snapped ring-anchor update to the owning node + each incident edge
			// inbox. TS sends a world-space direction (anchor {x,y,z}) from node center to the
			// pointer; Go snaps it to the nearest ring-anchor index and forwards AnchorId to
			// each mover. Each owning goroutine sets AnchorId (clears free Anchor), re-emits/
			// recomputes (node re-streams node-geometry; edges recompute segment/arc). No
			// central recompute. Unknown keys are ignored (forward-compat).
			if md == nil || msg.Node == "" || msg.Port == "" || msg.Anchor == nil || len(msg.Keys) == 0 {
				return
			}
			tr.Breadcrumb("edit-port-anchor-recv", msg.Node, msg.Port, "")
			dir := vec3{X: msg.Anchor.X, Y: msg.Anchor.Y, Z: msg.Anchor.Z}
			kind := md.NodeKind(msg.Node)
			anchorId := snapToRingAnchorIndex(kind, dir)
			for _, key := range msg.Keys {
				if ch, ok := md.dispatch[key]; ok {
					ch <- moveMsg{
						Kind:     moveMsgKindAnchor,
						NodeID:   msg.Node,
						Port:     msg.Port,
						IsInput:  msg.IsInput,
						AnchorId: anchorId,
					}
				}
			}
			if treeRoot != "" {
				side := "inputs"
				if !msg.IsInput {
					side = "outputs"
				}
				portPath := filepath.Join(treeRoot, "nodes", msg.Node, side, msg.Port+".json")
				var p specPort
				if raw, err := os.ReadFile(portPath); err == nil {
					_ = json.Unmarshal(raw, &p)
				}
				p.Name = msg.Port
				p.AnchorId = &anchorId
				_ = writePort(treeRoot, msg.Node, msg.Port, msg.IsInput, p)
			}
		}
	case "edge":
		// attr-guarded, symmetric with the "node" case above: switch on msg.Attr and
		// ignore an unknown attr (forward-compat). Currently "faded" is the only edge attr.
		switch msg.Attr {
		case "faded":
			// Mail-sort each (edgeId, faded) entry to its edge's inbox. Each edgeMover sets
			// its OWN wire's faded flag — no central fan-out. Unknown keys are ignored.
			if md == nil || len(msg.Edges) == 0 {
				return
			}
			for edgeID, faded := range msg.Edges {
				if ch, ok := md.dispatch[edgeID]; ok {
					ch <- moveMsg{Kind: moveMsgKindFade, Faded: faded}
				}
			}
			if treeRoot != "" {
				_ = mergeFades(treeRoot, msg.Edges)
			}
		}
	case "camera":
		// Update the polar camera viewpoint and emit a camera trace event. Fire-and-forget.
		if md == nil || msg.Viewpoint == nil {
			return
		}
		vp := msg.Viewpoint
		// VP_KINDS_START
		switch vp.Kind {
		case "set":
			md.SetViewpoint(
				vec3{X: vp.PivotX, Y: vp.PivotY, Z: vp.PivotZ},
				vp.R,
				dir{Theta: vp.PosTheta, Phi: vp.PosPhi},
				dir{Theta: vp.UpTheta, Phi: vp.UpPhi},
			)
			md.EmitViewpoint(tr)
		case "orbit":
			md.OrbitViewpoint(
				dir{Theta: vp.FromTheta, Phi: vp.FromPhi},
				dir{Theta: vp.ToTheta, Phi: vp.ToPhi},
				tr,
			)
		case "orbit-locked":
			md.OrbitLockedViewpoint(
				dir{Theta: vp.FromTheta, Phi: vp.FromPhi},
				dir{Theta: vp.ToTheta, Phi: vp.ToPhi},
				tr,
			)
		case "zoom":
			md.ZoomViewpoint(vp.Factor, tr)
		case "pan":
			md.PanViewpoint(vec3{X: vp.Dx, Y: vp.Dy, Z: vp.Dz}, tr)
		}
		// VP_KINDS_END
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
		case "set":
			// Set all overlay visibilities to explicit values. Sent by TS on window reload
			// so Go's authoritative state matches persisted scene settings.
			if msg.State == nil {
				return
			}
			s := msg.State
			// Map the wire payload fields onto the named overlayVisibility struct (no
			// positional bool order to get wrong); SetGuideVisibility installs it wholesale.
			md.SetGuideVisibility(overlayVisibility{
				sceneToriVisible:      s.Tori,
				scenePolesVisible:     s.ScenePoles,
				nodePolesVisible:      s.NodePoles,
				angleLabelsVisible:    s.AngleLabels,
				selSpherePolesVisible: s.SelSpherePoles,
				handholdsVisible:      s.Handholds,
				doubleLinksVisible:    s.DoubleLinks,
				labelsGlobalVisible:   s.LabelsGlobal,
				badgesGlobalVisible:   s.BadgesGlobal,
				overlaysVisible:       s.Overlays,
			}, tr)
		}
	case "scene":
		if treeRoot != "" && len(msg.Scene) > 0 {
			_ = writeScene(treeRoot, msg.Scene)
		}
	}
	// EDIT_UPDATE_KINDS_END
}
