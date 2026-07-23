package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// viewpoint_ops_test.go — the Zoom/Pan/Orbit viewpoint ops mutate viewpointState's own
// fields correctly; the underlying orbit/zoom/pan math is verified in spherical_test.go /
// viewpoint tests, these assert the op wiring only. The RowEvent/VIEW-frame side of this
// (Decentralized, Step C, memory/feedback_no_single_writer_bridge.md) is a MoveDispatch-level concern,
// covered by TestMoveDispatchViewpointDelegatorsEmit below and viewpoint_bridge_test.go.

// TestZoomViewpointEmitsRadius: ZoomViewpoint scales r.
func TestZoomViewpointEmitsRadius(t *testing.T) {
	tr := T.New(0)
	vp := &viewpointState{}
	vp.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	vp.ZoomViewpoint(0.5, tr)
	if vp.r != 50 {
		t.Fatalf("Zoom(0.5) on r=100: r=%v, want 50", vp.r)
	}
}

// TestPanViewpointEmitsPivot: PanViewpoint slides the pivot.
func TestPanViewpointEmitsPivot(t *testing.T) {
	tr := T.New(0)
	vp := &viewpointState{}
	vp.SetViewpoint(vec3{X: 1, Y: 2, Z: 3}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	vp.PanViewpoint(vec3{X: 10, Y: 0, Z: -3}, tr)
	if vp.pivot.X != 11 || vp.pivot.Y != 2 || vp.pivot.Z != 0 {
		t.Fatalf("Pan: pivot=(%v,%v,%v), want (11,2,0)", vp.pivot.X, vp.pivot.Y, vp.pivot.Z)
	}
}

// TestOrbitViewpointEmitsMovedPos: OrbitViewpoint carries pos from→to and changes the
// pos direction.
func TestOrbitViewpointEmitsMovedPos(t *testing.T) {
	tr := T.New(0)
	vp := &viewpointState{}
	before := dir{Theta: 1.0, Phi: 0.0}
	vp.SetViewpoint(vec3{}, 100, before, dir{Theta: 1.5708})

	vp.OrbitViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.2, Phi: 0.3}, tr)
	if vp.pos.Theta == before.Theta && vp.pos.Phi == before.Phi {
		t.Fatalf("Orbit did not change pos: still (%v,%v)", vp.pos.Theta, vp.pos.Phi)
	}
}

// TestMoveDispatchViewpointDelegatorsEmit: the MoveDispatch delegators (Zoom/Pan/Orbit)
// forward to md.vp and each writes a camera RowEvent onto the VIEW stream.
func TestMoveDispatchViewpointDelegatorsEmit(t *testing.T) {
	tr := T.New(0)
	md := &MoveDispatch{}
	var events []RowEvent
	captureViewFrameKinds(md, &events)
	md.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	md.ZoomViewpoint(0.5, tr)
	md.PanViewpoint(vec3{X: 5}, tr)
	md.OrbitViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.1, Phi: 0.1}, tr)

	if n := countCameraEvents(events); n < 3 {
		t.Fatalf("expected >=3 camera events from delegators, got %d", n)
	}
	// Confirm the final viewpoint state reflects the zoom+pan (r halved, pivot moved).
	if md.vp.r != 50 || md.vp.pivot.X != 5 {
		t.Fatalf("delegator state: r=%v pivot.X=%v, want r=50 pivot.X=5", md.vp.r, md.vp.pivot.X)
	}
}
