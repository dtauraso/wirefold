// node-override-text.ts — derive a per-kind value string for billboard labels.
// Render-only. Returns just the VALUE (no key prefix) for the field that
// matters for each kind. Fail-soft: any missing path returns "".

import type { RFNode, NodeData } from "../types";
import { NODE_DEFS } from "../../schema/node-defs";

// HandledKind is a compile-time subset check: if "Input" or "ChainInhibitor"
// are ever removed/renamed in NODE_DEFS, this line fails tsc.
type HandledKind = "Input" | "ChainInhibitor";
// eslint-disable-next-line @typescript-eslint/no-unused-vars
type _HandledIsSubsetOfNodeDefs = HandledKind extends keyof typeof NODE_DEFS ? true : never;
// Instantiate to trigger the check (evaluated at compile time only).
const _check: _HandledIsSubsetOfNodeDefs = true;
void _check;

export function nodeOverrideText(node: RFNode<NodeData>): string {
  try {
    const kind = node?.data?.type;
    if (!kind) return "";
    const raw = node.data?.nodeData as Record<string, unknown> | undefined;
    if (!raw || typeof raw !== "object") return "";

    switch (kind as HandledKind | string) {
      case "Input": {
        const init = raw["init"];
        if (!Array.isArray(init)) return "";
        return "[" + init.map((x) => String(x)).join(", ") + "]";
      }
      case "ChainInhibitor": {
        const state = raw["state"] as Record<string, unknown> | undefined;
        if (!state || typeof state !== "object") return "";
        const held = state["held"];
        if (held === undefined || held === null) return "";
        return String(held);
      }
      default:
        return "";
    }
  } catch {
    return "";
  }
}
