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
	// persist, when non-nil, is called with the current viewpoint after every EmitViewpoint
	// so a gesture-driven change is persisted to scene.json. nil until armed by
	// MoveDispatch.EnableViewpointPersist (after the startup seed), so the seed's own emit
	// does not write. Owned by MoveDispatch; the debounce/write live in the persister.
	persist func(viewpoint)
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
	if tr != nil {
		tr.Camera(v.pivot.X, v.pivot.Y, v.pivot.Z, v.r,
			v.pos.Theta, v.pos.Phi,
			v.up.Theta, v.up.Phi)
	}
	// Persist the just-emitted viewpoint (debounced, off the hot path) when armed —
	// independent of the trace sink. Every gesture viewpoint change (orbit/zoom/pan/home)
	// flows through EmitViewpoint, so this is the single chokepoint for the write side.
	if v.persist != nil {
		v.persist(v.viewpoint)
	}
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

// Camera viewpoint API — thin delegators to the owned viewpointState above.
// The public signatures are unchanged; the state and behavior live on md.vp.

func (md *MoveDispatch) SetViewpoint(pivot vec3, r float64, pos, up dir) {
	md.vp.SetViewpoint(pivot, r, pos, up)
}

// cameraViewEvent is the single Camera event every camera-changing delegator below hands
// to emitViewFrame. Camera decodes entirely from the VIEW frame's own Camera block (see
// buffer-log.ts's decodeEventLine "camera" case) — no row identity to resolve.
func cameraViewEvent() []RowEvent {
	return []RowEvent{{Kind: T.KindCamera, NodeRow: -1, PortRow: -1, TargetRow: -1, TargetPortRow: -1, EdgeRow: -1}}
}

func (md *MoveDispatch) EmitViewpoint(tr *T.Trace) {
	md.vp.EmitViewpoint(tr)
	md.emitViewFrame(cameraViewEvent())
}
func (md *MoveDispatch) OrbitViewpoint(from, to dir, tr *T.Trace) {
	md.vp.OrbitViewpoint(from, to, tr)
	md.emitViewFrame(cameraViewEvent())
}
func (md *MoveDispatch) OrbitLockedViewpoint(from, to dir, tr *T.Trace) {
	md.vp.OrbitLockedViewpoint(from, to, tr)
	md.emitViewFrame(cameraViewEvent())
}
func (md *MoveDispatch) ZoomViewpoint(factor float64, tr *T.Trace) {
	md.vp.ZoomViewpoint(factor, tr)
	md.emitViewFrame(cameraViewEvent())
}
func (md *MoveDispatch) PanViewpoint(delta vec3, tr *T.Trace) {
	// A dolly is a pure CAMERA move (the eye translates toward the cursor). It must NOT move the
	// scene sphere: coupling them left md.sceneSphere.Center diverged from the movers' held
	// center until a later broadcast reconciled it with a jump (the "zoom got canceled"
	// symptom). Nothing moves the sphere — MODEL.md: "It is established once and never moves."
	// Pan-moves-the-sphere is REJECTED doctrine, not a gap to fill; if it is ever revisited it
	// must be its own gesture, never a side effect of a camera move.
	md.vp.PanViewpoint(delta, tr)
	md.emitViewFrame(cameraViewEvent())
}
