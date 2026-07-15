package Wiring

import (
	"math"
	"testing"
)

// gesture_test.go — drive the gesture state machine (gesture.go) with raw pointer/wheel
// sequences and assert the FSM state transitions + camera OUTCOMES (viewpoint pose changes).
// Uses a zero-value MoveDispatch (no node movers → empty heldCenters → deterministic
// region-focus fallback), so the outcomes are hand-computable.

func newGestureMD(v viewpoint) *MoveDispatch {
	md := &MoveDispatch{}
	md.vp.viewpoint = v
	return md
}

// canonical "+Z camera looking at origin, up +Y, r=100" viewpoint.
func canonicalViewpoint() viewpoint {
	return viewpoint{pivot: vec3{0, 0, 0}, r: 100, pos: dir{Theta: math.Pi / 2, Phi: math.Pi / 2}, up: dir{0, 0}}
}

func rawEvent(kind string, x, y float64) rawInputMsg {
	return rawInputMsg{
		Kind: kind, X: x, Y: y,
		RectLeft: 0, RectTop: 0, RectWidth: 800, RectHeight: 600,
		Button: 0, Fov: 50,
		Hit: rawHit{Kind: "empty"},
	}
}

// Empty-space drag orbits the camera about a frozen region-focus pivot: pivot + radius are
// preserved (rigid orbit) while pos changes.
func TestGestureEmptyDragOrbits(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())

	down := rawEvent("pointerdown", 400, 300)
	md.HandleRawInput(down, nil, nil)
	if md.gest.phase != gestPending || !md.gest.emptyDown {
		t.Fatalf("after pointerdown: phase=%v emptyDown=%v", md.gest.phase, md.gest.emptyDown)
	}
	// Orbit pivot is the content ahead (focusAhead). Empty centers → a point on the view axis a
	// fixed distance ahead: eye=(0,0,100), forward=(0,0,-1), focusMin=10 → (0,0,90).
	if !vecClose(md.gest.rotPivot, vec3{0, 0, 90}, 1e-9) {
		t.Fatalf("rotPivot=%v want focus-ahead (0,0,90)", md.gest.rotPivot)
	}

	// First move past the slop: transitions to rotating and seeds the viewpoint. The first
	// frame's arc is ~zero (prev==curr), so pose is essentially the seeded one.
	md.HandleRawInput(rawEvent("pointermove", 420, 300), nil, nil)
	if md.gest.phase != gestRotating {
		t.Fatalf("after slop-cross move: phase=%v want rotating", md.gest.phase)
	}
	if !vecClose(md.vp.pivot, vec3{0, 0, 90}, 1e-9) {
		t.Fatalf("seed pivot=%v want focus-ahead (0,0,90)", md.vp.pivot)
	}
	if math.Abs(md.vp.r-10) > 1e-9 {
		t.Fatalf("seed r=%v want 10 (eye→focus-ahead)", md.vp.r)
	}
	posBefore := md.vp.pos
	rBefore, pivotBefore := md.vp.r, md.vp.pivot

	// Second move: genuine cursor delta → orbit. pos must change; r + pivot preserved.
	md.HandleRawInput(rawEvent("pointermove", 480, 320), nil, nil)
	if math.Abs(md.vp.r-rBefore) > 1e-9 {
		t.Fatalf("orbit changed r: %v != %v", md.vp.r, rBefore)
	}
	if !vecClose(md.vp.pivot, pivotBefore, 1e-9) {
		t.Fatalf("orbit moved pivot: %v != %v", md.vp.pivot, pivotBefore)
	}
	if angularDistance(md.vp.pos, posBefore) < 1e-6 {
		t.Fatalf("orbit did not change pos (dir stayed %v)", md.vp.pos)
	}

	md.HandleRawInput(rawEvent("pointerup", 480, 320), nil, nil)
	if md.gest.phase != gestIdle {
		t.Fatalf("after pointerup: phase=%v want idle", md.gest.phase)
	}
}

