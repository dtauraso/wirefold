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

	// Polar rule-builder session state (active only while the selSpherePoles overlay is
	// ON — see trySelectSphereRule / clearRuleBuilding). A handhold click latches a
	// pending half-term (comp, sign); a subsequent node click completes it. Every
	// consecutive PAIR of completed terms forms one polarEq about md.selected (the
	// latched Center), appended to md.polarEqs.
	hasPending  bool
	pendingComp polarComp
	pendingSign float64
	ruleTerms   []polarTerm

	// `port ∈ torus` lock capture (independent of the node/node hasPending/ruleTerms above):
	// a PORT click latches the port; a subsequent TORUS click completes the lock and appends
	// an eqPortTorus entry to md.polarEqs. Does not touch pendingComp/pendingSign/ruleTerms.
	hasPendingPort  bool
	pendingPortNode string
	pendingPortName string
	pendingPortIn   bool

	// the other half of the `port ∈ torus` pair: a TORUS click latches the owning node id
	// when no port is pending yet, so port-then-torus AND torus-then-port both complete the
	// lock (whichever hit arrives second finds the other side already latched).
	hasPendingTorus  bool
	pendingTorusNode string

	// authoringKind names which equation KIND the keyboard-authoring channel (AuthorBegin) is
	// currently building. Set by AuthorBegin; the click path never sets it (it infers kind
	// implicitly from hit type). See gesture.go's "Keyboard-authoring channel" section.
	authoringKind eqKind
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
		md.updateHover(ev, tr)
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
			// (A plain click without crossing the drag slop still resolves via the
			// gestPending fallthrough on pointer-up, so this doesn't block select-mode
			// `port ∈ torus` authoring — only an actual drag reaches gestPortMove.)
			if c, ok := md.centerOfNode(node); ok {
				g.portMoveNode, g.portMovePort, g.portMoveInput = node, port, isInput
				g.portMoveCenter = c
			}
			return
		}
		if md.ov.selSpherePolesVisible {
			// Select mode: an UNCONNECTED port hit is `port ∈ torus` authoring on
			// pointer-up (trySelectSphereRule), not a wiring-drag grab. Leave g.wireNode
			// unset so gestPointerUp's gestPending fallthrough reaches trySelectSphereRule.
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

// updateHover resolves the entity under the pointer from the raycast hit and, WHEN IT
// CHANGES, records it as the Go-owned hover and emits KindHover so the buffer snapshot marks
// the node's / port's Hovered column. Hover is node+port only (edges do not hover on the
// pre-branch path). Deduping on the (node, port, isInput) triple keeps a still pointer and a
// same-entity drag from re-emitting a snapshot each pointer-move (no new flood — Go already
// emits per raw-input; a hover only fires on a genuine target change). An empty / edge / other
// hit clears hover.
func (md *MoveDispatch) updateHover(ev rawInputMsg, tr *T.Trace) {
	var node, port string
	var isInput bool
	switch ev.Hit.Kind {
	case "port":
		if n, p, in, ok := md.portFromHit(ev.Hit); ok {
			node, port, isInput = n, p, in
		}
	case "torus":
		// The concentric hover ring emphasizes the TORUS handle, so it lights only when the
		// cursor is actually on the ring — NOT on the node body. A plain "node"-body hit
		// deliberately falls through here and clears hover (node-body hover feedback is a
		// separate concern, not wired yet).
		if n, ok := md.nodeFromHit(ev.Hit); ok {
			node = n
		}
	}
	md.setHover(node, port, isInput, tr)
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
		if !md.trySelectSphereRule(ev, tr) {
			md.applySelect(ev, tr, g.secondary)
		}
	}
	g.reset()
}

