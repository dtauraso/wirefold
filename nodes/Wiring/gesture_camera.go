package Wiring

import "math"

// gesture_camera.go — the RENDERER-EDGE Cartesian camera math, ported FORMULA-FOR-FORMULA
// from the TS source so Go's gesture state machine (gesture.go) can turn RAW pointer/wheel
// pixels into the polar viewpoint ops (orbit / zoom / pan) that already live in
// spherical.go + viewpoint.go. This is the SAME quarantined Cartesian the TS side isolates
// in polar.ts (cameraFrame / toWorld / planeSlide) and viewpoint-bridge.ts
// (anglesToWorldOffset / worldDirToAngles); porting it here — instead of reinventing it —
// keeps the hard-won great-circle orbit form and avoids reintroducing the arcball /
// wrong-axis bug class. Every function names the TS source it mirrors, and
// gesture_camera_test.go cross-checks the arithmetic against transcribed TS values.
//
// The great-circle ORBIT itself is NOT re-derived here: this file only produces the two
// world directions (prevDir, currDir) the orbit carries; viewpoint.orbit (spherical.go
// arcBetween/rotateDir) does the rotation. Radius / pan translation are likewise handed to
// viewpoint.zoom / viewpoint.pan.

// ---------------------------------------------------------------------------
// vec3 dot / cross (the only Cartesian rotation-basis ops; kept beside their use)
// ---------------------------------------------------------------------------

