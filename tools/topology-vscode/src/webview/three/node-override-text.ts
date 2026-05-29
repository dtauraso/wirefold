// node-override-text.ts — derive a multi-line summary of spec-data fields that
// differ from the per-kind defaults declared in NODE_DEFS. Render-only.
//
// General-purpose: walks node.data.nodeData (the raw Spec.Node.data carried
// verbatim through the editor) and compares each key against
// NODE_DEFS[kind].defaultData. Fields with no entry in defaultData are
// considered overrides when present (the default is "absent"). No per-kind
// branching.

import type { RFNode, NodeData } from "../types";
import { NODE_DEFS } from "../../schema/node-defs";

/** Spec/RF fields that vary per instance and are NOT domain config. */
const SKIP_KEYS = new Set<string>([
  "id",
  "label",
  "type",
  "x",
  "y",
  "z",
  "position",
]);

function fmt(v: unknown): string {
  if (Array.isArray(v)) return "[" + v.map((x) => fmt(x)).join(",") + "]";
  if (v === null) return "null";
  if (typeof v === "object") {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}

function eq(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a == null || b == null) return false;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (!eq(a[i], b[i])) return false;
    return true;
  }
  if (typeof a === "object" && typeof b === "object") {
    const ak = Object.keys(a as object);
    const bk = Object.keys(b as object);
    if (ak.length !== bk.length) return false;
    for (const k of ak) {
      if (!eq((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k])) return false;
    }
    return true;
  }
  return false;
}

/**
 * Return a multi-line string listing each field on `node.data.nodeData` whose
 * value differs from `NODE_DEFS[kind].defaultData`. Empty string if there are
 * no overrides (or no spec data).
 */
export function nodeOverrideText(node: RFNode<NodeData>): string {
  const kind = node.data?.type;
  if (!kind) return "";
  const def = NODE_DEFS[kind];
  const defaults = (def?.defaultData ?? {}) as Record<string, unknown>;

  const raw = node.data?.nodeData;
  if (!raw || typeof raw !== "object") return "";

  const lines: string[] = [];
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (SKIP_KEYS.has(k)) continue;
    if (eq(v, defaults[k])) continue;
    lines.push(`${k}: ${fmt(v)}`);
  }
  return lines.join("\n");
}
