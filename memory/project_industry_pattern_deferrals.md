---
name: Industry-pattern review deferred items
description: Visual-editor gaps surfaced by the 2026-05-03 industry-pattern review, re-scoped 2026-07-05 against the current buffer/Three.js architecture — surface them when relevant friction appears
type: project
---

The 2026-05-03 industry-standard-pattern review (entry in
`../docs/planning/visual-editor/session-log.md`) produced a coverage
matrix and triage. Quick wins (MiniMap, fit-view keybindings,
rounded snake corners, port-reject UI cue) shipped on
`task/industry-pattern-review` (commit `689fd7d`). The rest were
deferred per the post-v0 friction-driven posture.

**Re-scoped 2026-07-05** against the post-content-buffer-erase
architecture (Go owns all state → one binary content buffer → TS
decodes + renders in Three.js; `buffer-scene.tsx`). The original
sizes assumed the react-flow render path, which is GONE. Net effect:
render-only items got CHEAPER (data already streams on the buffer);
stateful/interaction items got MORE EXPENSIVE (no TS-side shortcut —
Go owns state, and three foundations react-flow gave for free are
absent: multi-node selection, undo, edge waypoints/auto-layout lib).

**Deferred — bring up when matching friction appears (new size in bold):**

- *Edge display labels* (M → **S**). `edge.label` is already plumbed
  end-to-end — the buffer's trailing `EdgeLabel` section (readers in
  `buffer-decode.ts` `edgeLabel()`), but currently consumed ONLY by
  the `.probe` logger (`buffer-log.ts`), not drawn. Needs only a
  render layer: reuse the node-label pattern (`BufferLabelProjector.tsx`
  + ThreeView DOM pills) projecting edge midpoints. No Go work.
  Surface when the user asks "which channel is this?".
- *Hover tooltips* (XS → **XS**). Hover is Go-owned (node+port);
  buffer node/port `Hovered` column already drives the hover ring
  (`gesture.go` `updateHover`, `SelectionHighlight.tsx` `HoverHighlight`).
  Add a DOM overlay reading that column via the projector — same pill
  pattern. Surface when the user squints at a truncated sublabel.
- *Export to PNG* (XS → **S**). react-flow `toPng` freebie is gone.
  Single WebGL `<Canvas>` → `toDataURL`, BUT node-label pills are a
  separate DOM-overlay layer that must be composited in. Surface when
  the user wants to share/paste a topology image.
- *Export to SVG* (XS → **M**). react-flow `toSvg` gone; no retained
  primitive scene-graph — would re-derive geometry from buffer blocks.
  Drop unless vector output is specifically wanted.
- *Properties inspector sidebar* (M → **M**, scaffolding exists). No
  general inspector; the old `state/ops/rename.ts` was DEAD react-flow-era
  code (unmounted, still imported reactflow) — since deleted. But `RuleEquationPanel.tsx`
  (the live equation/lock DOM-portal panel, mounted in `main.tsx`) is
  the clone target, and it uses the `edit-update` lock-style wire path a
  generic inspector would extend. Surface when editing arbitrary `props`
  becomes a real workflow.
- *Copy / paste / duplicate* (M → **L**). Node selection is
  SINGLE-select only (`md.selected` = one id, `node_move.go`). Needs a
  new Go-side multi-node selection set + buffer column + TS highlight
  loop — none exists. (Multi-select exists only for polar-equation
  LOCKS via `selectedLocks`/`SelectLock`, a partial pattern to borrow,
  not reuse.) Surface when the user duplicates subgraphs by hand.
- *Multi-node alignment guides* (S → **L**). Depends on multi-node
  selection (absent) AND alignment guides (absent — `NavGuides.tsx` /
  `SelectedEquationGuides.tsx` are unrelated polar/equation overlays).
  Two greenfield pieces. Surface when guides "stop working" while
  moving a selection.
- *Snap to other nodes' edges* (S → **M-L**). Positions are the polar
  model, Go-computed; only "snap" today is polar ring-anchor snapping
  for connected ports (`port_geometry.go`). Edge-flush snapping is a new
  Go geometry pass, not a TS tweak. Surface when guides catch centers
  but the user wants flush left/right edges.
- *Drag-stop undo coalescing* (S → **L**). There is NO undo/redo
  anywhere — no history, no journal (grep-confirmed). This is building
  undo from scratch, not coalescing an existing one. Natural boundary is
  the gesture FSM drag phase (`gesture.go` `gestDragging` → pointer-up).
  Surface when a multi-node drag takes N undo presses.
- *Auto-layout (dagre / ELK one-shot)* (M-L → **L**). No layout lib
  present. Positions are polar + Go-owned, so this is a new Go geometry
  pass feeding the center stream — dagre/ELK output must map onto the
  polar frame, not drop in raw. Surface when a larger spec draws
  hand-placement complaints.
- *Auto-routing with obstacle avoidance* (L → **L**, unchanged).
  Edges are straight `LineCurve3` tubes (`EdgeTube.tsx`), no waypoints.
  Router output needs new waypoint columns in the buffer edge stride +
  Go-side routing. Prefer ELK or libavoid-js. Surface when "edges
  crossing through nodes" gets logged.
- *Edge bend points / waypoints* (M-L → **L**). Same blocker as
  auto-routing — every edge is one straight segment; no waypoint columns
  in the edge stride. Manual waypoints need the same new plumbing.
  Surface when three preset routes don't suffice for one edge.

**Keybindings registry** (placeholder → **S** greenfield). Shortcuts
are hardcoded scattered `keydown` listeners (`"f"` fade toggle in
`ThreeView.tsx`; equation-panel keys). No fit-view binding exists —
"home" is a button (`HomeButton`). A small registry is genuinely small.

**Why:** the review identified these as real gaps vs. industry
norms, but the post-v0 rule is don't open branches preemptively —
wait for friction. The risk without this memory is forgetting the
analysis exists and re-doing it next time the topic comes up.

**How to apply:** when the user says something that maps onto one
of the bullets above (e.g. "I keep redoing the same wiring",
"which channel is this edge?", "this drag took five undos"), name
the matching deferred item and offer to open the corresponding
task branch. Don't volunteer the list unprompted.

**The cheap render-only pair (edge labels S, hover tooltips XS) are
now the clear high-value/low-cost items** — both just need a DOM-overlay
render layer over already-streaming buffer columns. Everything else is
L, gated on one of three missing foundations: multi-node selection,
undo, or edge waypoints/auto-layout lib.

**The old quick wins were placeholders for canonical replacements —
but most are now GONE, not merely placeholders.** The content-buffer
erase deleted the files they lived in:

- `AnimatedEdge.tsx` — GONE. Rounded snake corners went with it; edges
  are straight `LineCurve3` tubes in `EdgeTube.tsx` now.
- `flashRejectedHandle` (was in `app.tsx`) — GONE. `app.tsx` itself is
  gone (entry is `main.tsx`). A general validation/user-feedback channel
  is still unbuilt.
- `f` / `shift+f` keybindings — the `cmd-z` effect that hosted them is
  gone; only a hardcoded `"f"` fade-toggle listener remains in
  `ThreeView.tsx`. Fold into a keybinding registry when a third shortcut
  shows up.
- `MiniMap` — GONE (no match).

Dead-code cleanup candidates surfaced during this re-scope (still compiled,
unmounted, react-flow-era) and since deleted: `webview/state/ops/rename.ts`, the
`webview/state/adapter/*` tree, and the webview's react-flow-era edge/node data
types file (zero importers, deleted alongside the dead wire-prop kind chain and
its barrel/color-map modules — see task/code-smell-audit-fixes).