// Plain wheel pans the pivot (screen-space slide); ctrl+wheel dollies (pivot translation
// toward the cursor target). Both leave the radius set by the region-focus seed.
func TestGestureWheelStrafesCamera(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	pivotBefore := md.vp.pivot
	centerBefore := md.sceneSphere.Center
	ev := rawEvent("wheel", 400, 300)
	ev.DeltaX = 10
	ev.DeltaY = 0
	md.HandleRawInput(ev, nil, nil)
	// Lateral pan strafes the CAMERA (free-camera model); the fixed scene does not move.
	if vecClose(md.vp.pivot, pivotBefore, 1e-9) {
		t.Fatalf("plain wheel did not strafe the camera (pivot stayed %v)", md.vp.pivot)
	}
	if !vecClose(md.sceneSphere.Center, centerBefore, 1e-9) {
		t.Fatalf("plain wheel moved the scene center %v; the scene must stay fixed", md.sceneSphere.Center)
	}
}

// Plain-wheel PAN must fire regardless of what the raycast hit is under the cursor: the
// gesture FSM's wheel path is hit-independent for a plain (non-ctrl) wheel. This pins that a
// node/edge hit does NOT suppress or divert the pan (the TS-side validator drop of "edge"
// hits was the real bug; this guards the Go contract the fix relies on).
func TestGestureWheelPansOverNodeAndEdgeHit(t *testing.T) {
	for _, h := range []rawHit{
		{Kind: "node", NodeRow: 0},
		{Kind: "edge", EdgeRow: 0},
		{Kind: "port", PortRow: 0},
	} {
		md := newGestureMD(canonicalViewpoint())
		md.SetNodeRowResolver(stubNodeRows{"N7"})
		before := md.vp.pivot
		ev := rawEvent("wheel", 400, 300)
		ev.DeltaX = 10
		ev.DeltaY = 0
		ev.Hit = h
		md.HandleRawInput(ev, nil, nil)
		if vecClose(md.vp.pivot, before, 1e-9) {
			t.Fatalf("plain wheel with %s hit did not strafe the camera (pivot stayed %v)", h.Kind, md.vp.pivot)
		}
	}
}

func TestGestureCtrlWheelZoomsToCursor(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	ev := rawEvent("wheel", 400, 300)
	ev.Ctrl = true
	ev.DeltaY = 1
	md.HandleRawInput(ev, nil, nil)
	// ctrl-wheel dollies the camera along the cursor→node ray KEEPING orientation (node stays
	// under the mouse — no re-aim). Empty centers → target=regionFocus=(0,0,90), eye=(0,0,100),
	// rayDir=(0,0,-1), distP=10. The fractional step (10*(1-1.01)=-0.1) is below the pass-through
	// floor (minStep = vp.r*(zoomBase-1) = 1), so the camera moves minStep AWAY (DeltaY=1) →
	// pivot.Z = +1.
	wantZ := 100 * (gestureZoomBase - 1)
	if math.Abs(md.vp.pivot.Z-wantZ) > 1e-9 || math.Abs(md.vp.pivot.X) > 1e-9 {
		t.Fatalf("ctrl-wheel pivot=%v want Z≈%v (dolly toward cursor)", md.vp.pivot, wantZ)
	}
	// The look direction is unchanged (no re-aim).
	if angularDistance(md.vp.pos, canonicalViewpoint().pos) > 1e-9 {
		t.Fatalf("ctrl-wheel re-aimed the camera (pos changed); zoom-to-cursor must keep orientation")
	}
}

// Edge creation by port→port drag reuses the EXISTING create-edge slot path: dropping an
// unconnected OUT port onto another node's IN port un-silences (Restore) that dest slot's
// wire — the same effect as an op=create edit.
func TestGestureWireCreatesEdgeViaExistingCreatePath(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetPortRowResolver(stubPortRows{
		{Node: "A", Port: "out", IsInput: false},
		{Node: "B", Port: "in", IsInput: true},
	})
	// A silenced (deleted) wire at dest slot B.in — as if the edge had been deleted.
	pw := NewPacedWire(10, 1)
	pw.Target, pw.TargetHandle = "B", "in"
	pw.Delete() // silence it (as if the edge had been deleted)
	slotReg := SlotRegistry{"B.in": pw}

	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "port", PortRow: 0, IsInput: false}
	md.HandleRawInput(down, slotReg, nil)
	if md.gest.wireNode != "A" || md.gest.wirePort != "out" || md.gest.wireInput {
		t.Fatalf("after port down: wireNode=%q wirePort=%q wireInput=%v", md.gest.wireNode, md.gest.wirePort, md.gest.wireInput)
	}
	// Drag past slop → wiring.
	md.HandleRawInput(rawEvent("pointermove", 460, 300), slotReg, nil)
	if md.gest.phase != gestWiring {
		t.Fatalf("phase=%v want wiring", md.gest.phase)
	}
	// Drop on B's IN port → create-edge (Restore) on B.in.
	up := rawEvent("pointerup", 460, 300)
	up.Hit = rawHit{Kind: "port", PortRow: 1, IsInput: true}
	md.HandleRawInput(up, slotReg, nil)
	if pw.deleted {
		t.Fatalf("wire B.in still deleted; create-edge path did not Restore it")
	}
	if md.gest.phase != gestIdle {
		t.Fatalf("after wire up phase=%v want idle", md.gest.phase)
	}
}

