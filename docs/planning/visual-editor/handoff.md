---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-12 — on `task/persist-geometry-from-go-stream`, IN FLIGHT, DESIGN/SPEC phase)

Active branch **`task/persist-geometry-from-go-stream`** — IN FLIGHT and in the
**DESIGN/SPEC phase**. The design **SPEC is written; implementation is NOT started**
(open items still pending). Branched off `main` (latest `main` carries all the merged
work listed below). **Not merged.**

**NEXT for a fresh session:** open the branch-local spec page
`docs/topology-tree-go-owned/index.html` (7 tabs + 4 SVG diagrams), settle the OPEN
items — **especially the exact tree shape** — with the user, THEN build phase 1. This
is a foundational change; confirm the tree layout before writing any code.

### Settled architecture (already on main — keep short)

Go-authoritative per-goroutine model: one clock; node-move + fade + port-anchor via
`MoveDispatch` (decentralized key→inbox, zero central fan-out); node/port position,
edge-curve geometry, node radius, and interior beads all Go-streamed; TS renders Go's
stream and computes no geometry (guard: `check-ts-computes-no-geometry`). TS owns only
the SCENE (camera, gitignored `topology.scene.json`). One constant pulse speed
`CurveParamPulseSpeedWuPerMs = 0.04` wu/ms (half), uniform across ALL bead animations
(wire beads AND node-1 refill slide).

### Earlier work already merged to main (background — one line each)

1. **Scene split:** `topology.json` = diagram (tracked) / `topology.scene.json` = scene
   (gitignored, camera/labels); `topology.json` no longer skip-worktree.
2. **Deterministic `view.nodes` serialization** (idempotent load→save, kills churn).
3. **Reload gating:** external `topology.json` change reloads + restarts Go only on
   STRUCTURAL change, not view-only (`structuralKey`).
4. **Node rename** `1`/`2`/`3` (edges `1To2`/`2To3`/`2FeedbackTo1`).
5. **Animated bead colored by value + torus; removed per-node value-display overlay.**
6. **4 robustness fixes:** Trace close race (stop-signal, not `close(ch)`); empty-kind
   save guard; NUL-byte → ` ` delimiter; geometry resend on webview ready.
7. **Node-1 interior:** depleting/refilling 2×2 double-buffer, value-colored sphere+torus,
   Go-streamed local offsets (children of node group), animated refill SLIDE; peek-send /
   pop-on-feedback, NO seed/bootstrap. Node body/ring center+radius Go-owned.
8. **Node-2 interior:** single centered held-value bead, empty when `held=-1`, emitted via
   injected `EmitHeldBead` closure on held-change only.
9. **Pulse speed 0.04 + slide unified** at the base speed (`interiorSlideDurationMul=1.0`).
10. **Dead-code sweep (−78 lines); `strip-branch-local-docs.sh` scans all of `docs/`.**

### PORT-DRAG (merged — with the gap THIS branch addresses)

You can drag a **connected** edge port along its node's ring to reposition it; the edge
follows. `Port.anchor` is wired through Go (`portDir` uses `normalize(anchor)`, applied via
`MoveDispatch`); a new `port-anchor` edit op flows webview → handle-message → Go stdin.
**KNOWN GAP:** the dragged anchor does **NOT survive reload** — persistence currently
flows through TS (`flowToSpec` serializes editor node data; Go only READS
`topology.json`, never writes it), so the anchor is never saved. That gap is the **seed
of THIS branch's redesign.**

## THIS BRANCH — the redesign (DESIGN DONE, build NOT started)

**GOAL:** make **Go own persistence**, not just the runtime model. Today TS is the sole
writer of `topology.json` (a VS Code custom text editor bound to the document); the editor
watches `onDidChangeTextDocument` and on an external/structural change RELOADS the webview
+ RESTARTS Go (kill + respawn) — because Go has no in-process reload and an external change
carries no diff. This loops/jolts whenever anything but the editor writes the file.

**THE PLAN** (full design in the branch-local spec page
`docs/topology-tree-go-owned/index.html` — 7 tabs + 4 SVG diagrams; READ IT for detail):

