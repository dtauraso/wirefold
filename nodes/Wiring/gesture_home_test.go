package Wiring

import (
	"math"
	"testing"
)

// gesture_home_test.go — the "home" (fit-to-content) command: Go frames all nodes from its
// OWN geometry (homeFitPose, ported from camera-ui.tsx HomeButton) and installs the pose in
// the gesture FSM via SetViewpoint + EmitViewpoint. The regression this guards is snap-back:
// before the command existed, TS mutated the three.js camera directly and never told Go, so
// md.vp held a stale pose and the first gesture reset to it. Here the FSM's own pose becomes
// the framed pose, so a subsequent orbit builds on it.

// homeMD builds a MoveDispatch whose nodeMovers carry the given centers, all of kind Hold
// (Width==Height==60 → body radius 60/CurveParamNodeRadiusDivisor). The atomic snap is
// seeded so heldCenters() observes each center, mirroring a live post-layout dispatch.
func homeMD(v viewpoint, centers map[string]vec3) *MoveDispatch {
	md := &MoveDispatch{nodeMovers: map[string]*nodeMover{}}
	md.vp.viewpoint = v
	for id, c := range centers {
		cc := c
		nm := &nodeMover{id: id, geom: nodeGeom{Kind: "Hold", Center: &cc}}
		nm.snap.Store(&centerSnap{c: cc})
		md.nodeMovers[id] = nm
	}
	return md
}

func TestGestureHomeComputesFitPoseFromGeometry(t *testing.T) {
	// A deliberately stale/off pose: the pre-home viewpoint the FSM would otherwise reuse.
	stale := viewpoint{pivot: vec3{500, 500, 500}, r: 999, pos: dir{Theta: 0.3, Phi: 0.7}, up: dir{Theta: 0.1, Phi: 0.2}}
	centers := map[string]vec3{
		"a": {X: -30, Y: 0, Z: 0},
		"b": {X: 30, Y: 0, Z: 0},
	}
	md := homeMD(stale, centers)
	// Match the raw "home" event TS sends: fov + aspect encoded as rectWidth/rectHeight.
	const fov, aspect = 50.0, 800.0 / 600.0
	ev := rawInputMsg{Kind: "home", Fov: fov, RectWidth: aspect, RectHeight: 1}

	md.HandleRawInput(ev, nil, nil)

	// Expected fit pose: bbox over centers ± body radius (min(60,60)/divisor).
	rad := 60.0 / CurveParamNodeRadiusDivisor
	sizeX := (30 + rad) - (-30 - rad) // 60 + 2*rad
	sizeY := (0 + rad) - (0 - rad)    // 2*rad
	sizeZ := (0 + rad) - (0 - rad)    // 2*rad
	wantDist := fitDistanceGo(fov, aspect, sizeX, sizeY) + sizeZ/2
	wantR := wantDist * 1.2

	if !vecClose(md.vp.pivot, vec3{0, 0, 0}, 1e-9) {
		t.Fatalf("home pivot=%v want content center (0,0,0)", md.vp.pivot)
	}
	if math.Abs(md.vp.r-wantR) > 1e-9 {
		t.Fatalf("home r=%v want padded fit distance %v", md.vp.r, wantR)
	}
	// Square-on: camera along +z, up +y — eye = pivot + r·(+z).
	eye := eyeOf(md.vp.viewpoint)
	if !vecClose(eye, vec3{0, 0, wantR}, 1e-6) {
		t.Fatalf("home eye=%v want (0,0,%v) (square-on +z)", eye, wantR)
	}
	if math.Abs(md.vp.pos.Theta-math.Pi/2) > 1e-9 || math.Abs(md.vp.pos.Phi-math.Pi/2) > 1e-9 {
		t.Fatalf("home pos=%v want (+z) theta=pi/2 phi=pi/2", md.vp.pos)
	}
	if md.vp.lockedAxis != nil {
		t.Fatalf("home must clear any locked axis, got %v", *md.vp.lockedAxis)
	}
}

