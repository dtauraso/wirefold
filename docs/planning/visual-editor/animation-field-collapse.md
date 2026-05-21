# Animation Field Registry Collapse

## Frame

"Animation fields" are data fields that `pump.ts` writes onto RF node/edge `.data`
objects in response to trace events. View components read these fields to drive
animations (pulse circles, fire flashes). Today the field names are bare strings
with no registry: `pump.ts` owns the write side and each view component owns the
read side independently. Renaming a field requires touching both sides across
multiple files. Adding a new animation kind requires the same dual-update plus a
types edit — three files minimum, no single source of truth.

## Current tax (concrete)

**Rename an animation field** (HIGH) — e.g. rename `pulse` → `send`:
- `tools/topology-vscode/src/webview/rf/types.ts` — `EdgeData.pulse` field declaration
- `tools/topology-vscode/src/webview/rf/pump.ts` — write: `{ ...e.data, pulse: { ... } }`
- `tools/topology-vscode/src/webview/rf/edges/SubstrateEdge.tsx` — reads `data?.pulse` (4 references)
- `tools/topology-vscode/src/webview/rf/nodes/GenericNode.tsx` — `data.lastFire` prop reference

Rename `lastFire` → anything: same `types.ts` + `pump.ts` write + `use-fire-flash.ts`
(hook arg) + `GenericNode.tsx` (prop pass). Four files minimum, all by hand.

**Add a new animation kind** (MEDIUM):
- `pump.ts` — add a new event branch, write the new field
- `types.ts` — add the field to `NodeData` or `EdgeData`
- A view component (`SubstrateEdge.tsx` or a node component) — add the read + animate logic

No recipe exists; the developer must infer the pattern from existing fields.

## Proposed source of truth

**Option (a) — single TS registry module `animation-fields.ts`** (chosen).

```ts
export const ANIMATION_FIELDS = {
  pulse:    { kind: "edge", type: {} as { value: number; simStep: number } },
  lastFire: { kind: "node", type: {} as number },
} as const;

export type AnimationFieldName = keyof typeof ANIMATION_FIELDS;
```

Both `pump.ts` (write) and view components (read) import from this module.
Renaming a field becomes a one-file change: update the key in `ANIMATION_FIELDS`
and let TypeScript surface every consumer via compile error.

**Option (b) — generated from Go** is overkill: these fields have no Go consumer.
Revisit if the Go substrate ever needs to declare animation events directly.

## Migration steps

1. **Add registry module.** Create `animation-fields.ts` with `ANIMATION_FIELDS`
   and `AnimationFieldName`. No other files change; CI stays green. ✅ (7e2f243)

2. **Update pump.ts writes.** Replace bare-string keys with `ANIMATION_FIELDS`
   key references. TypeScript enforces key validity at compile time. ✅ (9375373)

3. **Update view consumers.** `SubstrateEdge.tsx`, `use-fire-flash.ts`,
   `GenericNode.tsx` — read via registry-typed accessors. Field names come from
   the registry; no bare strings. ✅ (b84a0c8)

4. **Derive types from registry.** Replace hand-written `EdgeData.pulse` and
   `NodeData.lastFire` field declarations with types derived from `ANIMATION_FIELDS`
   so the shape stays in sync automatically. ✅ (0aa05a6)

5. **Document the recipe.** Add a comment block to `animation-fields.ts`:
   "To add a new animation: (1) add an entry here, (2) write it in pump.ts,
   (3) read it in one consumer." One place to look, three-step recipe. ✅ (this commit)

## Verification

```
npm run build
npm run check:loc
```
Manual smoke: drag a node → node flash still fires; connect an edge and run
the sim → pulse circle still travels; no console errors.

## Out of scope

- Substrate timing or pacing changes.
- New animation effects.
- CSS-vs-JS animation conversion.
- `FLASH_TIMEOUT_MS` is currently unused — free cleanup, but not bundled here.

## Next single concrete step

Create `tools/topology-vscode/src/webview/rf/animation-fields.ts` with the
`ANIMATION_FIELDS` registry and `AnimationFieldName` type; no other files change.
