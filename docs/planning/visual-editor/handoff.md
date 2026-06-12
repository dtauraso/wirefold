---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-11 — on `main`, clean, NO task in flight)

Node-1 + node-2 interior features and the pulse-speed change are all **merged to main**
(latest `a2f920e7`). **BOTH node 1 (2×2 depleting/refilling buffer + animated slide) and
node 2 (single centered held bead) interiors are Go-authoritative via the shared
`node-bead` stream.** There is **NO task in flight** — the next session starts fresh on
`main`, friction-driven.

### Settled architecture (already on main — keep short)

Go-authoritative per-goroutine model: one clock; node-move + fade via `MoveDispatch`
(zero central fan-out); node/port position, edge-curve geometry, and bead positions all
Go-authoritative and streamed; TS renders Go's stream and computes no geometry (guard:
`check-ts-computes-no-geometry`). TS owns only the SCENE (camera, gitignored
`topology.scene.json`). **Go owns the node and ALL its children** (body, ring, interior
beads) and their geometry; TS renders Go's stream and sends CRUD.

### Earlier work already merged to main (background — one line each)

1. **Scene split:** `topology.json` = diagram (tracked) / `topology.scene.json` = scene
   (gitignored, camera/labels); `topology.json` no longer skip-worktree.
2. **Deterministic `view.nodes` serialization** (idempotent load→save, kills churn).
3. **Reload gating:** external `topology.json` change reloads + restarts Go only on
   STRUCTURAL change, not view-only (`structuralKey`).
4. **Node rename** `in08`/`i0`/`i1` → `1`/`2`/`3` (edge labels
   `1To2`/`2To3`/`2FeedbackTo1`).
5. **Animated bead colored by value + torus** (`VALUE_BEAD_STYLE`).
6. **Removed per-node value-display overlay.**
7. **4 robustness fixes:** Trace close race (no more "send on closed channel" panic —
   stop-signal, not `close(ch)`); empty-kind save guard (`flowToSpec` refuses to write
   a node with empty type); NUL-byte → ` ` delimiter in `save.ts`; geometry resend
   (`{type:"resend"}` on webview ready when Go already running → edges survive
   hot-reload/remount).

### THE NODE-2 FEATURE (merged) — interior held-value bead

Node `2` (ChainInhibitor) interior shows its **HELD VALUE as a single centered bead**,
reusing the node-1 infra (`node-bead` trace kind, the per-node `InteriorSlotBead`
renderer, the value→style convention). What shipped:

- **One bead at the node center** (offset `0,0,0`), value-colored (0 = white/black,
  1 = black/black). `present = (held != -1)`, so the interior is **EMPTY when `held=-1`**
  — i.e. before node 2 receives its first value from node 1. Held path: `-1 → 0 → 1 → 0 …`.
- **Go emits via an injected `EmitHeldBead func(held int)` closure** (mirroring how Input
  gets `EmitNodeBeads`). Called once at startup (`held=-1`, `present=false`) and on each
  held change ONLY — only on an actual change, no per-tick spam. The inhibitor's
  firing/forwarding logic is unchanged.
- **No TS change needed:** the existing node-bead renderer (`InteriorSlotBead` per
  `GraphNode`) handles a single centered bead and hides the absent slots.
- `topology.json`: node `2` `data.state.held` changed `0 → -1` so node 2 starts with no
  value (empty interior).

### THE NODE-1 FEATURE (already merged to main) — interior depleting/refilling buffer + activity animation

Node `1` (Input) interior is a depleting/refilling **double-buffer with an animated
interior, fully Go-authoritative.** What shipped:

- **Trace kinds renamed/added:** the on-wire bead trace kind `position` → `edge-bead`;
  a new `node-bead` trace kind for interior beads (keyed by node id + row/col; payload =
  node-LOCAL offset + value + present).
