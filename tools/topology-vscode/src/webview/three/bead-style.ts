// bead-style.ts — Single source of truth for bead value → appearance.
// Both interior buffer beads and animated edge beads derive fill/ring colors here.

// Single source of truth for value→appearance. Both the interior buffer beads
// (inside node 1) and the animated edge bead derive their fill/ring colors here, so
// they cannot visually diverge. (The former static data.init bead components were
// removed when node 1's interior switched to the live node-bead stream.)
const VALUE_BEAD_STYLE: Record<number, { fill: string; ring: string }> = {
  0: { fill: "#000000", ring: "#000000" },
  1: { fill: "#ffffff", ring: "#000000" },
};
// Only 0 and 1 are valid bead values. A value outside the map (including a
// missing/undefined value) returns undefined — the caller hides the bead rather
// than drawing a grey/fake fallback. With Go no longer placing -1 on a wire, a
// non-0/1 bead is a bug, not a colour to paint.
export function beadStyleForValue(v: number | null | undefined): { fill: string; ring: string } | undefined {
  return v == null ? undefined : VALUE_BEAD_STYLE[v];
}
