package Wiring

// scene_node_pos_persist.go — nodePosPersister's root field is retained only so
// EnableEditPersist / scene-path tests can assert which topology form (directory-tree vs
// monolithic) a MoveDispatch was loaded from ("" == monolithic, no per-node meta.json).
//
// The scene-polar WRITE path this persister used to own is gone: under the flat
// absolute scene-polar model (node_move.go RootMove, quant_offset_persist.go) a node's
// persisted position is its integer scalar triple (iTheta,iPhi,iR) about the scene
// center — every node independent — not a scene-polar center, so scene-polar is no
// longer scheduled for write on drag.

// nodePosPersister's root field is inspected by tests to confirm which topology form a
// MoveDispatch loaded from; it schedules no writes of its own.
type nodePosPersister struct {
	root string // tree root; "" for a monolithic topology.json (no per-node meta.json)
}
