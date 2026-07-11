// registry.go — self-registration API for node kinds.
// Each node package calls Wiring.Register in its own init().

package Wiring

import (
	"context"

	T "github.com/dtauraso/wirefold/Trace"
)

// kindEntry is one entry in the kind registry.
type kindEntry struct {
	// newNode returns a fresh zero-valued pointer to the node struct.
	newNode func() any
}

// kindRegistry maps spec kind name → kindEntry.
var kindRegistry = map[string]kindEntry{}

// radiusForwarders is the set of kinds that forward the layout radius (iR) cascade
// to their descendants. Declared per kind (RegisterRadiusForwarder) so the property
// is a registry fact, not a hard-coded kind string in the loader / move dispatch.
var radiusForwarders = map[string]bool{}

// RegisterRadiusForwarder marks kind as one that forwards the layout radius cascade.
// Kept as a separate call from the node constructor registration so the code
// generator's source scan for node-kind registrations is unaffected.
func RegisterRadiusForwarder(kind string) {
	radiusForwarders[kind] = true
}

// ForwardsRadius reports whether the kind forwards the layout radius cascade.
func ForwardsRadius(kind string) bool {
	return radiusForwarders[kind]
}

// Register adds kind to kindRegistry and Registry. Panics if kind is already registered.
func Register(kind string, newNode func() any) {
	if _, exists := kindRegistry[kind]; exists {
		panic("Wiring.Register: kind already registered: " + kind)
	}
	e := kindEntry{newNode: newNode}
	kindRegistry[kind] = e
	// Also populate the loader-facing Registry immediately.
	sample := e.newNode()
	ports := reflectPorts(sample)
	stateKeys := reflectStateKeys(sample)
	Registry[kind] = NodeBuilder{
		Ports:     ports,
		StateKeys: stateKeys,
		Build: func(ctx context.Context, name string, data *NodeData, pb PortBindings, tr *T.Trace, geom nodeGeom, partnerCenter partnerCenterFn) (Node, error) {
			return reflectBuild(ctx, name, data, pb, e, tr, geom, partnerCenter)
		},
	}
}
