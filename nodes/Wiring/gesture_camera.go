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

// ---------------------------------------------------------------------------
// scene geometry (mirrors geometry-helpers.ts contentSphere + interaction-handlers.ts
// regionFocus)
// ---------------------------------------------------------------------------

const gestureFocusMin = 10.0  // FOCUS_MIN — keep the regionFocus pivot off the camera
const gestureMoveSlopPx = 6.0 // MOVE_SLOP_PX — pending → drag/rotate threshold
const gestureZoomBase = 1.01  // ZOOM_BASE — per-scroll-unit dolly factor
const gestureMinDist = 5.0    // MIN_DIST — never let the eye reach the zoom target

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
	zNear := math.Inf(1)
	zFar := math.Inf(-1)
	for _, p := range centers {
		depth := forward.dot(p.sub(eye))
		if math.IsNaN(depth) || math.IsInf(depth, 0) {
			continue
		}
		zNear = math.Min(zNear, depth)
		zFar = math.Max(zFar, depth)
	}
	if math.IsInf(zNear, 1) || math.IsInf(zFar, -1) {
		return eye.add(forward.scale(gestureFocusMin))
	}
	mid := math.Max((zNear+zFar)/2, gestureFocusMin)
	return eye.add(forward.scale(mid))
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