// trySelectSphereRule handles a click while the selSpherePoles overlay is ON: node clicks
// author a polar rule instead of changing selection. Returns true when it handled the
// click (suppressing the normal click-select); false when the overlay is off or the hit
// doesn't participate in rule-building (falls through to applySelect, e.g. to pick the
// Center sphere itself).
//
//   - A handhold hit (Hit.Kind=="handhold", HandholdTerm>=0) latches a pending half-term
//     decoded from the term-id (+θ=0, +φ=1, -θ=2, -φ=3, r=4; see decodeTermCode) WITHOUT
//     touching md.selected.
//   - A node hit while a half-term is pending completes the term {Node, comp, sign} and
//     appends it to ruleTerms. Once two terms have accumulated (a full equation) and
//     md.selected names a Center, they form one polarEq appended to md.polarEqs and the
//     accumulator resets for the next equation (e.g. a mirror's θ-pair then φ-pair).
func (md *MoveDispatch) trySelectSphereRule(ev rawInputMsg, tr *T.Trace) bool {
	if !md.ov.selSpherePolesVisible {
		return false
	}
	g := &md.gest
	switch {
	case ev.Hit.Kind == "handhold" && ev.Hit.HandholdTerm >= 0:
		comp, sign := decodeTermCode(ev.Hit.HandholdTerm)
		md.authorHalfTerm(comp, sign, tr)
		return true
	case ev.Hit.Kind == "node" && g.hasPending:
		node, ok := md.nodeFromHit(ev.Hit)
		if !ok {
			g.hasPending = false
			md.emitRuleBuilder(tr)
			return true // suppress select even when the hit is unresolvable
		}
		md.authorNode(node, tr)
		return true
	case ev.Hit.Kind == "node":
		// Select mode ON, no handhold pending: a plain node-body click does NOT highlight
		// (highlighting is an applySelect/select-mode-OFF behavior only), but the equation
		// panel must still show for the clicked node — set the sticky panel target
		// (md.ruleCenter) and emit the RuleBuilder row without touching md.selected or any
		// pending port/torus captures.
		if node, ok := md.nodeFromHit(ev.Hit); ok {
			md.authorNode(node, tr)
		}
		return true
	case ev.Hit.Kind == "port":
		node, port, isInput, ok := md.portFromHit(ev.Hit)
		if !ok {
			return true
		}
		if g.hasPendingTorus {
			// Torus was captured first; this port click completes the `port ∈ torus` lock.
			torusNode := g.pendingTorusNode
			g.hasPendingTorus = false
			md.addPortTorusLock(node, port, isInput, torusNode, tr)
			return true
		}
		// Latch the clicked port; a subsequent torus click completes the lock.
		g.hasPendingPort = true
		g.pendingPortNode = node
		g.pendingPortName = port
		g.pendingPortIn = isInput
		md.emitRuleBuilder(tr)
		return true
	case ev.Hit.Kind == "torus":
		torusNode, ok := md.nodeFromHit(ev.Hit)
		if !ok {
			return true
		}
		if g.hasPendingPort {
			// Port was captured first; this torus click completes the `port ∈ torus` lock.
			portNode, portName, portIn := g.pendingPortNode, g.pendingPortName, g.pendingPortIn
			g.hasPendingPort = false
			md.addPortTorusLock(portNode, portName, portIn, torusNode, tr)
			return true
		}
		// Latch the clicked torus (owning node); a subsequent port click completes the lock.
		g.hasPendingTorus = true
		g.pendingTorusNode = torusNode
		md.emitRuleBuilder(tr)
		return true
	default:
		return false
	}
}

// authorHalfTerm latches a pending half-term (comp, sign) WITHOUT touching md.selected —
// the shared body of trySelectSphereRule's handhold-hit case and AuthorLatchHalfTerm's typed
// equivalent, so a click and a typed comp word drive the exact same builder state.
func (md *MoveDispatch) authorHalfTerm(comp polarComp, sign float64, tr *T.Trace) {
	g := &md.gest
	g.pendingComp, g.pendingSign = comp, sign
	g.hasPending = true
	md.emitRuleBuilder(tr)
}

