package Wiring

import (
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// viewpoint_bridge_test.go — tests for SetViewpoint / OrbitLockedViewpoint integration.
//
// Assertions:
//   (a) OrbitLockedViewpoint emits a camera event each call.
//   (b) SetViewpoint clears the locked axis: nil after set, non-nil after first
//       OrbitLocked call, nil again after another SetViewpoint.

func countCameraEvents(events []T.Event) int {
	n := 0
	for _, e := range events {
		if e.Kind == T.KindCamera {
			n++
		}
	}
	return n
}

func TestOrbitLockedViewpointEmitsCamera(t *testing.T) {
	tr := T.New(64)
	md := &MoveDispatch{}

	// Seed a known camera state.
	md.SetViewpoint(
		vec3{X: 0, Y: 0, Z: 0},
		100,
		dir{Theta: 1.0, Phi: 0.0},
		dir{Theta: 1.5708, Phi: 0.0},
	)

	// First OrbitLockedViewpoint should emit a camera event.
	md.OrbitLockedViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.1, Phi: 0.1}, tr)
	// Second OrbitLockedViewpoint should emit another camera event.
	md.OrbitLockedViewpoint(dir{Theta: 1.1, Phi: 0.1}, dir{Theta: 1.2, Phi: 0.15}, tr)

	time.Sleep(10 * time.Millisecond)
	tr.Close()
	n := countCameraEvents(tr.Events())
	if n < 2 {
		t.Fatalf("expected at least 2 camera events, got %d", n)
	}
}

func TestSetViewpointClearsLock(t *testing.T) {
	tr := T.New(64)
	md := &MoveDispatch{}

	// After SetViewpoint the lock must be nil.
	md.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})
	if md.vp.lockedAxis != nil {
		t.Fatal("lockedAxis should be nil after SetViewpoint")
	}

	// After the first OrbitLocked the lock must be non-nil.
	md.OrbitLockedViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.1, Phi: 0.1}, tr)
	if md.vp.lockedAxis == nil {
		t.Fatal("lockedAxis should be non-nil after first OrbitLockedViewpoint")
	}

	// Another SetViewpoint must clear the lock again.
	md.SetViewpoint(vec3{}, 100, dir{Theta: 1.0}, dir{Theta: 1.5708})
	if md.vp.lockedAxis != nil {
		t.Fatal("lockedAxis should be nil after second SetViewpoint")
	}

	tr.Close()
}
