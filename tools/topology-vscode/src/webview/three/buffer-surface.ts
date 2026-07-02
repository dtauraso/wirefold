// buffer-surface.ts — pure on-surface selection-set computation for the buffer path.
//
// Mirrors the pre-branch scene-content.tsx { sphereOwners, surfaceIds } logic, but
// expressed over the buffer's edge-graph adjacency (edge src/dst NODE-ROW indices from
// the Edge block) instead of the React edge list. When a node is selected, the nodes ON
// THE SURFACE of that node's sphere are highlighted with the SAME yellow-ring + orange-
// halo as the selected node. Pure — no store reads, no DataView, no three.js.
//
// Two modes (from the buffer's overlay SelMode column):
//   "own"  (SelMode=1, secondary / two-finger tap): owners = [selected];
//           surface = selected + every node `selected` outputs to (src==selected → dst).
//   "surface" (SelMode=0, primary click): owners = nodes that output TO selected
//           (dst==selected → src); surface = owners + each owner's children
//           (src==owner → dst).

/** One directed edge as buffer node-row indices; -1 marks an unresolved endpoint. */
export interface EdgeAdj {
  src: number;
  dst: number;
}

/** Select mode as stored in the overlay SelMode column. */
export type SelMode = "own" | "surface";

/**
 * Compute the set of buffer node ROWS to highlight (selected + on-surface).
 *
 * @param selectedRow the selected node's buffer row, or -1 when nothing is selected.
 * @param mode        "own" (secondary) or "surface" (primary).
 * @param edges       edge adjacency (src/dst node rows); rows with a -1 endpoint or a
 *                    self-loop are ignored.
 * @returns the set of node rows to draw the highlight at. Empty when nothing selected.
 */
export function surfaceRowSet(
  selectedRow: number,
  mode: SelMode,
  edges: readonly EdgeAdj[],
): Set<number> {
  const ids = new Set<number>();
  if (selectedRow < 0) return ids;

  // Owners: whose sphere(s) the selection sits on.
  const owners =
    mode === "own"
      ? [selectedRow]
      : Array.from(
          new Set<number>(
            edges
              .filter((e) => e.dst === selectedRow && e.src >= 0)
              .map((e) => e.src),
          ),
        );

  // Each owner is highlighted, plus all of its children (nodes on its surface).
  for (const owner of owners) {
    ids.add(owner);
    for (const e of edges) {
      if (e.src === owner && e.dst >= 0) ids.add(e.dst);
    }
  }
  // The selected node itself is always highlighted (in "own" mode it is the owner; in
  // "surface" mode it is a child of each owner, but guard the owner-less case too).
  ids.add(selectedRow);
  return ids;
}
