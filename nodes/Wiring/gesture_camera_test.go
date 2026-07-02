package Wiring

import (
	"math"
	"testing"
)

// gesture_camera_test.go — CROSS-CHECK the Go camera-math port (gesture_camera.go) against
// the TS formulas it mirrors. Each test either (a) hand-transcribes the TS arithmetic to an
// independent expected value, or (b) checks a hardcoded oracle case computed by hand from the
// TS source. This guards against reintroducing the arcball / wrong-axis bug class by pinning
// the ported pixel→world-direction pipeline to the TS values.

func vecClose(a, b vec3, tol float64) bool {
	return math.Abs(a.X-b.X) < tol && math.Abs(a.Y-b.Y) < tol && math.Abs(a.Z-b.Z) < tol
}

// anglesToWorldOffset must equal production polar2cart (the same formula the renderer uses),
// and the TS viewpoint-bridge.ts anglesToWorldOffset.
func TestGestureAnglesToWorldOffsetMatchesPolar2Cart(t *testing.T) {
	cases := []struct{ r, th, ph float64 }{
		{1, math.Pi / 2, math.Pi / 2}, {5, 0, 1.234}, {3, math.Pi / 2, 0}, {2, 1.1, -0.7},
	}
	for _, c := range cases {
		got := anglesToWorldOffset(c.r, c.th, c.ph)
		want := polar2cart(polar{R: c.r, Theta: c.th, Phi: c.ph})
		if !vecClose(got, want, 1e-12) {
			t.Fatalf("anglesToWorldOffset(%v)=%v want %v", c, got, want)
		}
	}
}

// worldDirToAngles: hand oracle. dir +Z → theta=acos(0)=π/2, phi=atan2(1,0)=π/2.
func TestGestureWorldDirToAngles(t *testing.T) {
	d := worldDirToAngles(vec3{0, 0, 1})
	if math.Abs(d.Theta-math.Pi/2) > 1e-12 || math.Abs(d.Phi-math.Pi/2) > 1e-12 {
		t.Fatalf("+Z → %v want {π/2, π/2}", d)
	}
	// Round-trip: anglesToWorldOffset(1, θ, φ) then back must recover (θ, φ).
	in := dir{Theta: 1.0, Phi: -0.5}
	back := worldDirToAngles(anglesToWorldOffset(1, in.Theta, in.Phi))
	if math.Abs(back.Theta-in.Theta) > 1e-9 || math.Abs(back.Phi-in.Phi) > 1e-9 {
		t.Fatalf("round-trip: %v → %v", in, back)
	}
}

// basisFromViewpoint hardcoded oracle: camera at +Z (pos=+Z), up=+Y →
// three.js lookAt basis refX=+X, refY=+Y, pole=+Z.
func TestGestureBasisOracle(t *testing.T) {
	pos := dir{Theta: math.Pi / 2, Phi: math.Pi / 2} // +Z
	up := dir{Theta: 0, Phi: 0}                      // +Y
	b := basisFromViewpoint(pos, up)
	if !vecClose(b.pole, vec3{0, 0, 1}, 1e-12) {
		t.Fatalf("pole=%v want +Z", b.pole)
	}
	if !vecClose(b.refX, vec3{1, 0, 0}, 1e-12) {
		t.Fatalf("refX=%v want +X", b.refX)
	}
	if !vecClose(b.refY, vec3{0, 1, 0}, 1e-12) {
		t.Fatalf("refY=%v want +Y", b.refY)
	}
}

// basisFromViewpoint must be orthonormal (right-handed) for random viewpoints — the
// property the TS cameraFrame relies on (quaternion basis).
func TestGestureBasisOrthonormal(t *testing.T) {
	poses := []dir{{0.3, 0.7}, {1.9, -2.1}, {math.Pi / 2, 0}, {2.4, 1.1}}
	ups := []dir{{1.1, 0.2}, {0.4, 2.9}, {0, 0}, {1.5, -1.2}}
	for i := range poses {
		b := basisFromViewpoint(poses[i], ups[i])
		for _, v := range []vec3{b.refX, b.refY, b.pole} {
			if math.Abs(v.length()-1) > 1e-9 {
				t.Fatalf("basis vector not unit: %v (|v|=%v)", v, v.length())
			}
		}
		if math.Abs(b.refX.dot(b.pole)) > 1e-9 || math.Abs(b.refX.dot(b.refY)) > 1e-9 || math.Abs(b.refY.dot(b.pole)) > 1e-9 {
			t.Fatalf("basis not orthogonal: %+v", b)
		}
		// refY == pole × refX (right-handed)
		if !vecClose(b.refY, b.pole.cross(b.refX), 1e-9) {
			t.Fatalf("not right-handed: refY=%v pole×refX=%v", b.refY, b.pole.cross(b.refX))
		}
	}
}