- **Node 1 is a pure double-buffer:** action (working, bottom row) + backup (top row),
  each from spec init `[1,0]` = 4 beads. It PEEK-sends the last bead of working (no pop)
  and POPS only on node 2's feedback `1` (`0` holds); when working empties it refills
  from backup and restocks backup. NO seed/bootstrap — the first peek-send naturally
  starts the ring. At rest the buffer shows the full 4.
- **Interior beads render as a 2×2 grid** inside the node body, value-colored
  (0 = white/black, 1 = black/black) sphere + torus, torus-aware spacing (fit inside the
  sphere, small even gaps). They are CHILDREN of the node group at Go-streamed local
  offsets, so they ride the node on drag (no re-emit needed).
- **Node body/ring geometry is Go-owned:** Go streams the node center AND radius
  (node-geometry event gained a `radius` field); TS reads both from the stream with a
  dims-based fallback only in the pre-first-emit startup window. TS computes no node
  geometry in steady state.
- **Animated refill SLIDE:** when working empties, the top row slides DOWN into the
  working position, clock-paced at human speed (pause-aware via the clock injected into
  the Input node), then the new top row appears. `interiorSlideDurationMul` is now `1.0`,
  so the slide runs at the base pulse speed with no extra multiplier.

**Uniform pulse speed (merged):** the one constant `CurveParamPulseSpeedWuPerMs = 0.04`
wu/ms (was 0.08 — half speed) drives ALL bead animation — wire beads AND node 1's refill
slide — at one constant speed. Note: `curve-params.ts` is a GENERATED mirror of Go's
`CurveParam*` constants, but TS does **not** actually consume the pulse-speed constant —
Go owns timing and streams bead positions; TS plots. So pulse speed is driven entirely by
the Go constant; the TS mirror is unused/vestigial (minor dead-mirror surface, not
cleaned up).

`topology.json`: node `1` `init` is `[1,0]` (so end-pop sends 0 then 1). Active network
nodes `1`/`2`/`3`, edges `1To2`, `2To3`, `2FeedbackTo1` (feedback ring).

### NEXT

Friction-driven, **no task in flight.** Next session starts fresh on `main`; justify new
work from real editor-use friction logged in `session-log.md`.

### Carry-forward facts

- **Two bead trace kinds:** `node-bead` (interior, node-LOCAL offsets, children of the
  node group) and `edge-bead` (on-wire). Node geometry (center + radius + ports +
  interior beads) is Go-streamed; TS renders, computes none.
- **`topology.json` is tracked normally** (not skip-worktree). The gitignored
  `topology.scene.json` holds camera/labels and reconstitutes to defaults when absent.
- **Fading a load-bearing ring edge stalls the whole ring** (token dropped); **unfade
  does NOT revive** — restart re-seeds from node `1`'s Input init. EXPECTED model
  behavior, not a bug.
- **Node editing requires Go alive** (positions Go-authoritative): if Go is
  stopped/crashed, NO node moves until restart.
- **Two-process editor:** extension-host changes
  (`extension.ts`/`handle-message.ts`/`messages.ts`/`runCommand.ts`) need **Developer:
  Reload Window**; webview-only changes hot-reload on build (and keep edges via node
  geometry resend). See `feedback_two_process_editor_reload`.
- **Parser-parity trap:** when changing a TS↔Go message shape, update `messages.ts`
  parser AND the Go stdin-reader in lockstep; trace-kinds + message-kinds tracked in
  LOCKSTEP. Guards: `check-message-kind-parity`, `check-trace-kind-parity`. See
  `feedback_schema_parser_parity`.
- **Subagent commit hygiene:** subagents have repeatedly swept incidental
  `topology.json` autosave churn into commits — instruct them to `git add` specific
  paths only, and spot-check net diffs before merge.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't
  re-propose; O(N²) follow latency.

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`):
  `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` +
  `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-message-kind-parity`,
  `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file
  for webview-only.
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
