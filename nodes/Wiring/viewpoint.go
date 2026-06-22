package Wiring

// viewpoint.go — the polar camera state and its navigation ops, expressed in angle-only
// spherical terms (spherical.go). The renderer (three.js) turns this into a Cartesian
// camera at draw time; nothing here forms a rotation vector or quaternion. The only
// Cartesian is the `pivot`, which is a plain anchor POINT (like a node center), never
// rotation math — it is just added to the camera position at the renderer edge.
//
// Orientation is carried as two directions: `pos` (pivot → camera, which also fixes the
// look direction since the camera looks at the pivot) and `up` (the screen-up hint, which
// carries ROLL). A rotation spins BOTH rigidly, so tilt accumulates and is preserved.

const viewpointMinDist = 5.0

type viewpoint struct {
	pivot vec3    // world orbit center (anchor point; not rotation math)
	r     float64 // distance from pivot to camera
	pos   dir     // direction pivot → camera
	up    dir     // up-hint direction (carries roll)
}

// rotate spins the whole camera frame (position direction AND up) by the same rotation,
// so roll is preserved as a rigid turn of the frame rather than recomputed.
func (v *viewpoint) rotate(rt rot) {
	v.pos = rotateDir(v.pos, rt.Axis, rt.Angle)
	v.up = rotateDir(v.up, rt.Axis, rt.Angle)
}

// orbit applies the shortest-arc rotation carrying `from` to `to`, so a grabbed direction
// follows the cursor (the motion-driven great-circle gesture).
func (v *viewpoint) orbit(from, to dir) {
	v.rotate(arcBetween(from, to))
}

// zoom scales the orbit radius about the pivot, floored so the camera never reaches it.
func (v *viewpoint) zoom(factor float64) {
	nr := v.r * factor
	if nr < viewpointMinDist {
		nr = viewpointMinDist
	}
	v.r = nr
}

// pan slides the orbit pivot by a world delta; the camera rides along (position is pivot
// plus the radial offset). The delta is a world vector computed at the renderer edge.
func (v *viewpoint) pan(delta vec3) {
	v.pivot = v.pivot.add(delta)
}
