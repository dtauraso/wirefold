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
//
// Deferred (recognized but not yet acted on in this additive phase; see report): handhold-
// constrained orbit, connected-port ring-move, and click-select — those are not camera
// substance and stay on the current interaction path until the flip phase.

type gesturePhase int

const (
	gestIdle gesturePhase = iota
	gestPending
	gestRotating
	gestDragging
	gestWiring
)

// gestureState is the FSM's owned bookkeeping. Zero value = idle.
type gestureState struct {
	phase gesturePhase

	// pointer-down snapshot + running previous position (client pixels)
	downX, downY float64
	prevX, prevY float64
	button       int

	// empty-space rotation gate + the entity grabbed at pointer-down
	emptyDown bool

	// node-drag target
	dragNode        string
	dragStartCenter vec3

	// wiring source port (unconnected port grabbed at pointer-down)
	wireNode  string
	wirePort  string
	wireInput bool

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
	}
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
	g.phase = gestPending
	g.emptyDown = false
	g.dragNode = ""
	g.wireNode = ""

	switch ev.Hit.Kind {
	case "port":
		node, port, isInput, ok := parseGesturePortId(ev.Hit.Id)
		if !ok {
			return
		}
		if md.portConnected(node, port, isInput) {
			// Connected port → ring-move. Not ported in this additive phase.
			tr.Breadcrumb("gesture-port-move-deferred", node, port, "")
			return
		}
		g.wireNode, g.wirePort, g.wireInput = node, port, isInput
	case "handhold":
		// Handhold-constrained orbit not ported in this additive phase.
		tr.Breadcrumb("gesture-handhold-deferred", ev.Hit.Id, "", "")
	case "node":
		if c, ok := md.centerOfNode(ev.Hit.Id); ok {
			g.dragNode = ev.Hit.Id
			g.dragStartCenter = c
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

	if g.phase == gestPending && dist > gestureMoveSlopPx {
		switch {
		case g.wireNode != "":
			g.phase = gestWiring
		case g.dragNode != "":
			g.phase = gestDragging
		case g.emptyDown:
			g.prevX, g.prevY = ev.X, ev.Y
			g.phase = gestRotating
			// Seed the viewpoint so the orbit pivot is the frozen region-focus (mirrors the
			// TS sendViewpointSet at rotation start). pos/up/r recompute about the new pivot.
			vp := md.vp.viewpoint
			eye := eyeOf(vp)
			r := eye.sub(g.rotPivot).length()
			pos := worldDirToAngles(eye.sub(g.rotPivot))
			md.SetViewpoint(g.rotPivot, r, pos, vp.up)
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
	}
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
		// direction, the intent is an OUT→IN edge. New-edge CREATION requires the loader
		// to rebuild the graph (Go has no in-place add-edge for an arbitrary port pair);
		// a create op only RESTORES a previously-silenced slot. So the FSM resolves the
		// intended edge and, when it maps to a known slot, restores it; otherwise it emits
		// the intent for the (future) create path. Deletion of an existing wire IS fully
		// supported via slotReg. See report.
		if ev.Hit.Kind == "port" {
			tn, tp, ti, ok := parseGesturePortId(ev.Hit.Id)
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
					if pw, found := slotReg[dstNode+"."+dstPort]; found {
						pw.Restore()
					}
				}
			}
		}
	case g.phase == gestDragging:
		md.applyNodeDragTarget(ev) // final target flush
	case g.phase == gestPending:
		// Click. Selection is not Go-owned yet; recognized only. See report.
		tr.Breadcrumb("gesture-click", ev.Hit.Kind, ev.Hit.Id, "")
	}
	g.reset()
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
