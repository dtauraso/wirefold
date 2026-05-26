# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-25, task/editor-3d-plan)

**Active branch:** `task/editor-3d-plan`. Branched from main. This is a
**planning/design branch** for making the visual editor 3D. Planning docs only
so far; **NO implementation code yet**. NOT merged to main.

### Why 3D

Not cosmetics. The topology genuinely has depth — the wire structures (inhibitor
chain, rings, lateral-inhibition lattices) have real geometry that the current 2D
React Flow canvas flattens into misleading edge crossings and false adjacencies.
The move is about **representational honesty**: make the rendered structure match
the actual structure.

### Governing principle (most important — drift guard)

**Interaction CONTROL is substance, not medium.** This is a **classification
clause** of CLAUDE.md's medium-vs-substance rule, NOT a competing rule. One
rulebook, correctly applied.

Decision procedure:

1. Is this rendering/plumbing, or control over the system?
2. Rendering → industry default (react-three-fiber: yes).
3. Control → substance → design from need → apply the **recoverable-by-device
   test**: if a better input device does NOT restore a lost capability without
   changing the design, the loss is baked into the design → wrong industry
   pattern-match → REJECT.

`drei`'s `OrbitControls` FAILS this test (a SpaceMouse still leaves a fixed pivot
and locked roll — the loss is in the design) and must **NOT** be adopted.
Adopting R3F (medium, yes) does not imply adopting OrbitControls (substance, no).

This principle is also saved in
[`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md)
(rides to main on merge). Full design lives in
[3d-editor.md](3d-editor.md) (branch-local — does **not** ride the merge).

### Working mode

David drives the design problem-by-problem (**ONE problem at a time**); the
assistant records each resolution into `3d-editor.md`. Do not batch or get ahead.

### Resolved so far (documented in 3d-editor.md)

- **Problem #1 (DOF mismatch — 2-DOF input vs 6 DOF):** resolved. Pointer **drag
  reserved entirely for rotation** (pivot = the clicked point at its depth; axis
  in the screen plane perpendicular to the local drag segment; magnitude
  proportional to arc length; a curved drag sweeps the axis; covers 2 in-plane
  rotational DOF). Screen-plane spin/roll → a slider. Translation off the drag:
  X/Y = a floating pan pad summoned under the cursor; Z = ^/v hold buttons.
  **Native target = a 6-DOF input (SpaceMouse)** where all click-tricks vanish;
  **trackpad multitouch = good fallback**; **bare-2D-mouse click-tricks**
  (movement-vs-dwell gesture discrimination, ~200 ms pad dwell, ~4–8 px slop) =
  last-resort fallback, NOT the model. Open conventions (not blockers): which
  sign `^` is (dolly toward/away); empty-space pivot (what a drag rotates about
  when nothing was clicked).
- **Problem #2 (depth ambiguity):** resolved. Primary = **active motion** (dolly
  size-change + rotation lateral parallax; depth from control). Plus the standard
  graph-viz convention adopted as **pure medium**: node **BODY** = a 3D primitive
  (sphere/rounded solid, orientation-invariant, shades for depth) carrying
  kind/ports; **LABEL** = a separate billboarded text (hover/nearest/selected
  LOD). Depth cues by strength: occlusion > motion parallax > relative size >
  shading > optional fog. Color and shape are reserved for DATA, never depth.

### Open problems (next session works these one at a time, recording each into 3d-editor.md)

- **#3 occlusion** — nodes/edges hide behind each other; can't see/click what's
  behind. (Natural next: it's your strongest depth cue but also hides the ports
  needed for #4.)
- **#4 port picking** — selecting a specific small, possibly-occluded port to
  wire (click A, click B model already chosen; picking-under-occlusion is open).
- **#5 layout-derivation coverage** — does the structure→coordinate function
  place real, irregular/mixed topologies, or fall back to manual 3D placement?
- **#6 disorientation** — free rotation/dolly leaving the camera lost; no
  inherent "up"/home.
- **#7 label/panel legibility** — pseudo panels + red validation flags rotating
  edge-on/overlapping. (PARTLY addressed by #2's billboarded-label decision.)
- **#8 pulse-animation legibility** — pump-driven pulses on edges pointing into
  the screen foreshorten/occlude.
- **#9 rendering scale** — many nodes + 3D edges + transparency + text
  performance.
- **#10 input-device variance** — gesture bindings across
  mouse/trackpad/SpaceMouse/touch (ties to the device hierarchy in #1).

### Next concrete step

Take **Problem #3 (occlusion)**.

### Separate deferred task (paused — NOT this branch)

Branch `task/inhibitright-pseudo` exists on origin: **InhibitRightGate pseudo-text
projection** (same Input/ReadGate/ChainInhibitor pattern). Params L/R; semantic
"L pass / R inhibit" → result = `Left==1 && Right==0`. Steps: `cmd/pseudo`
subcommand (render/save) + `nodes/inhibitrightgate/SPEC.md` `hasPseudo:true` +
`handle-message.ts` handler + Go template regen of `node.go`. **Watch:** apply
the ChainInhibitor OutMulti handle-matching lesson (suffix-strip ToNext0/ToNext1
→ base ToNext) if InhibitRightGate has multiple outputs. Paused while 3D planning
is in flight.

### Key files

- [3d-editor.md](3d-editor.md) — full 3D design (branch-local; does NOT ride the merge)
- [`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md) — control-is-substance anti-drift principle (rides to main)
- `tools/topology-vscode/src/webview/rf/` — current 2D React Flow editor (node registry, `SubstrateEdge.tsx`, RF store) that 3D will replace with react-three-fiber + drei
- `pump.ts` — pump firing + pulse-animation logic; stays put (only the geometry pulses travel over changes)
- `tools/topology-vscode/src/schema/parse-spec.ts` — node position model (gains `z`) + 3D port-anchor model when implementation begins

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel

### Substrate model contract (stable)

See [MODEL.md](../../MODEL.md#slot-phase-lifecycle). Unchanged by the 3D move —
going 3D is a medium change; the Go substrate, slot-phase/AND-gate/backpressure
model, and `pump.ts` firing logic stay untouched.

## Dev-loop

This branch is planning-only — no build needed yet. When implementation begins:
After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change (deferred branch): `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored
to the state you're leaving the branch in, and commit on the active branch (main
if no task is in flight). Do not rely on chat history; the next AI may be a fresh
model with no transcript. The rendered handoff must itself contain this same ALWAYS
clause so the loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the
structural source of truth; update the template when an invariant changes.
