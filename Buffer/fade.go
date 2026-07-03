// Buffer/fade.go — pure fixpoint computation for the fade (dimming) mask.
//
// Ported VERBATIM from the pre-branch TS render-mask fixpoint
// (tools/topology-vscode/src/webview/three/fade.ts). It determines which nodes and edges
// are visually DIMMED — nothing more. It references no clock, no firing rule, no held
// state: fade is a Go-owned VISUAL property (Go owns topology + selection), and this is the
// same 4-rule fixpoint the editor used to run, now on the Go side where the buffer is
// authored.
//
// Rules (applied to fixpoint):
//  1. A directly-faded node fades all its incident edges.
//  2. A directly-faded edge is faded.
//  3. A node with ZERO non-faded incident edges auto-fades (which fades any remaining
//     incident edges and can cascade to neighbors).
//  4. A node with NO incident edges at all is only faded if directly faded.

package Buffer

// FadeEdge is one edge for the fade fixpoint: its id (label) and endpoint node ids.
type FadeEdge struct {
	ID     string
	Source string
	Target string
}

// computeFade returns the full set of faded node ids and faded edge ids given the
// directly-faded seed sets. Pure — no side effects, no Go domain state.
func computeFade(
	nodeIDs []string,
	edges []FadeEdge,
	directlyFadedNodes map[string]bool,
	directlyFadedEdges map[string]bool,
) (fadedNodes map[string]bool, fadedEdges map[string]bool) {
	fadedNodes = make(map[string]bool, len(directlyFadedNodes))
	for n := range directlyFadedNodes {
		fadedNodes[n] = true
	}
	fadedEdges = make(map[string]bool, len(directlyFadedEdges))
	for e := range directlyFadedEdges {
		fadedEdges[e] = true
	}

	// Build incident-edge index: nodeID → set of edge ids.
	incident := make(map[string]map[string]bool, len(nodeIDs))
	for _, n := range nodeIDs {
		incident[n] = map[string]bool{}
	}
	for _, e := range edges {
		if s, ok := incident[e.Source]; ok {
			s[e.ID] = true
		}
		if t, ok := incident[e.Target]; ok {
			t[e.ID] = true
		}
	}

	for changed := true; changed; {
		changed = false

		// Rule 1: faded node → fade all its incident edges.
		for nid := range fadedNodes {
			for eid := range incident[nid] {
				if !fadedEdges[eid] {
					fadedEdges[eid] = true
					changed = true
				}
			}
		}

		// Rule 3: node with incident edges but zero non-faded incident edges → auto-fade.
		for _, nid := range nodeIDs {
			if fadedNodes[nid] {
				continue
			}
			inc := incident[nid]
			if len(inc) == 0 {
				continue // no edges: only faded if directly faded (rule 4)
			}
			allFaded := true
			for eid := range inc {
				if !fadedEdges[eid] {
					allFaded = false
					break
				}
			}
			if allFaded {
				fadedNodes[nid] = true
				changed = true
			}
		}
	}

	return fadedNodes, fadedEdges
}
