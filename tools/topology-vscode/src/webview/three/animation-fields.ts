// Single source of truth for animation data fields written by pump.ts and read
// by view components. To add a new animation kind:
//   1. Add an entry here (key = field name, kind = "edge" | "node", type = shape).
//   2. Write the field in pump.ts using the key from this registry.
//   3. Read it in one consumer (SubstrateEdge.tsx for edges, a node component for nodes).
// TypeScript will surface every consumer via compile error on rename.

export const ANIMATION_FIELDS = {
  pulse:    { name: "pulse"    as const, kind: "edge" as const, type: {} as { value: number; simStep: number } },
  lastFire: { name: "lastFire" as const, kind: "node" as const, type: {} as number },
} as const;

export type AnimationFieldName = keyof typeof ANIMATION_FIELDS;