// stubNodeRows is a fixed node-row table for the new-system node-hit resolution tests: it
// maps a numeric buffer NODE-ROW index → node id, mirroring Buffer's
// SnapshotState.LookupNodeRow without pulling in the Buffer package.
type stubNodeRows []string

func (t stubNodeRows) LookupNodeRow(row int) (nodeID string, ok bool) {
	if row < 0 || row >= len(t) {
		return "", false
	}
	return t[row], true
}

// stubPortRows is a fixed port-row table for the new-system port-hit resolution tests: it
// maps a numeric buffer PORT-ROW index → (node, port, isInput), mirroring Buffer's
// SnapshotState.LookupPortRow without pulling in the Buffer package.
type stubPortRows []PortRowEntryStub

// PortRowEntryStub mirrors Buffer.PortRowEntry for the resolver stub.
type PortRowEntryStub struct {
	Node    string
	Port    string
	IsInput bool
}

func (t stubPortRows) LookupPortRow(row int) (node, port string, isInput, ok bool) {
	if row < 0 || row >= len(t) {
		return "", "", false, false
	}
	e := t[row]
	return e.Node, e.Port, e.IsInput, true
}

// stubEdgeRows is a fixed edge-row table for the new-system edge-hit resolution test: it
// maps a numeric buffer EDGE-ROW index → edge label, mirroring Buffer's
// SnapshotState.LookupEdgeRow without pulling in the Buffer package.
type stubEdgeRows []string

func (t stubEdgeRows) LookupEdgeRow(row int) (label string, ok bool) {
	if row < 0 || row >= len(t) {
		return "", false
	}
	return t[row], true
}

// Click-select is Go-owned for EDGES too: a click on an edge (new-system: a numeric buffer
// EDGE-ROW hit) resolves the row → edge label via the injected edge-row table and sets
// md.selectedEdge, clearing any node selection (exclusive). Selecting a node afterwards
// clears the edge selection, and an empty click clears both.
func TestGestureClickSelectsEdgeGoOwned(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetEdgeRowResolver(stubEdgeRows{"e0", "e1"})
	md.SetNodeRowResolver(stubNodeRows{"N7"})

	// First select a node so we can prove edge-select clears it.
	nd := rawEvent("pointerdown", 400, 300)
	nd.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(nd, nil, nil)
	nu := rawEvent("pointerup", 400, 300)
	nu.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(nu, nil, nil)
	if md.selected != "N7" {
		t.Fatalf("pre: selected=%q want N7", md.selected)
	}

	// Tap EDGE row 1 → selectedEdge=e1, node selection cleared.
	ed := rawEvent("pointerdown", 400, 300)
	ed.Hit = rawHit{Kind: "edge", EdgeRow: 1}
	md.HandleRawInput(ed, nil, nil)
	eu := rawEvent("pointerup", 400, 300)
	eu.Hit = rawHit{Kind: "edge", EdgeRow: 1}
	md.HandleRawInput(eu, nil, nil)
	if md.selectedEdge != "e1" {
		t.Fatalf("selectedEdge=%q want e1", md.selectedEdge)
	}
	if md.selected != "" {
		t.Fatalf("selected=%q want empty (edge select clears node)", md.selected)
	}

	// Selecting a node clears the edge selection (exclusive both ways).
	nd2 := rawEvent("pointerdown", 400, 300)
	nd2.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(nd2, nil, nil)
	nu2 := rawEvent("pointerup", 400, 300)
	nu2.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(nu2, nil, nil)
	if md.selectedEdge != "" {
		t.Fatalf("selectedEdge=%q want empty after node select", md.selectedEdge)
	}

	// Empty-space click clears the current selection (highlight is transient).
	md.HandleRawInput(rawEvent("pointerdown", 400, 300), nil, nil)
	md.HandleRawInput(rawEvent("pointerup", 400, 300), nil, nil)
	if md.selected != "" || md.selectedEdge != "" {
		t.Fatalf("after empty click: selected=%q selectedEdge=%q want empty,empty (cleared)", md.selected, md.selectedEdge)
	}
}

