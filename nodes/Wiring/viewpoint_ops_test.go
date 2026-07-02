package Wiring

import (
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// viewpoint_ops_test.go — the Zoom/Pan/Orbit viewpoint ops and the MoveDispatch thin
// delegators each emit exactly one camera trace event carrying the expected change. The
// underlying orbit/zoom/pan math is verified in spherical_test.go / viewpoint tests; these
// assert the emit wiring only.

// lastCamera returns the last KindCamera event, or fails if none was emitted.
func lastCamera(t *testing.T, tr *T.Trace) T.Event {
	t.Helper()
	tr.Close()
	var last *T.Event
	for i := range tr.Events() {
		e := tr.Events()[i]
		if e.Kind == T.KindCamera {
			last = &e
		}
	}
	if last == nil {
		t.Fatal("no camera event emitted")
	}
	return *last
}

// TestZoomViewpointEmitsRadius: ZoomViewpoint scales r and emits it on a camera event.
func TestZoomViewpointEmitsRadius(t *testing.T) {
	tr := T.New(16)
	vp := &viewpointState{}
	vp.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	vp.ZoomViewpoint(0.5, tr)
	e := lastCamera(t, tr)
	if e.R != 50 {
		t.Fatalf("Zoom(0.5) on r=100: camera event R=%v, want 50", e.R)
	}
}

// TestPanViewpointEmitsPivot: PanViewpoint slides the pivot and emits the new pivot.
func TestPanViewpointEmitsPivot(t *testing.T) {
	tr := T.New(16)
	vp := &viewpointState{}
	vp.SetViewpoint(vec3{X: 1, Y: 2, Z: 3}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	vp.PanViewpoint(vec3{X: 10, Y: 0, Z: -3}, tr)
	e := lastCamera(t, tr)
	if e.PX != 11 || e.PY != 2 || e.PZ != 0 {
		t.Fatalf("Pan: camera pivot=(%v,%v,%v), want (11,2,0)", e.PX, e.PY, e.PZ)
	}
}

// TestOrbitViewpointEmitsMovedPos: OrbitViewpoint carries pos from→to and emits a changed
// pos direction on a camera event.
func TestOrbitViewpointEmitsMovedPos(t *testing.T) {
	tr := T.New(16)
	vp := &viewpointState{}
	before := dir{Theta: 1.0, Phi: 0.0}
	vp.SetViewpoint(vec3{}, 100, before, dir{Theta: 1.5708})

	vp.OrbitViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.2, Phi: 0.3}, tr)
	e := lastCamera(t, tr)
	if e.PosTheta == before.Theta && e.PosPhi == before.Phi {
		t.Fatalf("Orbit did not change pos: still (%v,%v)", e.PosTheta, e.PosPhi)
	}
}

// TestMoveDispatchViewpointDelegatorsEmit: the MoveDispatch delegators (Zoom/Pan/Orbit)
// forward to md.vp and each emits a camera event.
func TestMoveDispatchViewpointDelegatorsEmit(t *testing.T) {
	tr := T.New(64)
	md := &MoveDispatch{}
	md.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})

	md.ZoomViewpoint(0.5, tr)
	md.PanViewpoint(vec3{X: 5}, tr)
	md.OrbitViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.1, Phi: 0.1}, tr)

	if n := countCameraEvents(tr.Events()); n < 3 {
		tr.Close()
		n = countCameraEvents(tr.Events())
		if n < 3 {
			t.Fatalf("expected >=3 camera events from delegators, got %d", n)
		}
	}
	// Confirm the last event reflects the zoom+pan state (r halved, pivot moved).
	e := lastCamera(t, tr)
	if e.R != 50 || e.PX != 5 {
		t.Fatalf("delegator state: R=%v PX=%v, want R=50 PX=5", e.R, e.PX)
	}
}
