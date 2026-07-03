package Wiring

import (
	"math"
	"strings"

	T "github.com/dtauraso/wirefold/Trace"
)

// gesture.go — the GESTURE STATE MACHINE. It consumes RAW pointer/wheel input (forwarded
// fire-and-forget from TS behind USE_RAW_INPUT) plus the stateless raycast hit, owns the
// in-progress gesture bookkeeping (origin, button, phase, frozen rotation frame), and
// decides what the raw input MEANS — orbit / zoom / pan / drag / wire. This is the one place
// gesture state lives (the spec's "gesture state machine lives in Go, in one place"); TS
// holds none of it.
//
// The camera OUTCOMES are produced through the already-tested polar viewpoint ops
// (OrbitViewpoint / ZoomViewpoint / PanViewpoint → spherical.go), fed by the renderer-edge
// camera math in gesture_camera.go (ported formula-for-formula from the TS handlers). This
// file adds no new orbit/rotation math — it only sequences gestures and calls the ported
// helpers.
//
// States:
//   idle      — nothing in progress.
//   pending   — pointer is down; not yet past the move slop. Resolves to a drag/rotate on
//               the first move past MOVE_SLOP_PX, or to a click/wire on pointer-up.
//   rotating  — empty-space great-circle orbit about a frozen region-focus pivot.
//   dragging  — node body drag (world target on a camera-facing plane → RootMove).
//   wiring    — an unconnected port is being dragged toward another port to wire an edge.
//   portMove  — a CONNECTED port is being dragged along its node's ring (ring-anchor snap).
//   handhold  — a handhold grab-sphere is dragged for axis-locked (constrained) orbit.
//
// Phase 7 closed the interaction gaps: edge creation reuses the existing create-edge slot
// path (createEdgeInSlot); click-select is Go-owned (md.selected + KindSelect trace →
// buffer Selected column); handhold-constrained orbit and connected-port ring-move are
// ported here formula-faithfully from interaction-handlers.ts.

type gesturePhase int

const (
	gestIdle gesturePhase = iota
	gestPending
	gestRotating
	gestDragging
	gestWiring
	gestPortMove
	gestHandhold
)

// gestureState is the FSM's owned bookkeeping. Zero value = idle.
type gestureState struct {
	phase gesturePhase

	// pointer-down snapshot + running previous position (client pixels)
	downX, downY float64
	prevX, prevY float64
	button       int
	// secondary is true when the pointer-down was a SECONDARY (button 2) press — a
	// two-finger trackpad tap. Mirrors interaction-handlers.ts `secondaryDown`: such a
	// press is always a tap-select and NEVER converts to a drag/rotate, so it stays
	// `gestPending` through any finger drift and resolves to a select on pointer-up.
	secondary bool

	// empty-space rotation gate + the entity grabbed at pointer-down
	emptyDown bool

	// node-drag target
	dragNode        string
	dragStartCenter vec3

	// wiring source port (unconnected port grabbed at pointer-down)
	wireNode  string
	wirePort  string
	wireInput bool

	// connected-port ring-move (portMove): the grabbed port + its node's center at grab.
	portMoveNode   string
	portMovePort   string
	portMoveInput  bool
	portMoveCenter vec3

	// handhold-constrained orbit gate (set at pointer-down on a handhold hit).
	handholdDown bool

	// rotation frame, FROZEN at gesture start (mirrors beginSphereRotation): the pivot,
	// its screen-pixel center, and pixels-per-radian for screenToPolar.
	rotPivot     vec3
	rotCx, rotCy float64
	rotPxPerRad  float64

	// per-gesture render params captured from the raw events
	fov  float64
	rect gestureRect
}

type gestureRect struct{ left, top, width, height float64 }

func (r gestureRect) aspect() float64 {
	if r.height == 0 {
		return 1
	}
	return r.width / r.height
}

