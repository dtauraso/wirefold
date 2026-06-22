package Wiring

import (
	"encoding/json"
	"math"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// buildViewpointFixture returns a minimal MoveDispatch (no nodes/edges) with a fresh
// Trace, ready for viewpoint op tests.
func buildViewpointFixture() (*MoveDispatch, *T.Trace) {
	tr := T.New(256)
	md := newMoveDispatch(map[string]nodeGeom{}, map[string]EdgeEndpoints{}, tr)
	return md, tr
}

// cameraEvents filters the trace to camera-kind events.
func cameraEvents(tr *T.Trace) []T.Event {
	tr.Close()
	var out []T.Event
	for _, e := range tr.Events() {
		if e.Kind == T.KindCamera {
			out = append(out, e)
		}
	}
	return out
}

func TestSetViewpointEmitsCamera(t *testing.T) {
	md, tr := buildViewpointFixture()
	pivot := vec3{X: 1, Y: 2, Z: 3}
	r := 10.0
	pos := dir{Theta: math.Pi / 4, Phi: math.Pi / 6}
	up := dir{Theta: math.Pi / 2, Phi: 0}

	md.SetViewpoint(pivot, r, pos, up)
	md.EmitViewpoint(tr)

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event, got %d", len(evs))
	}
	e := evs[0]
	const eps = 1e-12
	checkFloat(t, "px", e.PX, pivot.X, eps)
	checkFloat(t, "py", e.PY, pivot.Y, eps)
	checkFloat(t, "pz", e.PZ, pivot.Z, eps)
	checkFloat(t, "r", e.R, r, eps)
	checkFloat(t, "posTheta", e.PosTheta, pos.Theta, eps)
	checkFloat(t, "posPhi", e.PosPhi, pos.Phi, eps)
	checkFloat(t, "upTheta", e.UpTheta, up.Theta, eps)
	checkFloat(t, "upPhi", e.UpPhi, up.Phi, eps)
}

func TestOrbitViewpointEmitsCamera(t *testing.T) {
	md, tr := buildViewpointFixture()
	// Place camera at north pole looking south, up = +x direction.
	posStart := dir{Theta: 0, Phi: 0} // +y axis
	upStart := dir{Theta: math.Pi / 2, Phi: 0}
	md.SetViewpoint(vec3{}, 15.0, posStart, upStart)

	from := dir{Theta: math.Pi / 4, Phi: 0}
	to := dir{Theta: math.Pi / 4, Phi: math.Pi / 4}
	md.OrbitViewpoint(from, to, tr)

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event after orbit, got %d", len(evs))
	}
	// r unchanged by orbit
	if math.Abs(evs[0].R-15.0) > 1e-12 {
		t.Errorf("orbit changed r: got %v, want 15.0", evs[0].R)
	}
}

func TestZoomViewpointEmitsCamera(t *testing.T) {
	md, tr := buildViewpointFixture()
	md.SetViewpoint(vec3{}, 20.0, dir{Theta: math.Pi / 2, Phi: 0}, dir{Theta: 0, Phi: 0})
	md.ZoomViewpoint(0.5, tr)

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event after zoom, got %d", len(evs))
	}
	if math.Abs(evs[0].R-10.0) > 1e-12 {
		t.Errorf("zoom r: got %v, want 10.0", evs[0].R)
	}
}

func TestZoomViewpointFloor(t *testing.T) {
	md, tr := buildViewpointFixture()
	md.SetViewpoint(vec3{}, 8.0, dir{Theta: math.Pi / 2, Phi: 0}, dir{Theta: 0, Phi: 0})
	// factor that would put r below viewpointMinDist
	md.ZoomViewpoint(0.1, tr)

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event, got %d", len(evs))
	}
	if evs[0].R < viewpointMinDist {
		t.Errorf("zoom floor: r=%v below min %v", evs[0].R, viewpointMinDist)
	}
}

func TestPanViewpointEmitsCamera(t *testing.T) {
	md, tr := buildViewpointFixture()
	md.SetViewpoint(vec3{X: 0, Y: 0, Z: 0}, 10.0, dir{Theta: math.Pi / 2, Phi: 0}, dir{Theta: 0, Phi: 0})
	md.PanViewpoint(vec3{X: 3, Y: 4, Z: 5}, tr)

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event after pan, got %d", len(evs))
	}
	const eps = 1e-12
	checkFloat(t, "pan px", evs[0].PX, 3, eps)
	checkFloat(t, "pan py", evs[0].PY, 4, eps)
	checkFloat(t, "pan pz", evs[0].PZ, 5, eps)
}

// TestViewpointEditJSONRoundTrip drives a full edit message through JSON → applyEdit,
// exercising the wire format (not just the methods). Guards the struct-tag class: every
// field — including the φ / Y / Z components — must round-trip, not collapse to zero.
func TestViewpointEditJSONRoundTrip(t *testing.T) {
	const line = `{"type":"edit","op":"viewpoint","viewpoint":{` +
		`"kind":"set","pivotX":1,"pivotY":2,"pivotZ":3,"r":42,` +
		`"posTheta":0.5,"posPhi":0.6,"upTheta":0.7,"upPhi":0.8}}`
	var msg stdinMsg
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	md, tr := buildViewpointFixture()
	applyEdit(msg, SlotRegistry{}, md, tr, "")

	evs := cameraEvents(tr)
	if len(evs) != 1 {
		t.Fatalf("expected 1 camera event, got %d", len(evs))
	}
	e := evs[0]
	const eps = 1e-12
	// Every distinct field must arrive — a shared json tag would zero the second of a pair.
	checkFloat(t, "px", e.PX, 1, eps)
	checkFloat(t, "py", e.PY, 2, eps)
	checkFloat(t, "pz", e.PZ, 3, eps)
	checkFloat(t, "r", e.R, 42, eps)
	checkFloat(t, "posTheta", e.PosTheta, 0.5, eps)
	checkFloat(t, "posPhi", e.PosPhi, 0.6, eps)
	checkFloat(t, "upTheta", e.UpTheta, 0.7, eps)
	checkFloat(t, "upPhi", e.UpPhi, 0.8, eps)
}

// checkFloat is a helper used by viewpoint tests (and is package-local so it won't
// conflict if another test file defines the same helper under a different name).
func checkFloat(t *testing.T, label string, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}