// New-system wiring: a port hit carries ONLY a numeric buffer PORT-ROW index (no port-name
// string). The gesture FSM resolves each row → (node, port) via its injected port-row table,
// then drives the SAME create-edge slot path. Drives a full port-row→port-row drag and
// asserts the dest wire is un-silenced (edge created) — end-to-end with NO strings.
func TestGestureWireFromPortRows(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	// Port-row table: row 0 = A.out (output), row 1 = B.in (input).
	md.SetPortRowResolver(stubPortRows{
		{Node: "A", Port: "out", IsInput: false},
		{Node: "B", Port: "in", IsInput: true},
	})

	pw := NewPacedWire(10, 1)
	pw.Target, pw.TargetHandle = "B", "in"
	pw.Delete()
	slotReg := SlotRegistry{"B.in": pw}

	// Grab A's OUT port by ROW INDEX 0 (Id empty — the string sidecar does not exist).
	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "port", PortRow: 0}
	md.HandleRawInput(down, slotReg, nil)
	if md.gest.wireNode != "A" || md.gest.wirePort != "out" || md.gest.wireInput {
		t.Fatalf("port-row down: wireNode=%q wirePort=%q wireInput=%v", md.gest.wireNode, md.gest.wirePort, md.gest.wireInput)
	}
	md.HandleRawInput(rawEvent("pointermove", 460, 300), slotReg, nil)
	if md.gest.phase != gestWiring {
		t.Fatalf("phase=%v want wiring", md.gest.phase)
	}
	// Drop on B's IN port by ROW INDEX 1.
	up := rawEvent("pointerup", 460, 300)
	up.Hit = rawHit{Kind: "port", PortRow: 1}
	md.HandleRawInput(up, slotReg, nil)
	if pw.deleted {
		t.Fatalf("wire B.in still deleted; port-row create-edge path did not Restore it")
	}
	if md.gest.phase != gestIdle {
		t.Fatalf("after wire up phase=%v want idle", md.gest.phase)
	}
}

// Click-select is Go-owned: a click on a node sets md.selected to that node id; a click on
// empty space clears it. (No camera change — covered by TestGestureClickNoCameraChange.)
func TestGestureClickSelectsNodeGoOwned(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(stubNodeRows{"N7"})

	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(down, nil, nil)
	md.HandleRawInput(func() rawInputMsg {
		e := rawEvent("pointerup", 401, 300)
		e.Hit = rawHit{Kind: "node", NodeRow: 0}
		return e
	}(), nil, nil)
	if md.selected != "N7" {
		t.Fatalf("selected=%q want N7", md.selected)
	}

	// Empty-space click CLEARS the highlight (md.selected), even though the rule-builder's
	// sticky panel Center (md.ruleCenter) stays put — see TestGestureRuleCenterStickyOnEmptyClick.
	d2 := rawEvent("pointerdown", 400, 300) // Hit defaults to empty
	md.HandleRawInput(d2, nil, nil)
	md.HandleRawInput(rawEvent("pointerup", 401, 300), nil, nil)
	if md.selected != "" {
		t.Fatalf("selected=%q want empty (cleared) after empty-space click", md.selected)
	}
}

