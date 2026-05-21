// Registry of top-level Spec metadata fields (everything except nodes/edges/view).
// Adding a new field: add one entry here; parser and adapters iterate this registry
// automatically — no other files need touching.

import type { Spec } from "./types-graph";
import { arr, opt } from "./parse-primitives";
import { parseNote } from "./parse-meta";

type MetaKey = keyof Omit<Spec, "nodes" | "edges">;

type MetaFieldDef<K extends MetaKey> = {
  // Parse raw JSON → the typed field value (or undefined if absent).
  parse: (raw: unknown) => Spec[K];
  // true = flow-to-spec spreads from currentSpec verbatim (not rebuilt from RF state).
  passThrough: boolean;
};

export const TOPOLOGY_META_FIELDS: { [K in MetaKey]: MetaFieldDef<K> } = {
  notes: {
    // notes is NOT a simple passThrough — flowToSpec rebuilds it from RF note nodes.
    // passThrough: false means flow-to-spec handles it separately.
    parse: (raw) =>
      opt(raw, (l) =>
        arr(l, "spec.notes").map((n, i) => parseNote(n, `spec.notes[${i}]`)),
      ),
    passThrough: false,
  },
};
