// Pure fixpoint computation for the fade mask.
// No imports from store or React — easy to unit-test in isolation.

export interface FadeEdge {
  id: string;
  source: string;
  target: string;
}

/**
 * Compute the full set of faded nodes and edges given the directly-faded seeds.
 *
 * Rules (applied to fixpoint):
 * 1. A directly-faded node fades all its incident edges.
 * 2. A directly-faded edge is faded.
 * 3. A node with ZERO non-faded incident edges auto-fades (which fades any
 *    remaining incident edges and can cascade to neighbors).
 * 4. A node with NO incident edges at all is only faded if directly faded.
 */
export function computeFade(
  nodeIds: string[],
  edges: FadeEdge[],
  directlyFadedNodeIds: Set<string>,
  directlyFadedEdgeIds: Set<string>,
): { fadedNodes: Set<string>; fadedEdges: Set<string> } {
  const fadedNodes = new Set<string>(directlyFadedNodeIds);
  const fadedEdges = new Set<string>(directlyFadedEdgeIds);

  // Build incident-edge index: nodeId → edge ids
  const incident = new Map<string, Set<string>>();
  for (const n of nodeIds) {
    incident.set(n, new Set());
  }
  for (const e of edges) {
    incident.get(e.source)?.add(e.id);
    incident.get(e.target)?.add(e.id);
  }

  let changed = true;
  while (changed) {
    changed = false;

    // Rule 1: faded node → fade all its incident edges
    for (const nid of fadedNodes) {
      for (const eid of incident.get(nid) ?? []) {
        if (!fadedEdges.has(eid)) {
          fadedEdges.add(eid);
          changed = true;
        }
      }
    }

    // Rule 3: node with incident edges but zero non-faded incident edges → auto-fade
    for (const nid of nodeIds) {
      if (fadedNodes.has(nid)) continue;
      const inc = incident.get(nid);
      if (!inc || inc.size === 0) continue; // no edges: only faded if directly faded (rule 4)
      const allFaded = [...inc].every((eid) => fadedEdges.has(eid));
      if (allFaded) {
        fadedNodes.add(nid);
        changed = true;
      }
    }
  }

  return { fadedNodes, fadedEdges };
}