// HandleRawInput is the FSM entry point: one raw pointer/wheel event → gesture state update
// and (possibly) a camera or topology change. Called by the stdin reader for a
// type=="raw-input" message. slotReg resolves an edge's destination slot; tr emits camera
// events + breadcrumbs. Fire-and-forget: nothing here triggers delivery.
func (md *MoveDispatch) HandleRawInput(ev rawInputMsg, slotReg SlotRegistry, tr *T.Trace) {
	g := &md.gest
	g.fov = ev.Fov
	g.rect = gestureRect{left: ev.RectLeft, top: ev.RectTop, width: ev.RectWidth, height: ev.RectHeight}
	switch ev.Kind {
	case "pointerdown":
		md.gestPointerDown(ev, tr)
	case "pointermove":
		md.gestPointerMove(ev, tr)
	case "pointerup":
		md.gestPointerUp(ev, slotReg, tr)
	case "wheel":
		md.gestWheel(ev, tr)
	case "home":
		md.gestHome(ev, tr)
	}
}

// gestHome handles a "home" (fit-to-content) command: Go frames ALL nodes from its OWN held
// geometry with the SAME fit math the TS HomeButton used (homeFitPose), then installs the
// result via SetViewpoint + EmitViewpoint — the exact path a gesture uses, so it streams out
// (pump → useCameraStore → CameraFromStore) and persists on the polar save path. TS sent no
// pose, only render context (fov + aspect). Because the FSM's own viewpoint now IS the framed
// pose, the next orbit/pan/zoom builds on it (no snap-back). Does nothing when there are no
// nodes, mirroring HomeButton's early return.
func (md *MoveDispatch) gestHome(ev rawInputMsg, tr *T.Trace) {
	centers := md.heldCenters()
	radius := make(map[string]float64, len(centers))
	for id := range centers {
		radius[id] = md.nodeBodyRadius(id)
	}
	pivot, r, pos, up, ok := homeFitPose(centers, radius, ev.Fov, md.gest.rect.aspect())
	if !ok {
		return
	}
	md.SetViewpoint(pivot, r, pos, up)
	md.EmitViewpoint(tr)
}

// nodeBodyRadius is the node's body sphere radius used to size the home fit. It reuses the
// SAME nodeRadius the pre-branch HomeButton framed with (geometry-helpers.ts nodeRadius ←
// getNodeGeometry(id).radius, the streamed radius the buffer also renders), i.e. the shared
// port_geometry.go nodeRadius(kind) = min(width,height)/CurveParamNodeRadiusDivisor with the
// (110,60) default for an unknown kind. Framing an unknown-kind node as a zero-size POINT
// (the earlier behavior) tightened the fit vs the pre-branch, which framed it at radius 15.
func (md *MoveDispatch) nodeBodyRadius(id string) float64 {
	return nodeRadius(md.NodeKind(id))
}

// pixelToNDC mirrors geometry-helpers.ts pixelToNDC.
func (g *gestureState) pixelToNDC(x, y float64) (nx, ny float64) {
	nx = ((x-g.rect.left)/g.rect.width)*2 - 1
	ny = -((y-g.rect.top)/g.rect.height)*2 + 1
	return nx, ny
}

func (md *MoveDispatch) gestPointerDown(ev rawInputMsg, tr *T.Trace) {
	g := &md.gest
	g.downX, g.downY = ev.X, ev.Y
	g.prevX, g.prevY = ev.X, ev.Y
	g.button = ev.Button
	g.secondary = ev.Button == 2 // two-finger trackpad tap → always a tap-select
	g.phase = gestPending
	g.emptyDown = false
	g.dragNode = ""
	g.wireNode = ""
	g.portMoveNode = ""
	g.handholdDown = false

	switch ev.Hit.Kind {
	case "port":
		node, port, isInput, ok := md.portFromHit(ev.Hit)
		if !ok {
			return
		}
		if md.portConnected(node, port, isInput) {
			// Connected port → ring-move along the node's ring. Freeze the node center
			// (the ring plane is z = center.z) at grab, mirroring portMoveRef.nodeCenter.
			if c, ok := md.centerOfNode(node); ok {
				g.portMoveNode, g.portMovePort, g.portMoveInput = node, port, isInput
				g.portMoveCenter = c
			}
			return
		}
		g.wireNode, g.wirePort, g.wireInput = node, port, isInput
	case "handhold":
		// Handhold grab → axis-locked (constrained) orbit. Freeze the sphere rotation frame
		// now (mirrors interaction-handlers.ts: beginSphereRotation on a handhold hit).
		g.handholdDown = true
		md.beginSphereRotation(ev)
	case "node":
		if node, ok := md.nodeFromHit(ev.Hit); ok {
			if c, ok := md.centerOfNode(node); ok {
				g.dragNode = node
				g.dragStartCenter = c
			}
		}
	case "empty":
		g.emptyDown = true
		md.beginSphereRotation(ev)
	}
}

