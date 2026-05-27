---
name: project-edge-midpoint-offset-plumbing
description: Edge `midpointOffset` is a schema-only stub — end-to-end plumbing does NOT exist in current code (verified 2026-05-26)
metadata:
  type: project
---

**CORRECTED 2026-05-26:** Earlier versions of this memory claimed that `midpointOffset`, `setEdgeMidpointOffset`, and `EdgeActionsCtx` were wired end-to-end. That claim is FALSE in current code.

- **Schema stub only:** `midpointOffset?: number` is declared in `WireProps` / `WIRE_PROPS` in `tools/topology-vscode/src/schema/wire-defs.ts` and nowhere else.
- **No setter:** `setEdgeMidpointOffset` does not exist anywhere in `src/`.
- **No EdgeActionsCtx:** `EdgeActionsCtx` does not exist in current `src/`.
- **No adapter threading:** nothing reads or writes `midpointOffset` in the adapter, store, or renderer.

The feature-audit.md §3a restore-parity table records this as **FULLY MISSING** (no drag UI, no setter, no adapter). Do not assume prior plumbing exists when working on edge rendering or midpoint drag — start from scratch.

Related: `src/schema/wire-defs.ts` is the correct place to add a new `WireProp` scalar; the adapter auto-threads declared `WireProps` generically via `pickWireProps`. But `midpointOffset` specifically is not yet threaded anywhere beyond the declaration.
