# SVG Style Guide

Read this file before generating or modifying any SVG in this repo. It
contains the binding conventions (Part 1) and the observed house-style
vocabulary (Part 2). CLAUDE.md keeps only a pointer to here so that
sessions not touching SVGs don't pay to load it.

---

# Part 1 — SVG Diagram Conventions (required for all SVG output)

Every SVG you generate or modify must follow these rules. They exist to keep diagrams cheap to read and structurally legible. Violations must be corrected before returning the file.

1. **Semantic grouping.** Every logical unit must be wrapped in a `<g>` with a descriptive `id` and `data-role` attribute. Example: `<g id="stage-i1" data-role="inhibitor" data-index="1">`. Never group by visual proximity alone — group by meaning. A circle and its label belong in the same group.

2. **Symbol factoring.** Any element that repeats with the same structure must be defined once as a `<symbol>` in `<defs>` and instantiated with `<use href="#id" x="..." y="...">`. If there are three inhibitor stages, define one symbol, not three copies. This applies even if the repetition is only two instances.
   > **Exception:** Do not use `<symbol>`/`<use>` for node background shapes that need per-class CSS styling (`fill`, `stroke`). CSS selectors cannot pierce the shadow DOM created by `<use>`, so colors will not apply. Use direct `<rect>` elements with a shape class instead (e.g. `<rect class="shape-latch" .../>`). `<symbol>` remains appropriate for purely geometric decorations that carry no CSS-styled fill or stroke.

3. **Class-based styling.** Colors, stroke widths, and font sizes must be defined in a `<style>` block at the top of the file and applied via `class="..."`. Class names must be semantic (`.inhibitor`, `.contrast-edge`, `.recognition-gate`), not visual (`.orange`, `.thick`). No inline `stroke="#..."` or `fill="#..."` except inside the `<style>` block.

4. **Sidecar metadata.** Every SVG must begin with a `<metadata>` block containing a compact machine-readable description of the diagram's logical structure: a list of named nodes with their roles, a list of edges with source/target/kind, and (for animated diagrams) a timing table. Format as JSON inside the metadata tag. This is the spec layer; the visual is a rendering of it.

5. **Legend block.** Immediately after `<metadata>`, include a `<desc>` block with a short plain-text key: one line per node-role or class, explaining what it means. Example: `i0, i1, i2 — inhibitor stages (shift register cells)`. This is for the model's first read; keep it under ten lines.

6. **Separate structure from animation.** If the diagram is animated, put all static `<g>` definitions first, then a clearly commented `<!-- ANIMATION -->` section containing all `<animate>` and timing-related elements. Never interleave static shapes with animate tags.

7. **Coordinate discipline.** Use integer coordinates where possible. No trailing zeros, no unnecessary decimal precision. Round to the nearest pixel unless sub-pixel positioning is structurally required.

8. **No redundant attributes.** Omit attributes that match SVG defaults. Omit `xmlns` repetition on child elements. Omit `fill="none"` if a class already sets it.

9. **Hierarchy for complexity.** If a diagram has more than roughly 15 logical nodes, split it: produce a top-level overview diagram showing subsystems as boxes, and separate files for each subsystem's internals. Link them by shared node ids in the metadata.

10. **When modifying an existing SVG**, preserve all of the above. Do not strip metadata, flatten groups, inline styles, or reorder sections. If the existing file violates these rules, fix the violations as part of the modification.

**Strong harness rule:** if any of these conventions would make the diagram incorrect or unclear, stop and report the conflict instead of silently breaking the rule. The conventions serve the diagram; the diagram does not serve the conventions.

## Known renderer exceptions

These exceptions are required because the VS Code SVG preview renderer does not reliably apply CSS to SVG elements. Apply them in all SVG output for this project.

**Exception to rule 2 — no `<symbol>`/`<use>` for styled shapes:**
CSS cannot pierce the shadow DOM created by `<use>`, so `fill` and `stroke` classes will not apply to instanced symbols. Use direct `<rect>` elements with a shape class (e.g. `<rect class="shape-latch" .../>`) instead. `<symbol>` is only appropriate for purely geometric decorations that carry no CSS-styled fill or stroke.

