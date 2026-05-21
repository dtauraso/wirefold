// Registry of top-level Spec metadata fields (everything except nodes/edges/view).
// Adding a new field: add one entry here; parser and adapters iterate this registry
// automatically — no other files need touching.

import type { Spec } from "./types-graph";
import { arr, obj, opt, str } from "./parse-primitives";
import { parseLegendRow, parseNote, parseSeedEvent } from "./parse-meta";

type MetaKey = keyof Omit<Spec, "nodes" | "edges">;

type MetaFieldDef<K extends MetaKey> = {
  // Parse raw JSON → the typed field value (or undefined if absent).
  parse: (raw: unknown) => Spec[K];
  // true = flow-to-spec spreads from currentSpec verbatim (not rebuilt from RF state).
  passThrough: boolean;
};

export const TOPOLOGY_META_FIELDS: { [K in MetaKey]: MetaFieldDef<K> } = {
  timing: {
    parse: (raw) =>
      opt(raw, (t) => {
        const to = obj(t, "spec.timing");
        return {
          duration: opt(to.duration, (x) => str(x, "spec.timing.duration")),
          seed: opt(to.seed, (x) =>
            arr(x, "spec.timing.seed").map((e, i) =>
              parseSeedEvent(e, `spec.timing.seed[${i}]`),
            ),
          ),
        };
      }),
    passThrough: true,
  },
  cycleAnchor: {
    parse: (raw) => opt(raw, (x) => str(x, "spec.cycleAnchor")),
    passThrough: true,
  },
  runtime: {
    parse: (raw) =>
      opt(raw, (x) => {
        const s = str(x, "spec.runtime");
        if (s !== "ticked") throw new Error(`spec.runtime: unknown value "${s}"`);
        return s as "ticked";
      }),
    passThrough: true,
  },
  legend: {
    parse: (raw) =>
      opt(raw, (l) =>
        arr(l, "spec.legend").map((r, i) =>
          parseLegendRow(r, `spec.legend[${i}]`),
        ),
      ),
    passThrough: true,
  },
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
