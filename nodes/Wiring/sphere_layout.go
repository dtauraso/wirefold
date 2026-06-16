// sphere_layout.go — graph-level node-position helpers for the polar layout.
// Node centers are stored absolutely (meta.json x/y/z).

package Wiring

// sphereEdge is a DIRECTED connection: Source outputs to Target.
type sphereEdge struct {
	Source string
	Target string
}
