// equation-form.ts — pure resolution helpers for the typed polar-equation FORM
// (RuleEquationPanel's "+ Add equation" flow). No React, no state — just pure functions over
// an already-decoded snapshot. The form itself (RuleEquationPanel.tsx) holds all in-progress
// values as local component state; these helpers only turn typed text into a resolved buffer
// row / component code, mirroring locks.go's polarComp ordering (compTheta=0, compPhi=1,
// compR=2) and the Port block's node-row/name/isInput columns.

import type { DecodedSnapshot } from "./buffer-decode";
import { nodeLabel, portName } from "./buffer-decode";
import { readPortNodeRow, readPortIsInput } from "../../schema/buffer-layout";

/** polarComp ordering (locks.go): compTheta=0, compPhi=1, compR=2. */
export const COMP_THETA = 0;
export const COMP_PHI = 1;
export const COMP_R = 2;

/** Parse a typed comp-slot word ("theta"/"phi"/"r", optionally "-"-prefixed for θ/φ,
 *  optionally already-substituted glyphs) into {comp, sign}. Returns null while the text is
 *  not (yet) a valid whole word — the caller keeps the slot open for more typing. */
export function parseCompInput(text: string): { comp: number; sign: number } | null {
  const raw = text.trim();
  if (raw === "") return null;
  let sign = 1;
  let body = raw.toLowerCase();
  if (body.startsWith("-") || raw.startsWith("−")) {
    sign = -1;
    body = body.replace(/^[-−]/, "");
  }
  if (body === "theta" || body === "θ") return { comp: COMP_THETA, sign };
  if (body === "phi" || body === "φ") return { comp: COMP_PHI, sign };
  if (body === "r") {
    if (sign === -1) return null; // r carries no sign
    return { comp: COMP_R, sign: 1 };
  }
  return null;
}

/** Live word→symbol substitution shown in the slot while typing (before full resolution):
 *  "theta"→θ, "phi"→φ, "r"→r, a leading "-" carries through as the sign glyph. Returns the
 *  raw text unchanged if it doesn't (yet) match a comp-word prefix. */
export function compLivePreview(text: string): string {
  const parsed = parseCompInput(text);
  if (parsed) return compGlyph(parsed.comp, parsed.sign);
  return text;
}

/** Render a resolved (comp, sign) pair as its display glyph. */
export function compGlyph(comp: number, sign: number): string {
  if (comp === COMP_R) return "r";
  const base = comp === COMP_PHI ? "φ" : "θ";
  return sign < 0 ? `−${base}` : base;
}

/** Resolve typed text to an existing node's buffer ROW by exact label match (the label→row
 *  lookup every typed node/center/torus slot needs). -1 if no node has that exact label. */
export function resolveNodeRowByLabel(decoded: DecodedSnapshot, text: string): number {
  const t = text.trim();
  if (!t) return -1;
  for (let i = 0; i < decoded.nodeCount; i++) {
    if (nodeLabel(decoded, i) === t) return i;
  }
  return -1;
}

export interface PortOption {
  row: number;
  name: string;
  isInput: boolean;
}

/** All ports belonging to buffer node row `nodeRow`, in port-row order — the portName slot's
 *  autocomplete list. */
export function listPortsForNode(decoded: DecodedSnapshot, nodeRow: number): PortOption[] {
  const out: PortOption[] = [];
  for (let i = 0; i < decoded.portCount; i++) {
    if (readPortNodeRow(decoded.portView, i) !== nodeRow) continue;
    out.push({ row: i, name: portName(decoded, i), isInput: readPortIsInput(decoded.portView, i) === 1 });
  }
  return out;
}
