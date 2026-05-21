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

## Next single concrete step

Run the audit grep above. Land its output in this doc as `## Audit output`.
No fixes until that table exists.

## Out of scope

Substrate model changes; editor visual changes; any actual rename/refactor
of kinds or ports in this initiative.