// Hover is Go-owned: a pointer-move over a node's TORUS ring records it as the hovered node
// (the concentric hover ring emphasizes the ring handle, so it lights only on a torus hit, not
// a body hit); a move over a port records the hovered port (clearing the node hover); a move
// over empty space — or over the node BODY — clears hover. The FSM dedupes on the
// (node,port,isInput) triple so a still/same-target move does not re-emit. Drives moves and
// asserts md.hoverNode/hoverPort track the hit.
func TestGestureHoverTracksNodeAndPort(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetPortRowResolver(stubPortRows{{Node: "A", Port: "in", IsInput: true}})
	md.SetNodeRowResolver(stubNodeRows{"N7"})

	// Move over node N7's torus ring → hovered node.
	mv := rawEvent("pointermove", 400, 300)
	mv.Hit = rawHit{Kind: "torus", NodeRow: 0}
	md.HandleRawInput(mv, nil, nil)
	if md.hoverNode != "N7" || md.hoverPort != "" {
		t.Fatalf("torus hover: hoverNode=%q hoverPort=%q want N7,''", md.hoverNode, md.hoverPort)
	}

	// Move over the node BODY (kind "node") → hover clears (body does not light the ring).
	bodyMv := rawEvent("pointermove", 402, 300)
	bodyMv.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(bodyMv, nil, nil)
	if md.hoverNode != "" || md.hoverPort != "" {
		t.Fatalf("body hover: hoverNode=%q hoverPort=%q want '',''", md.hoverNode, md.hoverPort)
	}

	// Move onto a port (row 0 = A.in) → hovered port, node hover cleared.
	pv := rawEvent("pointermove", 410, 300)
	pv.Hit = rawHit{Kind: "port", PortRow: 0}
	md.HandleRawInput(pv, nil, nil)
	if md.hoverPort != "in" || md.hoverNode != "A" || !md.hoverInput {
		t.Fatalf("port hover: hoverNode=%q hoverPort=%q input=%v want A,in,true", md.hoverNode, md.hoverPort, md.hoverInput)
	}

	// Move over empty space → hover cleared.
	md.HandleRawInput(rawEvent("pointermove", 500, 300), nil, nil)
	if md.hoverNode != "" || md.hoverPort != "" {
		t.Fatalf("empty hover: hoverNode=%q hoverPort=%q want '',''", md.hoverNode, md.hoverPort)
	}
}

// A SECONDARY (two-finger trackpad tap, button 2) select is a tap-select that must survive
// finger drift PAST the move slop: two fingers don't land precisely, so the down→up path
// jitters more than the slop. It must stay gestPending (never convert to drag/rotate) and
// still resolve to a node select on pointer-up. Empty-space two-finger tap preserves selection.
func TestGestureSecondaryTapSelectsThroughDrift(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.SetNodeRowResolver(stubNodeRows{"N7"})

	// Two-finger tap ON a node, with drift well past gestureMoveSlopPx between down and up.
	down := rawEvent("pointerdown", 400, 300)
	down.Button = 2
	down.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(down, nil, nil)
	if !md.gest.secondary || md.gest.phase != gestPending {
		t.Fatalf("after secondary down: secondary=%v phase=%v", md.gest.secondary, md.gest.phase)
	}
	// Finger drift past the slop must NOT convert to drag/rotate — it stays a tap-select.
	drift := rawEvent("pointermove", 400+gestureMoveSlopPx+10, 300)
	drift.Button = 2
	drift.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(drift, nil, nil)
	if md.gest.phase != gestPending {
		t.Fatalf("secondary tap converted out of pending: phase=%v", md.gest.phase)
	}
	up := rawEvent("pointerup", 400+gestureMoveSlopPx+10, 300)
	up.Button = 2
	up.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(up, nil, nil)
	if md.selected != "N7" {
		t.Fatalf("selected=%q want N7 after secondary tap-select through drift", md.selected)
	}

	// Two-finger tap on EMPTY space (with drift) clears the current selection.
	d2 := rawEvent("pointerdown", 400, 300) // Hit defaults to empty
	d2.Button = 2
	md.HandleRawInput(d2, nil, nil)
	m2 := rawEvent("pointermove", 400+gestureMoveSlopPx+10, 300)
	m2.Button = 2
	md.HandleRawInput(m2, nil, nil)
	u2 := rawEvent("pointerup", 400+gestureMoveSlopPx+10, 300)
	u2.Button = 2
	md.HandleRawInput(u2, nil, nil)
	if md.selected != "" {
		t.Fatalf("selected=%q want empty (cleared) after secondary empty-space tap", md.selected)
	}
}

// A handhold grab resolves (past the slop) to axis-locked orbit: the camera pose changes
// (pos moves) while the pivot + radius stay fixed, just like a free orbit.
func TestGestureHandholdOrbits(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "handhold"}
	md.HandleRawInput(down, nil, nil)
	if !md.gest.handholdDown || md.gest.phase != gestPending {
		t.Fatalf("after handhold down: handholdDown=%v phase=%v", md.gest.handholdDown, md.gest.phase)
	}
	md.HandleRawInput(rawEvent("pointermove", 460, 320), nil, nil)
	if md.gest.phase != gestHandhold {
		t.Fatalf("phase=%v want handhold", md.gest.phase)
	}
	rBefore, pivotBefore, posBefore := md.vp.r, md.vp.pivot, md.vp.pos
	md.HandleRawInput(rawEvent("pointermove", 520, 360), nil, nil)
	if math.Abs(md.vp.r-rBefore) > 1e-9 {
		t.Fatalf("handhold orbit changed r: %v != %v", md.vp.r, rBefore)
	}
	if !vecClose(md.vp.pivot, pivotBefore, 1e-9) {
		t.Fatalf("handhold orbit moved pivot: %v != %v", md.vp.pivot, pivotBefore)
	}
	if angularDistance(md.vp.pos, posBefore) < 1e-6 {
		t.Fatalf("handhold orbit did not change pos (stayed %v)", md.vp.pos)
	}
	md.HandleRawInput(rawEvent("pointerup", 520, 360), nil, nil)
	if md.gest.phase != gestIdle {
		t.Fatalf("after handhold up phase=%v want idle", md.gest.phase)
	}
}