// beginSphereRotation mirrors interaction-handlers.ts beginSphereRotation: freeze the orbit
// pivot (regionFocus), its screen-pixel center, and pixels-per-radian for the whole gesture.
func (md *MoveDispatch) beginSphereRotation(ev rawInputMsg) {
	g := &md.gest
	vp := md.vp.viewpoint
	pivot := regionFocus(vp, md.heldCenters())
	g.rotPivot = pivot

	eye := eyeOf(vp)
	basis := basisFromViewpoint(vp.pos, vp.up)
	ndcX, ndcY, _ := projectNDC(pivot, eye, basis, ev.Fov, g.rect.aspect())
	g.rotCx = ((ndcX+1)/2)*g.rect.width + g.rect.left
	g.rotCy = ((-ndcY+1)/2)*g.rect.height + g.rect.top

	_, csRadius := contentSphereOf(md.heldCenters())
	pivotDist := eye.sub(pivot).length()
	fovRad := ev.Fov * math.Pi / 180
	rpx := (csRadius / pivotDist) * (g.rect.height / 2) / math.Tan(fovRad/2)
	g.rotPxPerRad = math.Max(rpx*(2/math.Pi), 1)
}

func (md *MoveDispatch) gestPointerMove(ev rawInputMsg, tr *T.Trace) {
	g := &md.gest
	if g.phase == gestIdle {
		return
	}
	dx := ev.X - g.downX
	dy := ev.Y - g.downY
	dist := math.Hypot(dx, dy)

	// A secondary (two-finger) press never becomes a drag/rotate — it is a tap-select, so
	// it stays gestPending through any finger drift and resolves on pointer-up (mirrors
	// interaction-handlers.ts handlePointerMove's `!s.secondaryDown` guard).
	if g.phase == gestPending && dist > gestureMoveSlopPx && !g.secondary {
		switch {
		case g.wireNode != "":
			g.phase = gestWiring
		case g.portMoveNode != "":
			g.phase = gestPortMove
		case g.dragNode != "":
			g.phase = gestDragging
		case g.handholdDown:
			// Handhold-constrained orbit: seed prevX/prevY from the GRAB point (downX/downY),
			// not the slop-crossing point, so the first locked arc is grab→first-move (mirrors
			// interaction-handlers.ts). Seed the viewpoint about the frozen pivot, then lock.
			g.prevX, g.prevY = g.downX, g.downY
			g.phase = gestHandhold
			md.seedOrbitPivot(g.rotPivot)
		case g.emptyDown:
			g.prevX, g.prevY = ev.X, ev.Y
			g.phase = gestRotating
			// Seed the viewpoint so the orbit pivot is the frozen region-focus (mirrors the
			// TS sendViewpointSet at rotation start). pos/up/r recompute about the new pivot.
			md.seedOrbitPivot(g.rotPivot)
		}
	}

	switch g.phase {
	case gestDragging:
		if md.applyNodeDragTarget(ev) {
			g.prevX, g.prevY = ev.X, ev.Y
		}
	case gestRotating:
		md.applyOrbit(ev, tr)
		g.prevX, g.prevY = ev.X, ev.Y
	case gestHandhold:
		md.applyOrbitLocked(ev, tr)
		g.prevX, g.prevY = ev.X, ev.Y
	case gestPortMove:
		md.applyPortMove(ev)
		g.prevX, g.prevY = ev.X, ev.Y
	}
}

// seedOrbitPivot installs the frozen pivot as the viewpoint pivot (mirrors the TS
// sendViewpointSet at rotation start): pos/up/r recompute about the new pivot so the
// subsequent orbit is rigid about it.
func (md *MoveDispatch) seedOrbitPivot(pivot vec3) {
	vp := md.vp.viewpoint
	eye := eyeOf(vp)
	r := eye.sub(pivot).length()
	pos := worldDirToAngles(eye.sub(pivot))
	md.SetViewpoint(pivot, r, pos, vp.up)
}