**Exception to rule 3 — CSS font-weight and fill are ignored on text:**
The renderer does not inherit CSS `font-weight` or `fill` onto `<text>` elements. Always set these as inline presentation attributes on every `<text>` element:
- `font-weight="300"` for all labels (prevents thick/bold rendering)
- `fill="#111" stroke="none"` for edge name labels (dark, readable)
- `fill="<semantic-color>" stroke="none"` for value labels (use the edge's color)
- `stroke="none"` is required on all `<text>` elements — without it, the text inherits the parent `<g>`'s stroke, rendering letters as thick outlines with no fill.

**Exception to rule 3 — CSS is unreliable for text color via class inheritance:**
Do not rely on `.class text { fill: ... }` descendant selectors — they are ignored. Set `fill` directly on each `<text>` element as a presentation attribute.

---

# Part 2 — Topology-chain-cascade.svg — House Style Compilation

## Context
Reference compilation of the style conventions used in [diagrams/topology-examples/topology-chain-cascade.svg](../diagrams/topology-examples/topology-chain-cascade.svg). Captures nodes, edges, labels, spacing, path routing, and animation patterns so future diagrams in this repo can match the house style without re-deriving it from the file.

---

## 1. Canvas & Frame

- `viewBox="0 0 1380 740"`, no intrinsic width/height (scales to container)
- Root `font-family="ui-sans-serif, system-ui, sans-serif"`, `font-weight="400"`
- Background: full-bleed `<rect>` with `class="diagram-bg"`, `rx="8"` (fill `#fafafa`)
- Title centered at `(690, 30)`, `class="title-text"` — 16px, bold, `#222`
- Structural ASCII separators used between major sections: `<!-- ═══════ SECTION ═══════ -->`

## 2. Layout — Rows (y-bands) and Columns

The diagram is organized as vertical bands, each a logical row:

| Band | y-range | Contents |
|---|---|---|
| Top AND | 50–90 | `a0` pattern AND gate (center, x≈786–856) |
| Detector | 60–150 | `sbd0` (left top), `sd0` (left bottom), `sbd1`, `sd1` (right) |
| syncGate | 240–276 | syncGate (center, x≈680–750) |
| Pipeline | 280–340 | `in0 → readLatch → i0 → detectorLatch → i1` (all centered at y=310) |
| Gate row | 400–436 | `readGate` directly under `readLatch` |
| Annotation | 480–535 | yellow behavior-note box |
| Legend | 548–722 | color/description table |

Columns (pipeline x-centers): `in0≈60`, `readLatch/readGate=285`, `i0=545`, `detectorLatch/syncGate/a0≈815`, `i1=1105`, `sd1=1170`. Detector pair left of its inhibitor for i0, right of its inhibitor for i1 (mirror symmetry around pipeline).

## 3. Node Shapes

All `rect` (no symbol/use for styled shapes — renderer limitation, see CLAUDE.md exception).

| Role | Size (w×h) | rx | Class | Fill / Stroke |
|---|---|---|---|---|
| input | 80×60 | 6 | `.shape-input` | `#e0e0e0` / `#666` |
| latch | 70×36 | 6 | `.shape-latch` | `#e0f7fa` / `#00838f` (teal) |
| inhibitor | 90×60 | 6 | `.shape-inhibitor` | `#fff3e0` / `#e65100` (orange) |
| and-gate | 70×36 | 6 | `.shape-and-gate` | `#f3e5f5` / `#7b1fa2` (magenta) |
| pattern-and | 70×40 | 6 | `.shape-pattern-and` | `#e8eaf6` / `#283593` (indigo) |
| sbd0 (variant) | 110×40 | 20 (pill) | `.shape-sbd0` | `#ffebee` / `#c62828` (red) |
| sbd1 (variant) | 110×40 | 20 (pill) | `.shape-sbd1` | `#e3f2fd` / `#1565c0` (blue) |
| sd | 100×40 | 20 (pill) | `.shape-sd` | `#e8f5e9` / `#2e7d32` (green) |

Convention: **detectors are pills** (`rx=20`); **pipeline/control nodes are rounded rects** (`rx=6`). All strokes are `stroke-width: 2` on node shapes.

Note: `sbd0` and `sbd1` use different fills to distinguish left/right hierarchies even though they're the same role. This is a deliberate left/right hemisphere color cue.

## 4. Node Text

- Every node group wrapped in `<g id="node-X" data-role="..." [data-index="N"]>`
- Text classes: `.node-title` (12px), `.node-sublabel` (11px), `.node-type` (11px), all `text-anchor: middle`
- **Inline presentation attributes required** (renderer ignores CSS on text):
  - `font-weight="100"` on every label (very thin — house look)
  - `fill` set inline via a `.text-<role>` class that succeeds because it targets the `<text>` element directly
- Title color matches the node's stroke (e.g., inhibitor title is `#e65100`)
- 2-line nodes: title at `y+16`, second line at `y+30`; 3-line nodes: `y+20`, `y+35`, `y+50` from the rect top
- `in0` carries an inline sample sequence as third line: `[-1,1,1,-1]→`

## 5. Edge Classes — Semantics, Color, Style

Every edge kind is a CSS class on a `<g>` wrapper; the `<line>` or `<path>` inside inherits.

| Class | Color | Width | Dash | Arrow marker | Meaning |
|---|---|---|---|---|---|
| `.chain` | `#333` | 1.5 | solid | filled triangle | pipeline data advance |
| `.edge-connection` | `#2266aa` | 1.5 | solid | **open** V (blue) | read-port sample (old/new) |
| `.and-out` | `#283593` | 1.5 | solid | filled (indigo) | AND-gate reduction out |
| `.signal` | `#7b1fa2` | 1.5 | solid | filled (magenta) | ready/done pulse |
| `.feedback-ack` | `#7b1fa2` | 1.5 | solid | filled (magenta) | backpressure cycle closer (loop role marked by ↻ on label) |
| `.release` | `#00838f` | 1.5 | solid | filled (teal) | latch release |
| `.streak` | `#2e7d32` | 1.5 | solid | filled (green) | sd→sd same-sign chain |
| `.pointer` | `#e65100` | 1.5 | **4 3** dashed | filled (orange) | struct ref, not dataflow |
| `.future-out` | `#283593` | 1.5 | **4 3** dashed, opacity 0.5 | filled (indigo) | placeholder / not yet wired |

Conventions:
- **Dashed = non-dataflow** (pointer, future-out). The feedback-ack is a real channel send and renders solid like other dataflow edges; its loop-closing role is marked by a ↻ glyph on the label, not by stroke style.
- **Open arrowhead** distinguishes a *read* (sampling the value, not consuming it) from a *write*/transfer.

## 6. Edge Path Routing

- **All orthogonal (Manhattan).** No diagonals, no curves.
- Short horizontal chain edges use `<line>` with straight `x1→x2` (pipeline row).
- Turning edges use `<path d="M x,y L x,y L x,y ...">` — L-segments only.
- Snake routing (3+ segments) used when an edge must detour around intervening nodes, e.g. `i0-sbd0-old`: `M521,280 L521,235 L213,235 L213,75 L230,75`. Each horizontal read-edge is given its own *vertical lane* (x offsets staggered ~3–4px — 521/533, 551/563) so old/new pairs don't overlap.
- Edges leave the node face perpendicular to it; line endpoints are ~2px clear of the rect to let arrow markers render cleanly.
- Control signals routed through dedicated corridors: sync signals pass along y≈95 (above sbd) and y≈145 (above sd), readGate ack rides y=360 below pipeline.

## 7. Edge Labels

- Class `.edge-label` (12px, `text-anchor: middle`) for descriptors; `.value-label` for value annotations (`0|1`, `old`, `new`).
- Placed ~6px above horizontal edges (label y = edge y − 6) or to the side for verticals.
- **Inline attributes required** on every `<text>`:
  - `font-weight="300"` (slightly heavier than node labels)
  - `fill="#111" stroke="none"` for neutral descriptors, or `fill="<edge color>" stroke="none"` for semantic values (e.g. `#2266aa` for old/new, `#283593` for `0|1`, `#2e7d32` for streak value).
  - `stroke="none"` is **mandatory** — text inherits the parent `<g>`'s stroke otherwise and renders as hollow outlines.
- Feedback-ack label uses `font-weight="600"` + unicode `↻` prefix to visually mark it as the cycle-closer.

## 8. Edge Naming

- camelCase for signal flow: `inputToChainInhibitor`, `sbd0DoneToSyncGate`, `detectorAckToReadGate`.
- hyphen-segmented for qualified reads: `i0-sbd0-old`, `i0-sbd0-new`.
- IDs of edge groups follow `id="edge-<name>"`; edge name matches the `id` field inside `<metadata>` JSON.
- Channel names encode both endpoints (house rule from [CLAUDE.md](../CLAUDE.md)).

## 9. Arrow Markers

All defined in `<defs>`, identical geometry (`M0,0 L8,3 L0,6`, 8×6, refX=8 refY=3) varying only fill color to match edge class:
- `arrow` (#333), `arrow-blue` (#2266aa, unused, legacy), `arrow-indigo` (#283593), `arrow-green` (#2e7d32), `arrow-orange` (#e65100), `arrow-magenta` (#7b1fa2), `arrow-teal` (#00838f).
- `arrow-blue-open` is the exception: **10×8**, fill `none`, stroke `#2266aa` 1.2 — the read-port marker.

**Dynamic editor markers** (`MarkerDefs.tsx`) generate two size variants per kind:
- **md** (default): filled 8×6, open 10×8 — as above.
- **sm** (small): filled 5×4 (`M0,0 L5,2 L0,4 Z`, refX=5 refY=2), open 6×5 (`M0,0 L6,2.5 L0,5`, refX=6 refY=2.5).

`RSubstrateEdge` auto-selects `sm` when the approximate path pixel length is below **12 px** (≈ 8 × strokeWidth at 1.5) so that the arrowhead does not dominate or visually overflow a very short edge.

## 10. Metadata & Legend Layer

- `<metadata>` right after the opening tag: JSON with `nodes[]` (id, role, optional index), `edges[]` (id, source, target, kind), and optional `timing[]` (step with `t` fraction and `event` string). This is the authoritative spec; the graphics render it.
- `<desc>` after metadata: plain-text role legend, one line per node family — 6–10 lines, the model's first-read summary.
- Visible `<g id="legend">` at the bottom: header row (Color / Description), thin vertical divider at x=210, one row per edge class with a short stroked line sample next to its name. Uses `.legend-bg`, `.legend-heading`, `.legend-name`, `.legend-desc` classes.
- Annotation note box (`.behavior-note-bg` `#fff9c4`, `.behavior-note-text` `#f57f17`, `stroke="#f9a825"`) used for short behavioral call-outs.

## 11. Animation Pattern

- Single SMIL loop, `dur="27s"`, `repeatCount="indefinite"` on every `<animate>`.
- All timings expressed as **fractions of the cycle** via `keyTimes` (`0…1`), matching the fractions in `metadata.timing.steps` — so the spec and animation stay in sync.
- **Node highlight**: a white `<rect>` clone of the node (same x/y/w/h/rx) overlaid with `opacity` keyframes `0;0;0.5;0;0`, flashing on the firing moment.
- **Edge pulse**: a second copy of the edge path (stroke = edge color, width 3, `stroke-dasharray="20,9999"`) with two synchronized animations:
  - `stroke-dashoffset` from `0` to `-<pathLength>` (path length approximated in px)
  - `opacity` `0;0;1;1;0;0` gated to the travel window
- Feedback-ack gets **two** pulse copies — one `arriving` at readGate at the start of the cycle, one `departing` from detectorLatch near the end — showing it as the wrap-around.
- A small `<circle>` flash at the ack's origin (`cx,cy` at detectorLatch's edge) marks the emission moment.
- Animation block is **after** all static geometry, fenced by a big `<!-- ANIMATION -->` banner. Static and dynamic are never interleaved.

## 12. Coordinate & Attribute Discipline

- Integer coordinates throughout; sub-pixel values appear only in computed path lengths inside `keyTimes`.
- Attributes omitted when they match SVG defaults (no redundant `fill="none"` when class already declares it).
- Every structural `<g>` carries `data-role` (and `data-index` for numbered instances) — enables automated scraping/verification.

## 13. Things I Noticed That Weren't in the Prompt

1. **Two-beat read pairing.** Each inhibitor-to-detector edge is doubled (`old` + `new`) and animated on staggered fractions (0.40→0.52 for both, but visually separated by ~12px lane offsets). This encodes that detectors sample two successive values, not one.
2. **Sync-signal lanes.** `sbd0Done` rides y=95, `sd0Done` rides y=145 — dedicated horizontal corridors above each detector so signal paths never cross their source.
3. **Mirror asymmetry.** `sbd0` is red, `sbd1` is blue — same role, different color. Left/right distinction is carried by palette, not just position.
4. **Future-out stub.** `a0` has a faint, dashed, half-opacity exit edge (`.future-out`) pointing to a not-yet-existing downstream node. Pattern: draw placeholder with `opacity: 0.5` + dash to pre-commit to an interface.
5. **Pointer vs. dataflow.** `sd1→i1`, `sd1→sbd1` use `.pointer` (orange dashed) to indicate these are Go struct references captured at construction time, not runtime messages. Useful convention for hybrid dataflow/object diagrams.
6. **Loop role lives on the label, not the stroke.** The feedback-ack uses the same 1.5px solid stroke as other dataflow edges (it's an ordinary `chan int` send in code). Its cycle-closing role is signaled by the ↻ glyph prefix on its label.
7. **Values on edges are semantic, not ornamental.** `0|1` appears on `.and-out` and `.streak` lines to show the carrier type; `old`/`new` appears on read edges to indicate which of the two latched slots is being sampled.
8. **Node titles are ultra-thin (font-weight 100).** Combined with the muted-pastel fills, this keeps labels legible but visually secondary to the topology — the graph is the primary artifact.

## 14. Verification

This is a read-only compilation — no code changes. To verify:
- Open [diagrams/topology-examples/topology-chain-cascade.svg](../diagrams/topology-examples/topology-chain-cascade.svg) in VS Code preview; confirm every class/color/shape in the tables above appears as described.
- Cross-check the `<metadata>` JSON's `nodes[]` and `edges[]` against the `<g>` ids in the body; each entry should correspond to exactly one rendered group.
- Confirm timing fractions in `metadata.timing.steps` line up with `keyTimes` values in the `<animate>` elements.