1. Replace monolithic `topology.json` + `topology.scene.json` with a **DIRECTORY TREE** of
   small files + nested dirs:
   `topology/nodes/<id>/{meta,data,inputs/<port>,outputs/<port>}.json`,
   `topology/edges/<id>.json`, `topology/view/{nodes/<id>,fades,scene}.json`. A CRUD touches
   only the file(s) it changes (surgical writes + clean git diffs).
2. **Go READS the tree at startup** (a tree-walker replaces the single `os.ReadFile`),
   builds the model, streams it. **Go WRITES the tree on each CRUD** (node-move →
   `view/nodes/<id>`; port-anchor → `nodes/<id>/{inputs|outputs}/<port>`; wire create/delete
   → `edges/<id>`; fade → `view/fades`; scene → `view/scene`). **Go is the SOLE writer.**
3. Editor becomes a **WEBVIEW CLIENT of Go** (not a custom text editor): renders Go's
   stream, sends CRUD, never serializes geometry, never watches the document. **LAUNCH =** a
   COMMAND ("Topology: Open Editor" / right-click the `topology/` folder) opening a
   `WebviewPanel` — the `customEditors` registration is removed (no single document to bind
   to). No manifest.
4. **REMOVED entirely:** `onDidChangeTextDocument` handler, `structuralKey` gating,
   `send()`-reload, the `stopAndAwait()`+`run()` RESTART, `lastAppliedVersion` own-save
   suppression, `customEditors`, and TS's `flowToSpec`/save path for geometry. The
   React-Flow vestige (`RFNode`/`RFEdge`, `flowToSpec`/`specToFlow` — React Flow is fully
   removed; names are leftovers from the 2D→R3F cutover) is renamed/retired by this work.

**BUILD PHASES:** (1) tree format + Go tree-reader; (2) Go tree-writer on CRUD; (3) editor
command-launch + delete custom-editor/reload/restart/save-path; (4) fold scene into `view/`.

**OPEN ITEMS to settle BEFORE building** (in the spec's Open-questions tab):
- Exact **tree shape** (meta-vs-data split; one-file-per-port vs single `inputs.json`
  array; key names).
- First-open read (editor reads tree vs relies on Go's startup stream).
- External hand-edit handling (manual relaunch vs a later in-process reload op — follow-up).
- gitignore of `view/scene.json`.

### NEXT

Open the spec page, settle the OPEN items (tree shape first) with the user, then build
phase 1. Foundational change — confirm the tree layout before coding.

### Carry-forward facts

- **Two bead trace kinds:** `node-bead` (interior, node-LOCAL offsets, children of the
  node group) and `edge-bead` (on-wire). Node geometry (center + radius + ports + interior
  beads) is Go-streamed; TS renders, computes none.
- **`topology.json` tracked normally** (not skip-worktree); **`topology.scene.json`
  gitignored** (camera/labels, reconstitutes to defaults when absent).
- **Fading a load-bearing ring edge stalls the whole ring** (token dropped); unfade does
  NOT revive — restart re-seeds from node `1`'s Input init. EXPECTED model behavior.
- **Node editing requires Go alive** (positions Go-authoritative): if Go is stopped/crashed,
  NO node moves until restart.
- **Two-process editor:** extension-host changes need **Developer: Reload Window**;
  webview-only changes hot-reload on build (edges survive via geometry resend). THIS
  redesign removes the reload/restart loop. See `feedback_two_process_editor_reload`.
- **Parser/message-kind + trace-kind parity in LOCKSTEP:** changing a TS↔Go message shape
  updates `messages.ts` parser AND the Go stdin-reader together. Guards:
  `check-message-kind-parity`, `check-trace-kind-parity`. See `feedback_schema_parser_parity`.
- **A new edit op must be forwarded in THREE places:** `messages.ts`, `handle-message.ts`
  `case "edit"` per-op forward, and the Go `stdin_reader`. The port-anchor bug was a missing
  handle-message forward.
- **Subagent commit hygiene:** subagents have repeatedly swept incidental `topology.json`
  autosave churn into commits — instruct them to `git add` specific paths only, and
  spot-check net diffs before merge.
- **React Flow is fully removed;** `RF`-named code (`RFNode`/`RFEdge`/`flowToSpec`/
  `specToFlow`) is vestigial and retired by this branch.
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