// authorNode resolves an already-identified node id against the current builder state,
// exactly like trySelectSphereRule's node-hit cases: with a half-term pending it completes
// the term (and commits the equation on the 2nd completed term); otherwise it latches the
// node as the rule-builder's Center. Shared by the click path (trySelectSphereRule) and the
// typed path (AuthorNode) so Go — not the caller — decides center-vs-term.
func (md *MoveDispatch) authorNode(node string, tr *T.Trace) {
	g := &md.gest
	if g.hasPending {
		g.hasPending = false
		g.ruleTerms = append(g.ruleTerms, polarTerm{Node: node, Comp: g.pendingComp, Sign: g.pendingSign})
		// Center is the rule-builder's authored center (md.ruleCenter — the node clicked
		// as Center while authoring), NOT md.selected: in select mode a node-body click
		// sets md.ruleCenter and never md.selected, so keying the Center off md.selected
		// committed an empty Center whose links no-op'd (ensureEqLinks) and whose drag
		// enforcement silently skipped (applyPolarEqs). Gate on ruleCenter for the same reason.
		if len(g.ruleTerms) == 2 && md.ruleCenter != "" {
			eq := polarEq{Center: md.ruleCenter, A: g.ruleTerms[0], B: g.ruleTerms[1], Active: true}
			md.commitPolarEq(eq, tr)
			if tr != nil {
				tr.Breadcrumb("polar-rule-added", md.ruleCenter, node, "")
			}
			g.ruleTerms = nil
		}
		md.emitRuleBuilder(tr)
		md.emitPolarLocks(tr)
		return
	}
	// No half-term pending: a node hit while authoring sets the rule-builder's sticky
	// Center (md.ruleCenter) without touching md.selected — mirrors the plain node-body
	// click's select-mode behavior.
	md.ruleCenter = node
	md.pruneSelectionOffCenter(node, tr)
	md.emitRuleBuilder(tr)
	// Center changed → Owned bits are stale; re-emit so the panel tracks the new Center
	// (pruneSelectionOffCenter only re-emits when it prunes). Same reason as applySelect.
	md.emitPolarLocks(tr)
}

// commitPolarEq appends a fully-formed eqNodeNode polar equation and performs the FOUR steps
// every completion path needs: append + auto-select the just-committed equation + guarantee
// its Center↔term movement links + enforce it immediately (settle term A so its lock write
// flows to term B) + schedule persistence. Both the click-driven builder
// (trySelectSphereRule) and the keyboard-authoring builder (AuthorTerm) funnel through this
// single commit point so the two paths cannot drift apart.
func (md *MoveDispatch) commitPolarEq(eq polarEq, tr *T.Trace) {
	md.appendPolarEq(eq)
	// Auto-select JUST the just-committed equation (replacing any prior multi-selection) so
	// it lands highlighted in the panel list AND draws its diagram guides immediately — both
	// follow selectedLocks.
	md.selectedLocks = []int{len(md.polarEqsSnap()) - 1}
	// Guarantee the Center↔term movement links exist so the equation is enforced even when no
	// topology edge connects the picked nodes to the Center.
	md.ensureEqLinks(eq)
	// Enforce the equation IMMEDIATELY (don't wait for the next drag): settle term A in place
	// so its lock write flows to term B — the just-set side snaps to satisfy the equation now.
	// RootMove runs the full refresh→applyPolarEqs→fan pipeline.
	if c, ok := md.centerOfNode(eq.A.Node); ok {
		md.RootMove(eq.A.Node, c)
	}
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqsSnap())
	}
}

// --- Keyboard-authoring channel ---------------------------------------------------------
//
// AuthorBegin/AuthorLatchHalfTerm/AuthorNode/AuthorPort/AuthorTorus drive the SAME polar-lock
// builder state (gestureState's ruleCenter/ruleTerms/pendingPort*/pendingTorus* fields) the
// click path (trySelectSphereRule) drives, so a typed/resolved token is the exact equivalent
// of a click — AuthorLatchHalfTerm mirrors the handhold-hit case and AuthorNode mirrors the
// node-hit cases (letting Go, not the caller, decide center-vs-term completion) via the same
// authorHalfTerm/authorNode helpers trySelectSphereRule uses. They take already-resolved
// arguments (a buffer NODE-ROW index, resolved via md.nodeRows exactly like a raycast hit's
// NodeRow field — see nodeFromHit) rather than a raycast hit; there is no free-text parsing
// here, the caller (stdin_reader.go applyUpdate) has already decoded the token into these
// typed fields off the wire.