func (a vec3) dot(b vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

func (a vec3) cross(b vec3) vec3 {
	return vec3{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

// ---------------------------------------------------------------------------
// angle ↔ world direction (mirrors viewpoint-bridge.ts)
// ---------------------------------------------------------------------------

// anglesToWorldOffset mirrors viewpoint-bridge.ts anglesToWorldOffset:
//
//	x = r*sin(theta)*cos(phi), y = r*cos(theta), z = r*sin(theta)*sin(phi)
func anglesToWorldOffset(r, theta, phi float64) vec3 {
	sinT := math.Sin(theta)
	return vec3{
		X: r * sinT * math.Cos(phi),
		Y: r * math.Cos(theta),
		Z: r * sinT * math.Sin(phi),
	}
}

// worldDirToAngles mirrors polar.ts worldDirToFrameAngles with Y_POLE_FRAME (pole=+y,
// refX=+x, refY=+z), i.e. viewpoint-bridge.ts worldDirToAngles:
//
//	theta = acos(clamp(d.y, -1, 1)); phi = atan2(d.z, d.x)   (d = normalize(v))
func worldDirToAngles(v vec3) dir {
	d := v.normalize()
	return dir{
		Theta: math.Acos(clamp(d.Y, -1, 1)),
		Phi:   math.Atan2(d.Z, d.X),
	}
}

// ---------------------------------------------------------------------------
// camera basis (mirrors CameraFromStore.tsx lookAt + polar.ts cameraFrame)
// ---------------------------------------------------------------------------

// camBasis is the three.js camera screen basis, reconstructed from the polar viewpoint
// (pos = pivot→camera dir; up = up-hint). It reproduces exactly what CameraFromStore.tsx
// builds (cam.up = up; cam.lookAt(pivot)) and what polar.ts cameraFrame then reads off the
// quaternion:
//
//	pole (cam +Z, toward camera) = posWorld = anglesToWorldOffset(1, pos)
//	refX (cam +X, screen right)  = normalize(upWorld × pole)   [three.js Matrix4.lookAt]
//	refY (cam +Y, screen up)     = pole × refX
type camBasis struct {
	refX vec3 // screen right
	refY vec3 // screen up
	pole vec3 // toward the camera (cam +Z)
}

func basisFromViewpoint(pos, up dir) camBasis {
	pole := anglesToWorldOffset(1, pos.Theta, pos.Phi) // unit
	upWorld := anglesToWorldOffset(1, up.Theta, up.Phi).normalize()
	refX := upWorld.cross(pole).normalize()
	refY := pole.cross(refX)
	return camBasis{refX: refX, refY: refY, pole: pole}
}

// eyeOf is the camera world position for a viewpoint: pivot + r * posWorld.
func eyeOf(v viewpoint) vec3 {
	return v.pivot.add(anglesToWorldOffset(v.r, v.pos.Theta, v.pos.Phi))
}

// ---------------------------------------------------------------------------
// screen ↔ sphere (mirrors polar.ts screenToPolar / toWorld)
// ---------------------------------------------------------------------------

// polarDir is a direction on the sphere in a camBasis frame (polar.ts Polar).
type polarDir struct {
	theta float64
	phi   float64
}

// screenToPolar mirrors polar.ts screenToPolar:
//
//	phi = hypot(dx,dy)/scale; theta = atan2(-dy, dx)
func screenToPolar(dxFromCenter, dyFromCenter, scale float64) polarDir {
	return polarDir{
		phi:   math.Hypot(dxFromCenter, dyFromCenter) / scale,
		theta: math.Atan2(-dyFromCenter, dxFromCenter),
	}
}

// toWorldDir mirrors polar.ts toWorld with center=C and radius=1, then .sub(C): it returns
// the UNIT world direction (pole*cos(phi) + equatorDir*sin(phi)) — the .sub(C).normalize()
// in the TS orbit path just recovers this direction, since radius is 1.
//
//	equatorDir = refX*cos(theta) + refY*sin(theta)
//	dir        = pole*cos(phi) + equatorDir*sin(phi)
func toWorldDir(b camBasis, q polarDir) vec3 {
	s := math.Sin(q.phi)
	equator := b.refX.scale(math.Cos(q.theta)).add(b.refY.scale(math.Sin(q.theta)))
	return b.pole.scale(math.Cos(q.phi)).add(equator.scale(s))
}

// planeSlide mirrors polar.ts planeSlide: a polar in-screen-plane slide (r, angle) → a world
// translation along the camera's right/up basis, scaled by worldPerPixel.
func planeSlide(b camBasis, r, angle, worldPerPixel float64) vec3 {
	return b.refX.scale(r * math.Cos(angle) * worldPerPixel).
		add(b.refY.scale(r * math.Sin(angle) * worldPerPixel))
}

// deltaToPolar mirrors polar.ts deltaToPolar: (dx,dy) → (r, angle).
func deltaToPolar(dx, dy float64) (r, angle float64) {
	return math.Hypot(dx, dy), math.Atan2(dy, dx)
}

// panDisplacementPolar builds the lateral pan displacement in the POLAR frame
// (polar-frame-rewrite.md): the mouse drag gives r (distance) and a screen bearing; the
// displacement DIRECTION is a direction 90° off the camera view axis (i.e. in the screen
// plane) at that bearing, derived from the camera's own (θ,φ) and up via the spherical
// toolkit — no cartesian basis vectors. r is the magnitude. The single cartesian is the
// polar2cart at the end, composing the finished displacement for the scene-center move (the
// pointer input boundary). This is locked to the known-correct planeSlide by a unit test.
//
//	psiUp    = bearing of the up-hint about the view axis (azimuthFrom)
//	psiRight = psiUp − π/2 (screen right is a quarter-turn before up, right-handed about pos)
//	dir      = fromAxisFrame(pos, π/2, psiRight + bearing)   // on the view-axis equator
func panDisplacementPolar(pos, up dir, dx, dy, worldPerPixel float64) vec3 {
	r, bearing := deltaToPolar(dx, -dy)
	_, psiUp := azimuthFrom(pos, up)
	d := fromAxisFrame(pos, math.Pi/2, psiUp-math.Pi/2+bearing)
	return polar2cart(polar{R: r * worldPerPixel, Theta: d.Theta, Phi: d.Phi})
}

// ---------------------------------------------------------------------------
// scene geometry (mirrors geometry-helpers.ts contentSphere + interaction-handlers.ts
// regionFocus)
// ---------------------------------------------------------------------------

const gestureFocusMin = 10.0  // FOCUS_MIN — keep the regionFocus pivot off the camera
const gestureMoveSlopPx = 6.0 // MOVE_SLOP_PX — pending → drag/rotate threshold
const gestureZoomBase = 1.01  // ZOOM_BASE — per-scroll-unit dolly factor

// focusAhead returns the orbit center for rotate: a point on the view-center ray at the
// forward-depth of the node the camera is MOST POINTED AT (smallest angle from the view axis,
// in front). Because the point lies on the view axis, orbiting it does NOT re-aim the camera —
// the look direction is unchanged — yet the orbit depth tracks whatever content you have flown
// to and centered (fly to node 10, rotate spins around node 10). Falls back to a fixed distance
// ahead when there is no node in front.
func focusAhead(v viewpoint, centers map[string]vec3) vec3 {
	eye := eyeOf(v)
	forward := anglesToWorldOffset(1, v.pos.Theta, v.pos.Phi).scale(-1) // -pole, unit
	bestCos := -2.0
	depth := 0.0
	found := false
	for _, p := range centers {
		d := p.sub(eye)
		dl := d.length()
		if dl < 1e-9 {
			continue
		}
		cosAng := forward.dot(d) / dl
		if cosAng <= 0 { // behind the camera
			continue
		}
		if cosAng > bestCos { // more centered on the view axis
			bestCos = cosAng
			depth = forward.dot(d)
			found = true
		}
	}
	if !found {
		return eye.add(forward.scale(gestureFocusMin))
	}
	return eye.add(forward.scale(math.Max(depth, gestureFocusMin)))
}

// contentSphereOf mirrors geometry-helpers.ts contentSphere over the given node centers:
// center = bbox midpoint, radius = max(center-distance)*1.1 (min 1). Empty → (origin, 100).
func contentSphereOf(centers map[string]vec3) (center vec3, radius float64) {
	if len(centers) == 0 {
		return vec3{}, 100
	}
	min := vec3{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)}
	max := vec3{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}
	for _, p := range centers {
		if math.IsInf(p.X, 0) || math.IsNaN(p.X) {
			continue
		}
		min.X, max.X = math.Min(min.X, p.X), math.Max(max.X, p.X)
		min.Y, max.Y = math.Min(min.Y, p.Y), math.Max(max.Y, p.Y)
		min.Z, max.Z = math.Min(min.Z, p.Z), math.Max(max.Z, p.Z)
	}
	center = min.add(max).scale(0.5)
	r := 0.0
	for _, p := range centers {
		r = math.Max(r, p.sub(center).length())
	}
	return center, math.Max(r*1.1, 1)
}

// regionFocus mirrors interaction-handlers.ts regionFocus: the center of the node depth slab
// straight ahead of the camera. forward = -pole (camera looks along -Z); depth of each node
// = forward · (p - eye); pivot = eye + forward * max((zNear+zFar)/2, FOCUS_MIN). Falls back
// to eye + forward*FOCUS_MIN when there are no finite node depths.
func regionFocus(v viewpoint, centers map[string]vec3) vec3 {
	eye := eyeOf(v)
	forward := anglesToWorldOffset(1, v.pos.Theta, v.pos.Phi).scale(-1) // -pole, unit
	// Pivot on the view axis at the depth of the NEAREST node (smallest forward-depth), not the
	// whole-scene depth MIDPOINT. The midpoint sat between the near node you zoomed into and the
	// far ones, so rotate/pan operated around a distant point and swung/overshot from up close.
	// Using the nearest depth puts the pivot on what you dollied toward while staying on the
	// screen-center ray (deterministic — no node-tie ambiguity). Falls back straight ahead when
	// there are no nodes.
	zNear := math.Inf(1)
	for _, p := range centers {
		depth := forward.dot(p.sub(eye))
		if math.IsNaN(depth) || math.IsInf(depth, 0) {
			continue
		}
		zNear = math.Min(zNear, depth)
	}
	if math.IsInf(zNear, 1) {
		return eye.add(forward.scale(gestureFocusMin))
	}
	return eye.add(forward.scale(math.Max(zNear, gestureFocusMin)))
}

// ---------------------------------------------------------------------------
// home fit (mirrors camera-ui.tsx HomeButton.onClick + geometry-helpers.ts
// boundingBox3D / fitDistance) — frame all node bodies square-on.
// ---------------------------------------------------------------------------

// fitDistanceGo mirrors geometry-helpers.ts fitDistance: how far along the view axis to
// place the camera so a width×height world view fills the viewport at (fovDeg, aspect):
//
//	d = max(height/2, width/2/aspect) / tan(fov/2)
func fitDistanceGo(fovDeg, aspect, width, height float64) float64 {
	fovRad := fovDeg * math.Pi / 180
	halfTan := math.Tan(fovRad / 2)
	return math.Max(height/2, width/2/aspect) / halfTan
}

// homeFitPose ports camera-ui.tsx HomeButton.onClick FORMULA-FAITHFULLY: build the AABB over
// node centers ± body radius (geometry-helpers.ts boundingBox3D), place the camera square-on
// in front of the content plane along +z with +y up, at a padded fit distance:
//
//	dist       = fitDistance(fov, aspect, sizeX, sizeY) + sizeZ/2
//	paddedDist = dist * 1.2
//	pivot = bbox center; r = paddedDist; pos = dir(+z); up = dir(+y)
//
// centers maps node id → world center; radius maps node id → body sphere radius. Returns
// ok=false when there are no nodes (HomeButton returns early in that case). pos/up come from
// worldDirToAngles so the resulting eye = pivot + r·(+z) and screen-up = +y, matching the
// TS cam.position / cam.up / cam.lookAt(center).
func homeFitPose(centers map[string]vec3, radius map[string]float64, fovDeg, aspect float64) (pivot vec3, r float64, pos, up dir, ok bool) {
	if len(centers) == 0 {
		return vec3{}, 0, dir{}, dir{}, false
	}
	minX, minY, minZ := math.Inf(1), math.Inf(1), math.Inf(1)
	maxX, maxY, maxZ := math.Inf(-1), math.Inf(-1), math.Inf(-1)
	for id, p := range centers {
		rad := radius[id]
		minX, maxX = math.Min(minX, p.X-rad), math.Max(maxX, p.X+rad)
		minY, maxY = math.Min(minY, p.Y-rad), math.Max(maxY, p.Y+rad)
		minZ, maxZ = math.Min(minZ, p.Z-rad), math.Max(maxZ, p.Z+rad)
	}
	center := vec3{X: (minX + maxX) / 2, Y: (minY + maxY) / 2, Z: (minZ + maxZ) / 2}
	sizeX, sizeY, sizeZ := maxX-minX, maxY-minY, maxZ-minZ
	dist := fitDistanceGo(fovDeg, aspect, sizeX, sizeY) + sizeZ/2
	paddedDist := dist * 1.2
	pos = worldDirToAngles(vec3{X: 0, Y: 0, Z: 1})
	up = worldDirToAngles(vec3{X: 0, Y: 1, Z: 0})
	return center, paddedDist, pos, up, true
}

// ---------------------------------------------------------------------------
// projection (mirrors THREE.Vector3.project for the perspective camera) — used ONLY by
// zoom-to-cursor to find the node nearest the pointer in NDC. Small NDC error only changes
// which node is picked (the dolly is floored so it never reaches the target).
// ---------------------------------------------------------------------------

// projectNDC returns (ndcX, ndcY, inFront) for a world point p under the camera described by
// (basis b, eye, fov degrees, aspect = rectWidth/rectHeight). inFront is false when p is on
// or behind the camera plane (three.js ndc.z > 1 skip).
func projectNDC(p, eye vec3, b camBasis, fovDeg, aspect float64) (ndcX, ndcY float64, inFront bool) {
	rel := p.sub(eye)
	cx := rel.dot(b.refX)
	cy := rel.dot(b.refY)
	cz := rel.dot(b.pole) // +Z toward camera; a point in front has cz < 0
	if cz >= 0 {
		return 0, 0, false
	}
	tanHalf := math.Tan((fovDeg * math.Pi / 180) / 2)
	ndcX = cx / (-cz) / (tanHalf * aspect)
	ndcY = cy / (-cz) / tanHalf
	return ndcX, ndcY, true
}

// rayDirThroughNDC mirrors THREE.Raycaster.setFromCamera for a perspective camera: the world
// ray direction from the eye through NDC (nx, ny). camera-space dir = (nx*tanHalf*aspect,
// ny*tanHalf, -1), rotated into world by the basis (pole is +Z, so -1 along Z faces forward).
func rayDirThroughNDC(nx, ny float64, b camBasis, fovDeg, aspect float64) vec3 {
	tanHalf := math.Tan((fovDeg * math.Pi / 180) / 2)
	camDir := b.refX.scale(nx * tanHalf * aspect).
		add(b.refY.scale(ny * tanHalf)).
		add(b.pole.scale(-1))
	return camDir.normalize()
}
