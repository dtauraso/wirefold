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
| Add a wire prop | Medium | `spec.ts`, `spec-to-flow.ts`, `flow-to-spec.ts`, `SubstrateEdge.tsx`, schema parser, Go loader (~5–6 files) |
| Rename a kind | High | All of the above + `_on-connect.ts`, `_use-drag-drop.ts`, `RF_NODE_TYPES`, `RUNTIME_IMPLEMENTED_KINDS`, schema constants, `PortRim.tsx`, `pump.ts` (~8–12 files) |
| Rename/remove a port | High | Same scatter as rename-kind; port handle strings live in `_on-connect.ts`, `pump.ts`, and schema constants with no central lookup |

## Hot spots

**(a) Wire-prop threading.**  
`spec.ts` → `spec-to-flow.ts` → `flow-to-spec.ts` → `SubstrateEdge.tsx` →
schema parser → Go loader. Each hop duplicates the prop name as a string.
No typed wire-prop registry exists that all adapters consult.

**(b) Kind/port name scatter.**  
Kind names and port handles appear as bare strings in: `_on-connect.ts`,
`_use-drag-drop.ts`, `RF_NODE_TYPES` (`_constants.ts`),
`RUNTIME_IMPLEMENTED_KINDS`, schema constants, `PortRim.tsx`, `pump.ts`.
No central registry; renaming requires manual grep + edit across all sites.

## Audit step

Before any fix, run a grep to enumerate every call site that consumes a
kind name or port handle as a string. Produce a table of `file:line` per
name. That table is the input to the fix design.

Command (run from `tools/topology-vscode/src/`):

```
grep -rn --include="*.ts" --include="*.tsx" \
  -E '"(Input|ReadGate|ChainInhibitor|InhibitRightGate|in[0-9]|out[0-9]|slot[0-9]|inhibit|pass|chain)"' \
  . | grep -v node_modules | grep -v out/
```

Land the output as a new `## Audit output` section in this doc before
designing any fix.

## Fix direction (sketch — not committed)

Two candidates to investigate after the audit table exists:

1. **Table-driven kind/port registry.** A single TypeScript module exports
   a record of every kind and its ports. All handlers import from it.
   Renaming a kind becomes a one-file change.

2. **Typed wire-prop schema.** Either collapse `spec↔RF` adapters into a
   typed wire-prop registry both adapters import, or accept the adapter
   split and introduce a single typed schema module both sides reference.

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

Note: `node-types.ts` is the RUNTIME_IMPLEMENTED_KINDS set — it IS a central list, but it is a second copy of the kind names that also live in `node-defs.ts` (generated). The only scatter beyond that is `_use-drag-drop.ts:39` which branches on `"Input"` to set default node data.

### Port-handle scatter (10 handles × avg 2 call sites)

Each port handle appears in exactly 2 files: `schema/node-types.ts` (the schema port list) and `rf/nodes/node-defs.ts` (the RF handle IDs). No scatter beyond those two sources was found.

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

- The pre-audit doc predicted scatter in `_on-connect.ts`, `pump.ts`, `PortRim.tsx`, `_constants.ts` — none found. The codebase is cleaner than feared.
- Real duplication: `schema/node-types.ts` and `rf/nodes/node-defs.ts` are two separate string lists for the same port handles. A rename touches both.
- The `"Input"` kind branch in `_use-drag-drop.ts:39` is the only behavior-gating use of a bare kind string; it's the highest-tax single site.

## Next single concrete step

Audit is landed. Pick the first hot spot to fix:
- **Port registry** (`node-types.ts` ↔ `node-defs.ts` duplication) — highest rename tax; all 10 port handles live in 2 files; a single generated or imported source eliminates the gap.
- **Kind branch** (`_use-drag-drop.ts:39`) — one site, low urgency, but the only behavior-gating bare-string comparison in the tree.

## Out of scope

Substrate model changes; editor visual changes; any actual rename/refactor
of kinds or ports in this initiative.