// applyOrbit mirrors the "rotating" branch of interaction-handlers.ts handlePointerMove:
// map prev/curr cursor pixels through the frozen sphere frame to world directions and orbit
// (curr → prev), so the grabbed direction follows the cursor.
func (md *MoveDispatch) applyOrbit(ev rawInputMsg, tr *T.Trace) {
	g := &md.gest
	vp := md.vp.viewpoint
	basis := basisFromViewpoint(vp.pos, vp.up)
	prev := screenToPolar(g.prevX-g.rotCx, g.prevY-g.rotCy, g.rotPxPerRad)
	curr := screenToPolar(ev.X-g.rotCx, ev.Y-g.rotCy, g.rotPxPerRad)
	prevDir := toWorldDir(basis, prev)
	currDir := toWorldDir(basis, curr)
	md.OrbitViewpoint(worldDirToAngles(currDir), worldDirToAngles(prevDir), tr)
}

// applyOrbitLocked mirrors the "handhold-rotating" branch of interaction-handlers.ts
// handlePointerMove: identical prev/curr → world-direction mapping as applyOrbit, but the
// arc is applied through OrbitLockedViewpoint, which locks the rotation axis on the first
// call and reuses it (constrained "disk" orbit). The lock clears on the next SetViewpoint.
func (md *MoveDispatch) applyOrbitLocked(ev rawInputMsg, tr *T.Trace) {
	g := &md.gest
	vp := md.vp.viewpoint
	basis := basisFromViewpoint(vp.pos, vp.up)
	prev := screenToPolar(g.prevX-g.rotCx, g.prevY-g.rotCy, g.rotPxPerRad)
	curr := screenToPolar(ev.X-g.rotCx, ev.Y-g.rotCy, g.rotPxPerRad)
	prevDir := toWorldDir(basis, prev)
	currDir := toWorldDir(basis, curr)
	md.OrbitLockedViewpoint(worldDirToAngles(currDir), worldDirToAngles(prevDir), tr)
}

// applyPortMove mirrors the "port-move" branch of interaction-handlers.ts handlePointerMove:
// project the pointer ray onto the horizontal plane (normal +z) at the node's ring height
// (z = center.z), take the in-plane direction from center to the hit (z zeroed, matching
// pointerRingAnchor), and apply it as a ring-anchor update via the existing anchor path.
func (md *MoveDispatch) applyPortMove(ev rawInputMsg) {
	g := &md.gest
	hit, ok := md.pointerOnRingPlane(ev, g.portMoveCenter.Z)
	if !ok {
		return
	}
	dx := hit.X - g.portMoveCenter.X
	dy := hit.Y - g.portMoveCenter.Y
	if dx == 0 && dy == 0 {
		return
	}
	md.applyRingAnchor(g.portMoveNode, g.portMovePort, g.portMoveInput, vec3{X: dx, Y: dy, Z: 0})
}

// pointerOnRingPlane intersects the pointer ray with the horizontal plane (normal +z) at
// world height planeZ, mirroring interaction-handlers.ts unprojectToPlane. Returns
// (hit, false) if the ray is parallel to the plane or the result is non-finite.
func (md *MoveDispatch) pointerOnRingPlane(ev rawInputMsg, planeZ float64) (vec3, bool) {
	g := &md.gest
	vp := md.vp.viewpoint
	eye := eyeOf(vp)
	basis := basisFromViewpoint(vp.pos, vp.up)
	nx, ny := g.pixelToNDC(ev.X, ev.Y)
	dir := rayDirThroughNDC(nx, ny, basis, ev.Fov, g.rect.aspect())
	if dir.Z == 0 {
		return vec3{}, false
	}
	t := (planeZ - eye.Z) / dir.Z
	hit := eye.add(dir.scale(t))
	if math.IsNaN(hit.X) || math.IsInf(hit.X, 0) {
		return vec3{}, false
	}
	return hit, true
}

