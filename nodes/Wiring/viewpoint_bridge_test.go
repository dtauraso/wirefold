package Wiring

import (
	"io"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// viewpoint_bridge_test.go — tests for SetViewpoint / OrbitLockedViewpoint integration.
//
// Assertions:
//   (a) OrbitLockedViewpoint writes a camera RowEvent to the VIEW stream each call.
//   (b) SetViewpoint clears the locked axis: nil after set, non-nil after first
//       OrbitLocked call, nil again after another SetViewpoint.

// countCameraEvents counts KindCamera RowEvents.
func countCameraEvents(events []RowEvent) int {
	n := 0
	for _, e := range events {
		if e.Kind == T.KindCamera {
			n++
		}
	}
	return n
}

// captureViewFrameKinds wires md's VIEW stream to a builder that appends every
// RowEvent kind it's handed to *kinds, mirroring what the real buffer builder does
// (Decentralized, Step C, per-owner-buffer-rows.md) without needing a real fd.
func captureViewFrameKinds(md *MoveDispatch, kinds *[]RowEvent) {
	md.SetViewStream(io.Discard, func(tick uint32,
		camPX, camPY, camPZ, camR, camPosTheta, camPosPhi, camUpTheta, camUpPhi float32,
		sceneTori, scenePoles, nodePoles, selSpherePoles, handholds, labelsGlobal, overlaysVis, doubleLinks uint8,
		abcDragCount uint32,
		sceneCX, sceneCY, sceneCZ, sceneRadius float32,
		events []RowEvent,
	) []byte {
		*kinds = append(*kinds, events...)
		return nil
	})
}

func TestOrbitLockedViewpointEmitsCamera(t *testing.T) {
	tr := T.New(0)
	md := &MoveDispatch{}
	var events []RowEvent
	captureViewFrameKinds(md, &events)

	// Seed a known camera state.
	md.SetViewpoint(
		vec3{X: 0, Y: 0, Z: 0},
		100,
		dir{Theta: 1.0, Phi: 0.0},
		dir{Theta: 1.5708, Phi: 0.0},
	)

	// First OrbitLockedViewpoint should write a camera RowEvent.
	md.OrbitLockedViewpoint(dir{Theta: 1.0, Phi: 0.0}, dir{Theta: 1.1, Phi: 0.1}, tr)
	// Second OrbitLockedViewpoint should write another camera RowEvent.
	md.OrbitLockedViewpoint(dir{Theta: 1.1, Phi: 0.1}, dir{Theta: 1.2, Phi: 0.15}, tr)

	if n := countCameraEvents(events); n < 2 {
		t.Fatalf("expected at least 2 camera events, got %d", n)
	}
}

func TestSetViewpointClearsLock(t *testing.T) {
	tr := T.New(0)
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
}
