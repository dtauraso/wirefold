package Wiring

// viewpoint_state.go — viewpointState owns the polar camera viewpoint value plus its
// set/orbit/zoom/pan mutators and the camera-trace emit. It is owned as a field by
// MoveDispatch (md.vp), which exposes thin delegating methods; extracting it here keeps
// node_move.go focused on the dispatch registry. There is no goroutine — callers
// serialize externally (the stdin reader runs in a single goroutine).
//
// The viewpoint value is embedded so callers that reach through the field (e.g. tests
// asserting md.vp.lockedAxis) keep resolving, and the *viewpoint navigation ops
// (orbit/orbitLocked/zoom/pan) promote onto viewpointState.

import (
	T "github.com/dtauraso/wirefold/Trace"
)

// viewpointState carries the camera viewpoint and its emit/navigation methods.
type viewpointState struct {
	viewpoint
}

// SetViewpoint installs a known camera state without emitting. Used by the "set"
// viewpoint op to seed the viewpoint from persisted or initial values, followed by
// EmitViewpoint to broadcast it. Also clears any locked rotation axis from a prior
// handhold gesture so the next gesture starts fresh.
func (v *viewpointState) SetViewpoint(pivot vec3, r float64, pos, up dir) {
	v.pivot = pivot
	v.r = r
	v.pos = pos
	v.up = up
	v.lockedAxis = nil
}

// EmitViewpoint emits the current camera viewpoint state as a camera trace event.
func (v *viewpointState) EmitViewpoint(tr *T.Trace) {
	if tr == nil {
		return
	}
	tr.Camera(v.pivot.X, v.pivot.Y, v.pivot.Z, v.r,
		v.pos.Theta, v.pos.Phi,
		v.up.Theta, v.up.Phi)
}

// OrbitViewpoint applies a great-circle orbit (carrying from→to) and emits the new state.
func (v *viewpointState) OrbitViewpoint(from, to dir, tr *T.Trace) {
	v.orbit(from, to)
	v.EmitViewpoint(tr)
}

// OrbitLockedViewpoint applies a handhold-constrained orbit: the first call locks the
// rotation axis from the from→to arc; subsequent calls keep the same axis. The lock is
// cleared by the next SetViewpoint. Emits a camera event each call.
func (v *viewpointState) OrbitLockedViewpoint(from, to dir, tr *T.Trace) {
	v.orbitLocked(from, to)
	v.EmitViewpoint(tr)
}

// ZoomViewpoint scales the orbit radius by factor and emits the new state.
func (v *viewpointState) ZoomViewpoint(factor float64, tr *T.Trace) {
	v.zoom(factor)
	v.EmitViewpoint(tr)
}

// PanViewpoint slides the orbit pivot by a world delta and emits the new state.
func (v *viewpointState) PanViewpoint(delta vec3, tr *T.Trace) {
	v.pan(delta)
	v.EmitViewpoint(tr)
}