// Dragging a CONNECTED port along its ring dispatches a ring-anchor update to the node
// mover's inbox (the same moveMsgKindAnchor the op=update kind=node attr=anchor path sends).
func TestGestureConnectedPortRingMove(t *testing.T) {
	center := vec3{0, 0, 0}
	geoms := map[string]nodeGeom{
		"N1": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(center), Inputs: []portGeom{{Name: "in"}}, Outputs: []portGeom{{Name: "out"}}},
		"N2": {Kind: "Input", HasPos: true, ScenePolar: cart2polar(vec3{50, 0, 0}), Inputs: []portGeom{{Name: "in"}}},
	}
	edges := map[string]EdgeEndpoints{
		"e1": {Source: "N1", Target: "N2", SourceHandle: "out", TargetHandle: "in"},
	}
	md := newMoveDispatch(geoms, edges, nil)
	md.vp.viewpoint = canonicalViewpoint()
	md.SetPortRowResolver(stubPortRows{{Node: "N1", Port: "out", IsInput: false}})

	// Grab the CONNECTED out-port of N1.
	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "port", PortRow: 0, IsInput: false}
	md.HandleRawInput(down, nil, nil)
	if md.gest.portMoveNode != "N1" {
		t.Fatalf("connected-port down: portMoveNode=%q want N1 (phase=%v)", md.gest.portMoveNode, md.gest.phase)
	}
	// Drag past slop, off-center so the ring direction is nonzero.
	md.HandleRawInput(rawEvent("pointermove", 520, 300), nil, nil)
	if md.gest.phase != gestPortMove {
		t.Fatalf("phase=%v want portMove", md.gest.phase)
	}
	// The N1 mover inbox (buffered) must have received an anchor update.
	select {
	case msg := <-md.nodeMovers["N1"].inbox:
		if msg.Kind != moveMsgKindAnchor || msg.NodeID != "N1" || msg.Port != "out" || msg.IsInput {
			t.Fatalf("anchor msg mismatch: %+v", msg)
		}
	default:
		t.Fatalf("no anchor message dispatched to N1 mover")
	}
}

// A short press-release under the move slop stays in pending and resolves as a click
// (recognized only); it must NOT change the camera pose.
func TestGestureClickNoCameraChange(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	before := md.vp.viewpoint
	nodeHit := rawEvent("pointerdown", 400, 300)
	nodeHit.Hit = rawHit{Kind: "empty"}
	md.HandleRawInput(nodeHit, nil, nil)
	md.HandleRawInput(rawEvent("pointerup", 402, 301), nil, nil) // <6px → click
	if md.vp.viewpoint != before {
		t.Fatalf("click changed camera: %+v != %+v", md.vp.viewpoint, before)
	}
	if md.gest.phase != gestIdle {
		t.Fatalf("after click phase=%v want idle", md.gest.phase)
	}
}

// A node click sets md.selected regardless of the selSpherePoles overlay state (the
// rule-builder authoring path that used to intercept it under selSpherePoles has been
// removed; click-select is now uniform).
func TestGestureSelectModeOffStillHighlights(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	md.ov.selSpherePolesVisible = false
	md.SetNodeRowResolver(stubNodeRows{"A"})

	down := rawEvent("pointerdown", 400, 300)
	down.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(down, nil, nil)
	up := rawEvent("pointerup", 401, 300)
	up.Hit = rawHit{Kind: "node", NodeRow: 0}
	md.HandleRawInput(up, nil, nil)

	if md.selected != "A" {
		t.Fatalf("selected=%q after node click with select mode OFF, want A", md.selected)
	}
}