// applyNodeDragTarget mirrors the "dragging" branch: unproject the pointer onto a
// camera-facing plane through the node's start center, giving a free world target, then
// RootMove the node (Go snaps it to the parent sphere). Returns false if the ray is parallel
// to the plane.
func (md *MoveDispatch) applyNodeDragTarget(ev rawInputMsg) bool {
	g := &md.gest
	vp := md.vp.viewpoint
	eye := eyeOf(vp)
	basis := basisFromViewpoint(vp.pos, vp.up)
	nx, ny := g.pixelToNDC(ev.X, ev.Y)
	dir := rayDirThroughNDC(nx, ny, basis, ev.Fov, g.rect.aspect())
	forward := basis.pole.scale(-1) // camera looks along -pole
	denom := dir.dot(forward)
	if denom == 0 {
		return false
	}
	t := g.dragStartCenter.sub(eye).dot(forward) / denom
	hit := eye.add(dir.scale(t))
	if math.IsNaN(hit.X) || math.IsInf(hit.X, 0) {
		return false
	}
	md.RootMove(g.dragNode, hit)
	return true
}

func (md *MoveDispatch) gestPointerUp(ev rawInputMsg, slotReg SlotRegistry, tr *T.Trace) {
	g := &md.gest
	switch {
	case g.wireNode != "" && (g.phase == gestWiring || g.phase == gestPending):
		// Wiring completed: if dropped on a port of another node with the opposite
		// direction, the intent is an OUT→IN edge. The FSM orients the pair and hands the
		// destination slot to the EXISTING create-edge path (createEdgeInSlot — the same
		// helper op=create uses); it un-silences that wire so it carries beads again.
		if ev.Hit.Kind == "port" {
			tn, tp, ti, ok := md.portFromHit(ev.Hit)
			if ok && tn != g.wireNode {
				var srcNode, srcPort, dstNode, dstPort string
				oriented := false
				if !g.wireInput && ti { // source out → target in
					srcNode, srcPort, dstNode, dstPort = g.wireNode, g.wirePort, tn, tp
					oriented = true
				} else if g.wireInput && !ti { // grabbed an in, dropped on an out
					srcNode, srcPort, dstNode, dstPort = tn, tp, g.wireNode, g.wirePort
					oriented = true
				}
				if oriented {
					tr.Breadcrumb("gesture-wire", srcNode+":"+srcPort, dstNode+":"+dstPort, "")
					createEdgeInSlot(slotReg, dstNode, dstPort, tr)
				}
			}
		}
	case g.phase == gestPortMove:
		md.applyPortMove(ev) // final ring-anchor flush
	case g.phase == gestDragging:
		md.applyNodeDragTarget(ev) // final target flush
	case g.phase == gestHandhold, g.phase == gestRotating:
		// Rotation completed (free or handhold-constrained): nothing to flush.
	case g.phase == gestPending:
		// Click → Go-owned selection. A node hit selects it; empty space clears the
		// selection. md.selected is the authoritative selection; Select() emits it so the
		// buffer snapshot marks the node's Selected column. g.secondary (two-finger tap)
		// picks the "own" select mode; a primary click picks "surface".
		md.applySelect(ev, tr, g.secondary)
	}
	g.reset()
}

// applySelect sets the Go-owned selection from a click hit and emits it. Selection is
// single + EXCLUSIVE across nodes and edges: an EDGE hit selects that edge (clearing any
// node selection); a node/port hit selects that node (clearing any edge selection); an
// empty (or handhold) hit clears both.
func (md *MoveDispatch) applySelect(ev rawInputMsg, tr *T.Trace, own bool) {
	if ev.Hit.Kind == "edge" {
		if label, ok := md.edgeFromHit(ev.Hit); ok {
			md.selectedEdge = label
			md.selected = ""
			tr.SelectEdge(label)
			return
		}
		// Unresolvable edge hit → clear selection rather than leaving stale state.
	}

	var node string
	switch ev.Hit.Kind {
	case "node":
		if n, ok := md.nodeFromHit(ev.Hit); ok {
			node = n
		}
	case "port":
		if n, _, _, ok := md.portFromHit(ev.Hit); ok {
			node = n
		}
	}
	md.selected = node
	md.selectedEdge = ""
	tr.Select(node, own)
}