// AuthorBegin starts (or restarts) a keyboard-authored equation of the given kind: it clears
// any half-finished builder state (mirrors clearRuleBuilding, including its emit) and records
// which kind is being built (authoringKind), so a later inspection of the builder state knows
// what a completed pair should become.
func (md *MoveDispatch) AuthorBegin(kind eqKind, tr *T.Trace) {
	md.clearRuleBuilding(tr)
	md.gest.authoringKind = kind
}

// AuthorLatchHalfTerm latches a pending half-term (comp, sign) — the typed equivalent of a
// handhold click (trySelectSphereRule's handhold-hit case) — so the panel shows the same
// `( _ , <chip> )` in-progress preview a click produces.
func (md *MoveDispatch) AuthorLatchHalfTerm(comp polarComp, sign float64, tr *T.Trace) {
	md.authorHalfTerm(comp, sign, tr)
}

// AuthorNode resolves nodeRow → node id and applies it to the builder EXACTLY like a node
// click (trySelectSphereRule's node-hit cases, via the shared authorNode helper): with a
// half-term pending it completes the term (committing the equation on the 2nd completed
// term); otherwise it latches the node as the rule-builder's Center. This subsumes the old
// AuthorCenter/AuthorTerm split — Go decides center-vs-term the same way for both input paths.
func (md *MoveDispatch) AuthorNode(nodeRow int, tr *T.Trace) {
	if md.nodeRows == nil {
		return
	}
	node, ok := md.nodeRows.LookupNodeRow(nodeRow)
	if !ok {
		return
	}
	md.authorNode(node, tr)
}

// AuthorPort resolves nodeRow → node id and latches (or completes) the `port ∈ torus` pair's
// port side, mirroring trySelectSphereRule's "port" hit case — including completing the lock
// via addPortTorusLock when a torus is already pending.
func (md *MoveDispatch) AuthorPort(nodeRow int, portName string, isInput bool, tr *T.Trace) {
	if md.nodeRows == nil {
		return
	}
	node, ok := md.nodeRows.LookupNodeRow(nodeRow)
	if !ok {
		return
	}
	g := &md.gest
	if g.hasPendingTorus {
		torusNode := g.pendingTorusNode
		g.hasPendingTorus = false
		md.addPortTorusLock(node, portName, isInput, torusNode, tr)
		return
	}
	g.hasPendingPort = true
	g.pendingPortNode = node
	g.pendingPortName = portName
	g.pendingPortIn = isInput
	md.emitRuleBuilder(tr)
}

// AuthorTorus resolves nodeRow → node id and latches (or completes) the `port ∈ torus` pair's
// torus side, mirroring trySelectSphereRule's "torus" hit case.
func (md *MoveDispatch) AuthorTorus(nodeRow int, tr *T.Trace) {
	if md.nodeRows == nil {
		return
	}
	torusNode, ok := md.nodeRows.LookupNodeRow(nodeRow)
	if !ok {
		return
	}
	g := &md.gest
	if g.hasPendingPort {
		portNode, portName, portIn := g.pendingPortNode, g.pendingPortName, g.pendingPortIn
		g.hasPendingPort = false
		md.addPortTorusLock(portNode, portName, portIn, torusNode, tr)
		return
	}
	g.hasPendingTorus = true
	g.pendingTorusNode = torusNode
	md.emitRuleBuilder(tr)
}

// SetHoverPortByRow resolves nodeRow → node id and sets the SAME hover state updateHover
// writes for a port hit (dedup + KindHover emit), so a keyboard-authoring preview highlights
// the target port in the streamed buffer exactly like pointer hover does.
func (md *MoveDispatch) SetHoverPortByRow(nodeRow int, portName string, isInput bool, tr *T.Trace) {
	if md.nodeRows == nil {
		return
	}
	node, ok := md.nodeRows.LookupNodeRow(nodeRow)
	if !ok {
		return
	}
	md.setHover(node, portName, isInput, tr)
}

