---
name: project-edge-midpoint-offset-plumbing
description: Edge `midpointOffset` field, `setEdgeMidpointOffset`, and EdgeActionsCtx are already wired end-to-end — schema/adapter/mutation pre-exist, don't re-discover
metadata:
  type: project
---

**NOTE (post-R3F cutover, 2026-05-26):** The RF app layer (`rf/app/`, `rf/edges/SubstrateEdge.tsx`) was deleted in Slice 3 of the R3F cutover. The mutation hook and EdgeActionsCtx no longer exist. The current edge rendering is `SingleEdgeTube` in `tools/topology-vscode/src/webview/three/ThreeView.tsx`. The spec-field and adapter plumbing still applies:

- **Spec field:** `midpointOffset?: number` is in `WireProps` / `WIRE_PROPS` (`tools/topology-vscode/src/schema/wire-defs.ts`), and `pickWireProps` in `tools/topology-vscode/src/webview/rf/adapter/spec-to-flow.ts` threads it generically into edge `data`.
- **Renderer (R3F):** `SingleEdgeTube` in `tools/topology-vscode/src/webview/three/ThreeView.tsx` reads edge geometry. A new edge-level scalar added to `WireProps` is available via the store's `EdgeData` — thread it through `SingleEdgeTube`'s props.
- The next planned edge scalar is `bend: { x, y, z }` on `EdgeView` (view-only, not a `WireProp`), per handoff.md.

**How to apply:** When extending edge rendering or adding a new edge-level scalar (similar shape to `midpointOffset`), add it to `WIRE_PROPS` in `wire-defs.ts` — the adapter auto-threads it via `pickWireProps`. Then use it in `SingleEdgeTube`.

Related: [[feedback-schema-parser-parity]] still applies for the schema side; the parser is what makes generic threading work.