// An unknown-kind node must be framed at the SAME body radius the pre-branch used
// (geometry-helpers.ts nodeRadius → the streamed radius the buffer renders), i.e. the
// (110,60) default → min(110,60)/CurveParamNodeRadiusDivisor, NOT a zero-size point. This
// locks the home-fit radius to the pre-branch so an unsized node is not cut off at the frame
// edge.
func TestGestureHomeFramesUnknownKindAtRenderRadius(t *testing.T) {
	stale := viewpoint{pivot: vec3{500, 500, 500}, r: 999, pos: dir{Theta: 0.3, Phi: 0.7}, up: dir{Theta: 0.1, Phi: 0.2}}
	// A single node of an unknown kind at the origin. homeMD seeds kind "Hold"; override
	// the mover's kind to an unrecognized one so nodeBodyRadius takes the (110,60) fallback.
	centers := map[string]vec3{"x": {X: 0, Y: 0, Z: 0}}
	md := homeMD(stale, centers)
	md.nodeMovers["x"].geom.Kind = "NotAKind"

	const fov, aspect = 50.0, 800.0 / 600.0
	md.HandleRawInput(rawInputMsg{Kind: "home", Fov: fov, RectWidth: aspect, RectHeight: 1}, nil, nil)

	// Expected: bbox is ±renderRadius on every axis (single node at origin).
	renderRadius := 60.0 / float64(CurveParamNodeRadiusDivisor) // min(110,60)/4 = 15
	if renderRadius <= 0 {
		t.Fatalf("render radius must be positive, got %v", renderRadius)
	}
	size := 2 * renderRadius
	wantDist := fitDistanceGo(fov, aspect, size, size) + size/2
	wantR := wantDist * 1.2
	if math.Abs(md.vp.r-wantR) > 1e-9 {
		t.Fatalf("home r=%v want %v (unknown kind framed at render radius %v, not 0)", md.vp.r, wantR, renderRadius)
	}
}

// After home, a subsequent empty-space drag orbits about the HOME-derived region focus at the
// HOME radius — it does NOT reset to the pre-home (stale) pose. This is the anti-snap-back
// invariant: the FSM's own pose is the seed for the next gesture.
func TestGestureHomeThenOrbitBuildsOnHomePose(t *testing.T) {
	stale := viewpoint{pivot: vec3{500, 500, 500}, r: 999, pos: dir{Theta: 0.3, Phi: 0.7}, up: dir{Theta: 0.1, Phi: 0.2}}
	centers := map[string]vec3{
		"a": {X: -30, Y: 0, Z: 0},
		"b": {X: 30, Y: 0, Z: 0},
	}
	md := homeMD(stale, centers)
	const fov, aspect = 50.0, 800.0 / 600.0
	md.HandleRawInput(rawInputMsg{Kind: "home", Fov: fov, RectWidth: aspect, RectHeight: 1}, nil, nil)

	homePivot, homeR, homePos := md.vp.pivot, md.vp.r, md.vp.pos

	// Empty-space drag: pointerdown → move past slop (seeds region-focus orbit) → move (orbit).
	raw := func(kind string, x, y float64) rawInputMsg {
		return rawInputMsg{Kind: kind, X: x, Y: y, RectLeft: 0, RectTop: 0, RectWidth: 800, RectHeight: 600, Button: 0, Fov: fov, Hit: rawHit{Kind: "empty"}}
	}
	md.HandleRawInput(raw("pointerdown", 400, 300), nil, nil)
	md.HandleRawInput(raw("pointermove", 420, 300), nil, nil) // slop-cross → seed orbit
	md.HandleRawInput(raw("pointermove", 480, 320), nil, nil) // genuine orbit

	// The orbit seeded from the HOME pose: both node centers sit at z=0, so the region focus
	// (depth-slab midpoint straight ahead) is the content center and the seed radius is the
	// home radius. A rigid orbit preserves radius, so r must still be the HOME radius — NOT
	// the stale r=999.
	if math.Abs(md.vp.r-homeR) > 1e-6 {
		t.Fatalf("after home+orbit r=%v want home radius %v (stale was 999)", md.vp.r, homeR)
	}
	if !vecClose(md.vp.pivot, homePivot, 1e-6) {
		t.Fatalf("after home+orbit pivot=%v want home pivot %v (stale was (500,500,500))", md.vp.pivot, homePivot)
	}
	if math.Abs(md.vp.r-999) < 1.0 {
		t.Fatalf("after home+orbit r reset toward stale 999: %v", md.vp.r)
	}
	// The orbit actually rotated the camera off the square-on home direction.
	if math.Abs(md.vp.pos.Theta-homePos.Theta) < 1e-6 && math.Abs(md.vp.pos.Phi-homePos.Phi) < 1e-6 {
		t.Fatalf("orbit did not change pos from home %v", homePos)
	}
}