// SetHoverNodeByRow resolves nodeRow → node id and sets the SAME hover state updateHover
// writes for a torus/node hit (node hover, no port).
func (md *MoveDispatch) SetHoverNodeByRow(nodeRow int, tr *T.Trace) {
	if md.nodeRows == nil {
		return
	}
	node, ok := md.nodeRows.LookupNodeRow(nodeRow)
	if !ok {
		return
	}
	md.setHover(node, "", false, tr)
}

// setHover is the shared dedupe+emit hover write used by both updateHover (pointer path) and
// SetHoverPortByRow/SetHoverNodeByRow (keyboard-authoring preview path).
func (md *MoveDispatch) setHover(node, port string, isInput bool, tr *T.Trace) {
	if node == md.hoverNode && port == md.hoverPort && isInput == md.hoverInput {
		return // no change → no re-emit (dedupe)
	}
	md.hoverNode, md.hoverPort, md.hoverInput = node, port, isInput
	if tr != nil {
		tr.Hover(node, port, isInput)
	}
}

// addPortTorusLock appends a `port ∈ torus` equation once BOTH sides of the pair have been
// captured (in either order — see trySelectSphereRule's port/torus cases). STAGE 1: no
// ensureEqLinks call (ensureEqLinks itself skips eqPortTorus) and no RootMove/solve —
// authoring the lock moves nothing yet.
func (md *MoveDispatch) addPortTorusLock(portNode, portName string, portIsInput bool, torusNode string, tr *T.Trace) {
	eq := polarEq{
		Kind:        eqPortTorus,
		PortNode:    portNode,
		PortName:    portName,
		PortIsInput: portIsInput,
		TorusNode:   torusNode,
		Active:      true,
	}
	md.appendPolarEq(eq)
	if tr != nil {
		tr.Breadcrumb("port-torus-lock-added", portNode, torusNode, portName)
	}
	if md.locksPersist != nil {
		md.locksPersist.schedule(md.polarEqsSnap())
	}
	md.emitPolarLocks(tr)
	// The lock is active the instant it's authored — re-emit the constrained port's
	// geometry now so it moves onto its node's border ring immediately (locks.go).
	md.reemitPortTorusGeometry(portNode)
	// Both pending flags were already cleared by the caller before it reached here; mirror
	// that into the RuleBuilder block so the in-progress preview clears the instant the
	// pair commits to the list.
	md.emitRuleBuilder(tr)
}

// ruleTermCode packs a completed/pending term's (comp, sign) to a single code — matches the
// handhold hit's HandholdTerm encoding: +θ=0, +φ=1, −θ=2, −φ=3, r=4 (r is unsigned), i.e.
// the angles use code = (sign<0 ? 2 : 0) + (comp==compPhi ? 1 : 0); r is the fixed code 4.
func ruleTermCode(comp polarComp, sign float64) int {
	if comp == compR {
		return 4
	}
	code := 0
	if sign < 0 {
		code += 2
	}
	if comp == compPhi {
		code++
	}
	return code
}

// decodeTermCode is the inverse of ruleTermCode: a handhold/term code → (comp, sign). Code 4
// is r (unsigned, sign +1); codes 0..3 are the signed angles (comp = code&1, sign = code&2).
func decodeTermCode(code int) (polarComp, float64) {
	if code == 4 {
		return compR, 1
	}
	sign := 1.0
	if code&2 != 0 {
		sign = -1
	}
	return polarComp(code & 1), sign
}

