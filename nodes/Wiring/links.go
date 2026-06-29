package Wiring

// links.go — the double-link movement graph (docs/planning/visual-editor/
// double-link-polar-model.md). A movementLink is a DOUBLE link between two nodes: a
// bidirectional movement relationship — the radius between them, with each node sitting
// on the other's polar surface. It is NOT a displayed edge and carries no bead; it is
// purely for restricting movement. The polar locks (R/θ/φ equations) that ride on these
// links are added in a later step — this file only declares the graph.

// movementLink is one undirected double link between nodes A and B.
type movementLink struct {
	A string
	B string
}

// addLink appends a double link A↔B.
func (md *MoveDispatch) addLink(a, b string) {
	md.links = append(md.links, movementLink{A: a, B: b})
}

// registerMovementLinks declares the double-link graph for the current topology. The
// initial set mirrors the data edges; restriction-only links (e.g. 5↔11) are added
// later. Each pair is registered only when both nodes are loaded.
func (md *MoveDispatch) registerMovementLinks(has func(string) bool) {
	pairs := [][2]string{
		{"1", "8"}, {"1", "10"}, {"1", "9"},
		{"9", "6"}, {"9", "2"},
		{"2", "7"}, {"2", "3"},
		{"10", "11"}, {"6", "11"}, {"6", "5"}, {"7", "5"},
	}
	for _, p := range pairs {
		if has(p[0]) && has(p[1]) {
			md.addLink(p[0], p[1])
		}
	}
}