// screenToPolar + toWorldDir hand oracles in the canonical (+X,+Y,+Z) frame.
func TestGestureScreenToWorldOracle(t *testing.T) {
	b := camBasis{refX: vec3{1, 0, 0}, refY: vec3{0, 1, 0}, pole: vec3{0, 0, 1}}
	// cursor exactly at center → pole direction (+Z, toward camera).
	center := toWorldDir(b, screenToPolar(0, 0, 100))
	if !vecClose(center, vec3{0, 0, 1}, 1e-12) {
		t.Fatalf("center cursor → %v want +Z", center)
	}
	// cursor one scale-unit to the RIGHT: dx=scale, dy=0 → phi=1, theta=atan2(0,1)=0 →
	// equator=+X, dir=(sin1, 0, cos1).
	right := toWorldDir(b, screenToPolar(100, 0, 100))
	want := vec3{math.Sin(1), 0, math.Cos(1)}
	if !vecClose(right, want, 1e-12) {
		t.Fatalf("right cursor → %v want %v", right, want)
	}
	// cursor one scale-unit UP: screen dy is negative up in client coords; the handler
	// passes (y - cy). A point ABOVE center has dyFromCenter<0. theta=atan2(-(-1),0)=π/2 →
	// equator=+Y, dir=(0, sin1, cos1).
	up := toWorldDir(b, screenToPolar(0, -100, 100))
	wantUp := vec3{0, math.Sin(1), math.Cos(1)}
	if !vecClose(up, wantUp, 1e-12) {
		t.Fatalf("up cursor → %v want %v", up, wantUp)
	}
}

// planeSlide hand oracle: refX=+X, refY=+Y; (r=2, angle=0, wpp=3) → (6,0,0).
func TestGesturePlaneSlideOracle(t *testing.T) {
	b := camBasis{refX: vec3{1, 0, 0}, refY: vec3{0, 1, 0}, pole: vec3{0, 0, 1}}
	got := planeSlide(b, 2, 0, 3)
	if !vecClose(got, vec3{6, 0, 0}, 1e-12) {
		t.Fatalf("planeSlide=%v want (6,0,0)", got)
	}
	got = planeSlide(b, 2, math.Pi/2, 3)
	if !vecClose(got, vec3{0, 6, 0}, 1e-9) {
		t.Fatalf("planeSlide(π/2)=%v want (0,6,0)", got)
	}
}

func TestGestureDeltaToPolar(t *testing.T) {
	r, a := deltaToPolar(3, 4)
	if math.Abs(r-5) > 1e-12 || math.Abs(a-math.Atan2(4, 3)) > 1e-12 {
		t.Fatalf("deltaToPolar(3,4)=(%v,%v)", r, a)
	}
}

// contentSphereOf hand oracle: centers (0,0,0),(10,0,0) → center (5,0,0), radius 5*1.1=5.5.
func TestGestureContentSphereOracle(t *testing.T) {
	c, r := contentSphereOf(map[string]vec3{"a": {0, 0, 0}, "b": {10, 0, 0}})
	if !vecClose(c, vec3{5, 0, 0}, 1e-12) || math.Abs(r-5.5) > 1e-12 {
		t.Fatalf("contentSphere=%v r=%v want (5,0,0) 5.5", c, r)
	}
	c, r = contentSphereOf(nil)
	if !vecClose(c, vec3{}, 1e-12) || r != 100 {
		t.Fatalf("empty contentSphere=%v r=%v want origin 100", c, r)
	}
}

// regionFocus hand oracle: empty centers → eye + forward*FOCUS_MIN. Camera at +Z, r=100:
// eye=(0,0,100), forward=-pole=(0,0,-1) → focus=(0,0,90).
func TestGestureRegionFocusEmpty(t *testing.T) {
	v := viewpoint{pivot: vec3{0, 0, 0}, r: 100, pos: dir{Theta: math.Pi / 2, Phi: math.Pi / 2}, up: dir{0, 0}}
	f := regionFocus(v, nil)
	if !vecClose(f, vec3{0, 0, 90}, 1e-9) {
		t.Fatalf("regionFocus(empty)=%v want (0,0,90)", f)
	}
}

// projectNDC hand oracle: point at origin, camera at +Z looking at origin → NDC (0,0), inFront.
func TestGestureProjectNDCOracle(t *testing.T) {
	v := viewpoint{pivot: vec3{0, 0, 0}, r: 100, pos: dir{Theta: math.Pi / 2, Phi: math.Pi / 2}, up: dir{0, 0}}
	b := basisFromViewpoint(v.pos, v.up)
	nx, ny, inFront := projectNDC(vec3{0, 0, 0}, eyeOf(v), b, 50, 800.0/600.0)
	if !inFront || math.Abs(nx) > 1e-9 || math.Abs(ny) > 1e-9 {
		t.Fatalf("projectNDC(origin)=(%v,%v,inFront=%v) want (0,0,true)", nx, ny, inFront)
	}
	// A point behind the camera (further +Z than the eye) is not in front.
	_, _, inFront2 := projectNDC(vec3{0, 0, 200}, eyeOf(v), b, 50, 800.0/600.0)
	if inFront2 {
		t.Fatalf("point behind camera reported inFront")
	}
}
