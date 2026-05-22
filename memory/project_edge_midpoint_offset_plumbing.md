---
name: project-edge-midpoint-offset-plumbing
description: Edge `midpointOffset` field, `setEdgeMidpointOffset`, and EdgeActionsCtx are already wired end-to-end — schema/adapter/mutation pre-exist, don't re-discover
metadata:
  type: project
---

The pre-RF "modifiable snake edge" feature was rebuilt on `task/diagram-animation-fixes`. The plumbing for `midpointOffset` was already in place from a prior pass and does NOT need re-wiring when working on edges:

- **Spec field:** `midpointOffset?: number` is in `WireProps` / `WIRE_PROPS` (schema), and `pickWireProps` in `tools/topology-vscode/src/webview/rf/adapter/spec-to-flow.ts` already threads it generically into RF edge `data`.
- **Mutation:** `setEdgeMidpointOffset(edgeId, midpointOffset)` exists in `tools/topology-vscode/src/webview/rf/app/_use-edge-handlers.ts`; it patches the spec edge's `midpointOffset` and calls `scheduleSave()`.
- **Context:** `EdgeActionsCtx` is provided by `app.tsx` wrapping the RF canvas; `useEdgeActions()` is the consumer hook.
- **Renderer:** `tools/topology-vscode/src/webview/rf/edges/SubstrateEdge.tsx` contains `pickShape`, `snakeD/snakeVD/belowD`, `buildEdgePathD`, and the `MidpointDragHandle` inner component — all in one file.

**Why:** A prior session paid ~$ in subagent grep cost re-discovering all of this. Two of the four expected wiring sites were no-ops.

**How to apply:** When extending edge rendering or adding a new edge-level scalar (similar shape to `midpointOffset`), look at how `midpointOffset` flows first — copy the pattern. Don't re-grep schema/adapter/mutation; assume they already auto-thread anything added to `WIRE_PROPS`.

Related: [[feedback-schema-parser-parity]] still applies for the schema side; the parser is what makes generic threading work.