// emitRuleBuilder emits the FULL current rule-builder session state (KindRuleBuilder):
// the latched Center (md.selected), any half-finished pending term, and the accumulated
// completed terms. Called from every rule-builder state-change point (gesture.go +
// clearRuleBuilding + applySelect) so the buffer snapshot's RuleBuilder block always
// mirrors the session live. No-op when tr is nil (headless tests that don't wire Trace).
func (md *MoveDispatch) emitRuleBuilder(tr *T.Trace) {
	if tr == nil {
		return
	}
	g := &md.gest
	terms := make([]T.RuleTermPayload, len(g.ruleTerms))
	for i, t := range g.ruleTerms {
		terms[i] = T.RuleTermPayload{Node: t.Node, Code: ruleTermCode(t.Comp, t.Sign)}
	}
	pendingCode := 0
	if g.hasPending {
		pendingCode = ruleTermCode(g.pendingComp, g.pendingSign)
	}
	pendingPortNode, pendingPortName, pendingPortIsInput := "", "", false
	if g.hasPendingPort {
		pendingPortNode, pendingPortName, pendingPortIsInput = g.pendingPortNode, g.pendingPortName, g.pendingPortIn
	}
	pendingTorusNode := ""
	if g.hasPendingTorus {
		pendingTorusNode = g.pendingTorusNode
	}
	tr.RuleBuilder(md.ruleCenter, g.hasPending, pendingCode, terms, pendingPortNode, pendingPortName, pendingPortIsInput, pendingTorusNode)
}

// clearRuleBuilding ends any in-progress polar rule-building session: half-finished
// pending term + accumulated ruleTerms. Called when the selSpherePoles overlay turns OFF
// (stdin_reader.go applyUpdate) so a stale pending/half-formed pair doesn't leak into the
// next session. Emits the cleared state so the buffer's RuleBuilder block drops the stale
// pending/terms immediately.
func (md *MoveDispatch) clearRuleBuilding(tr *T.Trace) {
	md.gest.hasPending = false
	md.gest.ruleTerms = nil
	md.gest.hasPendingPort = false
	md.gest.hasPendingTorus = false
	md.emitRuleBuilder(tr)
}

// applySelect sets the Go-owned selection from a click hit and emits it. Selection is
// single + EXCLUSIVE across nodes and edges: an EDGE hit selects that edge (clearing any
// node selection); a node/port hit selects that node (clearing any edge selection); an
// empty-space hit CLEARS the transient highlight (md.selected / md.selectedEdge) — this is
// the original click-empty-clears behavior. The rule-builder panel's Center (md.ruleCenter)
// is a SEPARATE, sticky piece of state: it is untouched by an empty-space click and only
// changes when a different node is selected, so the equation panel stays on the last
// selected node even after the highlight ring clears.
func (md *MoveDispatch) applySelect(ev rawInputMsg, tr *T.Trace, own bool) {
	if ev.Hit.Kind == "empty" {
		md.selected = ""
		md.selectedEdge = ""
		tr.Select("", own)
		md.emitRuleBuilder(tr)
		return
	}
	if ev.Hit.Kind == "edge" {
		if label, ok := md.edgeFromHit(ev.Hit); ok {
			md.selectedEdge = label
			md.selected = ""
			tr.SelectEdge(label)
			// Edge selection does not touch the sticky rule-builder Center; leave
			// md.ruleCenter as-is so the panel keeps showing the last-selected node.
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
	md.ruleCenter = node
	md.pruneSelectionOffCenter(node, tr)
	tr.Select(node, own)
	// A selection change always changes the rule-builder's latched sticky Center
	// (md.ruleCenter), so mirror it unconditionally to keep the buffer's RuleBuilder block
	// (CenterRow/centerLabel) tracking the panel. The in-progress builder UI itself stays
	// gated on the selSpherePoles overlay in TS; this only keeps the data fresh underneath it.
	md.emitRuleBuilder(tr)
	// Each equation's Owned bit is (eqOwner == ruleCenter), so a Center change makes every
	// bit stale — re-emit the polar-lock block so the panel (which shows owned rows only)
	// reflects the new Center. pruneSelectionOffCenter above only re-emits when it prunes a
	// selection, so this is required for the no-selection case.
	md.emitPolarLocks(tr)
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
	// Persist the updated fade seeds to scene.json (debounced, fire-and-forget).
	if md.fadePersist != nil {
		md.fadePersist.schedule(setToSlice(md.directlyFadedNodes), setToSlice(md.directlyFadedEdges))
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
	// Persist the snapped anchor index to the port file (debounced, fire-and-forget).
	if md.anchorPersist != nil {
		md.anchorPersist.schedule(node, port, isInput, anchorID)
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
