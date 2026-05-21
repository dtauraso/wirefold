# Navigation-Tax Audit

## Frame

**Navigation tax** = number of files you must touch to make a change,
weighted by reference type: typed references (low tax) vs. bare strings
without a central registry (high tax). The taxable surface is "names used
as keys without a central registry."

## Tax by change type

| Change | Tax | Files touched |
|---|---|---|
| Add a kind | ~0 | Go struct + optional SPEC.md; TS auto-generates |
| Edit firing rule | ~0 | `nodes/<Kind>/<Kind>.go` only |
| Add a port | ~0 | Go struct only; gen-node-defs picks it up |
| Add a wire prop | ~0 (scalar) / Low (enum) | Go loader tag only; `gen-node-defs` regenerates `wire-defs.ts`; adapters and parser loop WIRE_PROPS automatically. Enum-typed props need one explicit case added to `parseEdge`. |
| Rename a kind | Low | Generated kinds: rename Go package + SPEC.md; generators propagate |
| Rename/remove a port | Low (post-collapse) | Edit SPEC.md `## Ports` for the kind; `gen-node-defs` regenerates the single source |
| Add new animation kind | Low | Add entry to `animation-fields.ts`; write in `pump.ts`; read in one consumer |
| Rename animation data field | Low | Rename key in `animation-fields.ts`; TypeScript compiler surfaces all consumers |

## Audit results

Scoped to `tools/topology-vscode/src/webview/rf/` and `src/schema/`.  
Generated files excluded: `node-defs.ts` (RF node visual defs), `node-data-types.ts` — noted but not listed.

### Kind-name scatter (4 kinds × avg 1.25 call sites)

| Kind | Sites |
|---|---|
| `"Input"` | `schema/node-types.ts:18`, `rf/app/_use-drag-drop.ts:39` |
| `"ReadGate"` | `schema/node-types.ts:19` |
| `"ChainInhibitor"` | `schema/node-types.ts:20` |
| `"InhibitRightGate"` | `schema/node-types.ts:21` |

Note: `node-types.ts` is the RUNTIME_IMPLEMENTED_KINDS set — it IS a central list, but it was a second copy of the kind names that also live in `node-defs.ts` (generated). The only scatter beyond that is `_use-drag-drop.ts:39` which branches on `"Input"` to set default node data.

### Port-handle scatter (10 handles × avg 2 call sites)

Each port handle appeared in exactly 2 files: `schema/node-types.ts` (the schema port list) and `rf/nodes/node-defs.ts` (the RF handle IDs). No scatter beyond those two sources was found.

| Handle | Sites |
|---|---|
| `ToReadGate` | `schema/node-types.ts:28`, `rf/nodes/node-defs.ts:26` |
| `FromInput` | `schema/node-types.ts:33`, `rf/nodes/node-defs.ts:27` |
| `FromChainInhibitor` | `schema/node-types.ts:33`, `rf/nodes/node-defs.ts:27` |
| `ToChainInhibitor` | `schema/node-types.ts:34`, `rf/nodes/node-defs.ts:27` |
| `FromPrevChainInhibitorNode` | `schema/node-types.ts:39`, `rf/nodes/node-defs.ts:24` |
| `ToNextChainInhibitorNode` | `schema/node-types.ts:42`, `rf/nodes/node-defs.ts:24` |
| `ToEdge` | `schema/node-types.ts:41` |
| `FromLeft` | `schema/node-types.ts:48`, `rf/nodes/node-defs.ts:25` |
| `FromRight` | `schema/node-types.ts:48`, `rf/nodes/node-defs.ts:25` |
| `ToPassed` | `schema/node-types.ts:49`, `rf/nodes/node-defs.ts:25` |

### Summary

- The pre-audit doc predicted scatter in `_on-connect.ts`, `pump.ts`, `PortRim.tsx`, `_constants.ts` — none found. The codebase is cleaner than predicted.
- Real duplication: `schema/node-types.ts` and `rf/nodes/node-defs.ts` were two separate string lists for the same port handles. A rename touched both.
- The `"Input"` kind branch in `_use-drag-drop.ts:39` is the only behavior-gating use of a bare kind string; it is the highest-tax single site.

## Fix applied

Collapsed `schema/node-types.ts` and `rf/nodes/node-defs.ts` into one generated source:
- Added `edgeKind`, `role`, `shape`, `fill`, `stroke`, `width`, `height` fields to each SPEC.md `## View` / `## Ports` section.
- Extended `tools/gen-node-defs` to emit those fields plus `RUNTIME_IMPLEMENTED_KINDS` into `node-defs.ts`.
- `node-types.ts` now imports generated entries from `node-defs.ts`; only static non-generated kinds (Generic, DetectorLatch, PatternAnd) remain hand-written there.
- Port handle names for generated kinds now have a single source: SPEC.md.

## Remaining

(none)

## Out of scope

Substrate model changes; editor visual changes; any actual rename/refactor
of kinds or ports in this initiative.
