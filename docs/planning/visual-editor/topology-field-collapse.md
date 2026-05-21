# Spec: Top-Level Field Registry (`topology-field-collapse`)

## Frame

"Top-level fields" are every root key of `topology.json` that isn't
`nodes`, `edges`, or `view`. Current fields: `timing`, `cycleAnchor`,
`legend`, `notes`, `runtime`. Their navigation tax is Medium: each field
is threaded by name through the schema parser, the type wrapper, and both
adapters. Adding or renaming a field requires touching 4–5 files with no
single source of truth. Go does **not** parse any of these fields
(`loader.go` is field-name-free for this set); the tax is TS-only.

## Current tax (concrete)

| Field | Files : lines |
|---|---|
| `timing` | `types-graph.ts:64–71`, `parse-spec.ts:25–35`, `parse-meta.ts:17` (SeedEvent), `flow-to-spec.ts:89`, `save.ts:10` (comment) |
| `cycleAnchor` | `types-graph.ts:74`, `parse-spec.ts:36`, `flow-to-spec.ts:90` |
| `legend` | `types-graph.ts:75`, `parse-spec.ts:42–45`, `parse-meta.ts:27` (LegendRow), `flow-to-spec.ts:91` |
| `notes` | `types-graph.ts:76`, `parse-spec.ts:47–49`, `parse-meta.ts:36` (Note), `flow-to-spec.ts:88`, `spec-to-flow.ts:176–181` |
| `runtime` | `types-graph.ts:79`, `parse-spec.ts:37–40`, `flow-to-spec.ts:92` |

Go side: `loader.go` contains no field-name references to this set — no
cross-language hop required.

## Proposed source of truth

**Option (c) hybrid** — structural triad (`nodes`, `edges`, `view`) stays
explicit everywhere; only the five passthrough/metadata fields enter a
registry `TOPOLOGY_META_FIELDS` in `src/schema/meta-field-defs.ts`.

Justification: `timing` already has a sub-parser (`parseSeedEvent`) and a
nested shape; a flat registry entry holds the parser function reference,
keeping the registry slim. The structural triad is handled by dedicated
parser calls that will never be generated — mixing them into the registry
buys nothing. Go is not authoritative here, so option (b) is out.

Registry shape (sketch):

```ts
type MetaFieldDef<K extends keyof Spec> = {
  parse: (raw: unknown) => Spec[K];
  passThrough: boolean; // true = flow-to-spec spreads verbatim
};
export const TOPOLOGY_META_FIELDS: { [K in MetaKey]: MetaFieldDef<K> } = { ... };
```

Adapters iterate `TOPOLOGY_META_FIELDS` instead of the current four
parallel spread lines in `flow-to-spec.ts:88–92`.

## Migration steps

1. **Add `meta-field-defs.ts`** ✓ — define `TOPOLOGY_META_FIELDS` with
   parser refs for all five fields. No behavior change yet. (529e6fc)
2. **Wire `parse-spec.ts`** ✓ — replace inline `opt(o.timing, ...)` etc.
   with a loop over `TOPOLOGY_META_FIELDS`. Remove per-field branches. (512dd83)
3. **Wire `flow-to-spec.ts`** ✓ — replace four spread lines (88–92) with
   a loop over `TOPOLOGY_META_FIELDS` where `passThrough === true`. (15a48f5)
4. **Derive `Spec` TS type** — deferred: `meta-field-defs.ts` imports
   `Spec` from `types-graph.ts`, and `types-graph.ts` cannot re-import
   from `meta-field-defs.ts` without a circular dep (through
   `SeedEvent`/`LegendRow`/`Note`). Resolving requires moving those
   leaf types to a separate file; deferred until the cost of the
   explicit-field form is felt.
5. **`npm run check:loc`** ✓ — no file exceeds 100 LOC.

## Verification

`npm run build` · `npm run check:loc` · `go build ./...` · `go test ./...`
· manual smoke: load `topologies/line.json`, add a `legend` row and a
`timing.seed`, save, reload — fields preserved.

## Out of scope

- Substrate model changes.
- New top-level fields (add to registry when needed).
- The structural `nodes`/`edges`/`view` triad — not scattered, not in scope.

## Next single concrete step

Create `tools/topology-vscode/src/schema/meta-field-defs.ts` with
`TOPOLOGY_META_FIELDS` (five entries, parser refs, no behavior change) and
confirm `npm run build` still passes.