// ToggleFadeSelection flips the fade state of the CURRENTLY-SELECTED entity (the pre-branch
// "f" key press). Selection is Go-owned (md.selected / md.selectedEdge), so the TS "f" press
// is a BARE command — Go resolves which node/edge is selected here. A selected edge toggles
// its edge-seed; otherwise a selected node toggles its node-seed. Nothing selected = no-op.
// After flipping, it emits the FULL directly-faded seed sets so the buffer snapshot mirrors
// them and recomputes the fade fixpoint.
func (md *MoveDispatch) ToggleFadeSelection(tr *T.Trace) {
	if md.directlyFadedNodes == nil {
		md.directlyFadedNodes = map[string]bool{}
	}
	if md.directlyFadedEdges == nil {
		md.directlyFadedEdges = map[string]bool{}
	}
	switch {
	case md.selectedEdge != "":
		toggleSet(md.directlyFadedEdges, md.selectedEdge)
	case md.selected != "":
		toggleSet(md.directlyFadedNodes, md.selected)
	default:
		return // nothing selected
	}
	if tr != nil {
		tr.Fade(setToSlice(md.directlyFadedNodes), setToSlice(md.directlyFadedEdges))
	}
}

// toggleSet flips key's membership in set (add if absent, delete if present).
func toggleSet(set map[string]bool, key string) {
	if set[key] {
		delete(set, key)
	} else {
		set[key] = true
	}
}

