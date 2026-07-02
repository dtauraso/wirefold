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
	// region-focus pivot (empty centers): eye=(0,0,100), forward=(0,0,-1) → (0,0,90).
	if !vecClose(md.gest.rotPivot, vec3{0, 0, 90}, 1e-9) {
		t.Fatalf("rotPivot=%v want (0,0,90)", md.gest.rotPivot)
	}

	// First move past the slop: transitions to rotating and seeds the viewpoint. The first
	// frame's arc is ~zero (prev==curr), so pose is essentially the seeded one.
	md.HandleRawInput(rawEvent("pointermove", 420, 300), nil, nil)
	if md.gest.phase != gestRotating {
		t.Fatalf("after slop-cross move: phase=%v want rotating", md.gest.phase)
	}
	if !vecClose(md.vp.pivot, vec3{0, 0, 90}, 1e-9) {
		t.Fatalf("seed pivot=%v want region-focus (0,0,90)", md.vp.pivot)
	}
	if math.Abs(md.vp.r-10) > 1e-9 {
		t.Fatalf("seed r=%v want 10", md.vp.r)
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
func TestGestureWheelPansPivot(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	before := md.vp.pivot
	ev := rawEvent("wheel", 400, 300)
	ev.DeltaX = 10
	ev.DeltaY = 0
	md.HandleRawInput(ev, nil, nil)
	// worldPerPixel>0 and deltaX=10 → nonzero screen-right slide of the pivot.
	if vecClose(md.vp.pivot, before, 1e-9) {
		t.Fatalf("plain wheel did not pan pivot (stayed %v)", md.vp.pivot)
	}
}

func TestGestureCtrlWheelDolliesPivot(t *testing.T) {
	md := newGestureMD(canonicalViewpoint())
	ev := rawEvent("wheel", 400, 300)
	ev.Ctrl = true
	ev.DeltaY = 1
	md.HandleRawInput(ev, nil, nil)
	// Empty centers → target=regionFocus=(0,0,90); eye=(0,0,100); toP=(0,0,-10);
	// factor=1.01^1≈1.01 (distP*factor=10.1>MIN_DIST); delta=toP*(1-factor)=(0,0,0.1).
	// Seed pivot=(0,0,90), then pan(delta) → (0,0,90.1).
	wantZ := 90 + (-10)*(1-math.Pow(gestureZoomBase, 1))
	if math.Abs(md.vp.pivot.Z-wantZ) > 1e-9 || math.Abs(md.vp.pivot.X) > 1e-9 {
		t.Fatalf("ctrl-wheel pivot=%v want Z≈%v", md.vp.pivot, wantZ)
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