// setToSlice returns the set's members as a fresh slice (safe to hand across the Trace
// bridge; the caller keeps mutating its map).
func setToSlice(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// nodeFromHit resolves a node hit to its node id. On the new-system path a node hit carries
// only a numeric buffer NODE-ROW index (the node InstancedMesh instanceId == its buffer node
// row); Go maps it back through its own node-row table (nodeRows), since Go owns the topology
// and wrote the Node block in that same row order. When no resolver is wired (old path / unit
// tests) it falls back to the hit's Id string.
func (md *MoveDispatch) nodeFromHit(h rawHit) (node string, ok bool) {
	if md.nodeRows != nil && h.NodeRow >= 0 {
		return md.nodeRows.LookupNodeRow(h.NodeRow)
	}
	if h.Id != "" {
		return h.Id, true
	}
	return "", false
}

// edgeFromHit resolves an edge hit to its edge label. On the new-system path an edge hit
// carries only a numeric buffer EDGE-ROW index (no label string); Go maps it back through
// its own edge-row table (edgeRows), since Go owns the topology and wrote the Edge block in
// that same row order. When no resolver is wired (old path / unit tests) it falls back to
// the hit's Id string.
func (md *MoveDispatch) edgeFromHit(h rawHit) (label string, ok bool) {
	if md.edgeRows != nil && h.EdgeRow >= 0 {
		return md.edgeRows.LookupEdgeRow(h.EdgeRow)
	}
	if h.Id != "" {
		return h.Id, true
	}
	return "", false
}

// gestWheel mirrors interaction-handlers.ts handleWheelNative: ctrl+wheel = zoom-to-cursor
// dolly (expressed as a PAN in the polar model — a pivot translation, not a radius change),
// plain wheel = screen-space pan. Both first seed the viewpoint to region-focus, then pan.
func (md *MoveDispatch) gestWheel(ev rawInputMsg, tr *T.Trace) {
	vp := md.vp.viewpoint
	eye := eyeOf(vp)
	basis := basisFromViewpoint(vp.pos, vp.up)
	pivot := regionFocus(vp, md.heldCenters())
	r := eye.sub(pivot).length()
	pos := worldDirToAngles(eye.sub(pivot))

	if ev.Ctrl {
		// Dolly toward the node nearest the cursor in NDC (fallback region-focus).
		mouseNdcX, mouseNdcY := md.gest.pixelToNDC(ev.X, ev.Y)
		target := pivot
		best := math.Inf(1)
		aspect := md.gest.rect.aspect()
		for _, c := range md.heldCenters() {
			nx, ny, inFront := projectNDC(c, eye, basis, ev.Fov, aspect)
			if !inFront {
				continue
			}
			d := math.Hypot(nx-mouseNdcX, ny-mouseNdcY)
			if d < best {
				best = d
				target = c
			}
		}
		toP := target.sub(eye)
		distP := toP.length()
		factor := math.Pow(gestureZoomBase, ev.DeltaY)
		if distP*factor < gestureMinDist && distP != 0 {
			factor = gestureMinDist / distP
		}
		delta := toP.scale(1 - factor)
		md.SetViewpoint(pivot, r, pos, vp.up)
		md.PanViewpoint(delta, tr)
		return
	}

	// Plain wheel = screen-space pan along the camera right/up basis.
	fovRad := ev.Fov * math.Pi / 180
	worldPerPixel := (2 * r * math.Tan(fovRad/2)) / md.gest.rect.height
	pr, angle := deltaToPolar(ev.DeltaX, -ev.DeltaY)
	delta := planeSlide(basis, pr, angle, worldPerPixel)
	md.SetViewpoint(pivot, r, pos, vp.up)
	md.PanViewpoint(delta, tr)
}

func (g *gestureState) reset() {
	g.phase = gestIdle
	g.emptyDown = false
	g.dragNode = ""
	g.wireNode = ""
	g.portMoveNode = ""
	g.handholdDown = false
	g.secondary = false
}

// applyRingAnchor snaps a world-space direction (node center → pointer) to the node's
// nearest ring-anchor index and mail-sorts a moveMsgKindAnchor to the node's mover AND
// every incident edge mover — the SAME dispatch the op=update kind=node attr=anchor path
// uses (applyUpdate). Live-only (no disk persistence), matching the FSM node-drag path.
func (md *MoveDispatch) applyRingAnchor(node, port string, isInput bool, dir vec3) {
	anchorID := snapToRingAnchorIndex(md.NodeKind(node), dir)
	msg := moveMsg{Kind: moveMsgKindAnchor, NodeID: node, Port: port, IsInput: isInput, AnchorId: anchorID}
	if ch, ok := md.dispatch[node]; ok {
		ch <- msg
	}
	for edgeID, em := range md.edgeMovers {
		incident := (isInput && em.dstID == node && em.dstH == port) ||
			(!isInput && em.srcID == node && em.srcH == port)
		if !incident {
			continue
		}
		if ch, ok := md.dispatch[edgeID]; ok {
			ch <- msg
		}
	}
}

// portConnected reports whether the named port has at least one incident edge. It scans the
// edge movers' endpoints (the held topology) — the FSM's own state, not a fact carried on
// the wire from TS.
func (md *MoveDispatch) portConnected(node, port string, isInput bool) bool {
	for _, em := range md.edgeMovers {
		if isInput {
			if em.dstID == node && em.dstH == port {
				return true
			}
		} else {
			if em.srcID == node && em.srcH == port {
				return true
			}
		}
	}
	return false
}

// portFromHit resolves a port hit to its (node, port, isInput) identity. On the new-system
// path a port hit carries only a numeric buffer PORT-ROW index (no name string); Go maps it
// back through its own port-row table (portRows), since Go owns the topology and wrote the
// Port block in that same row order. When no resolver is wired (old path / unit tests) it
// falls back to parsing the legacy "nodeId:in|out:portName" id string.
func (md *MoveDispatch) portFromHit(h rawHit) (node, port string, isInput, ok bool) {
	if md.portRows != nil && h.PortRow >= 0 {
		return md.portRows.LookupPortRow(h.PortRow)
	}
	return parseGesturePortId(h.Id)
}

// parseGesturePortId splits a port id of the form "nodeId:in:portName" / "nodeId:out:portName"
// (mirrors interaction-handlers.ts parsePortId). Returns ok=false on a malformed id.
func parseGesturePortId(pid string) (node, port string, isInput, ok bool) {
	i := strings.IndexByte(pid, ':')
	if i < 0 {
		return "", "", false, false
	}
	node = pid[:i]
	rest := pid[i+1:]
	j := strings.IndexByte(rest, ':')
	if j < 0 {
		return "", "", false, false
	}
	dir := rest[:j]
	port = rest[j+1:]
	return node, port, dir == "in", true
}
