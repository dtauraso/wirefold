---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-12 — on `task/persist-geometry-from-go-stream`, IMPLEMENTATION COMPLETE)

Active branch **`task/persist-geometry-from-go-stream`** — IMPLEMENTATION COMPLETE
(all 4 phases built + verified). **NOT yet merged. NOT yet exercised in the live editor.**
Branched off `main`.

### Settled open items (all matched spec defaults)

- **One-file-per-port** (not a single `inputs.json` array).
- **Split meta.json / data.json** per node.
- **First-open** relies on Go's startup stream: Go emits a `{"kind":"spec",...}` line via
  `EmitSpecLine` in `nodes/Wiring/loader.go`; consumed by `tryParseSpecLine` in
  `runCommand.ts` → `"load"` post.
- **`view/scene.json` gitignored.**

### Tree shape (settled & built)

```
topology/
  nodes/<id>/meta.json
             data.json
             inputs/<port>.json
             outputs/<port>.json
  edges/<id>.json
  view/nodes/<id>.json
       fades.json
       scene.json   ← gitignored
```

### Phases built

1. **dd02fbb4** — `loadTree` tree-reader + `topology/` fixture + round-trip test (`loader_tree.go`).
2. **dd4f831e** — `tree_writer.go`: Go persists each edit op to the tree
   (`writeViewNode`/`writePort`/`mergeFades`, atomic temp-rename); `applyEdit` gains
   `treeRoot`; `main.go` detects tree mode.
3. **df2a90e5** — command-launched `WebviewPanel` ("Topology: Open Editor" cmd +
   explorer/context menu); `customEditors`/`onDidChangeTextDocument`/`structuralKey`/
   restart-loop removed; Go `EmitSpecLine` on startup.
4. **212192fc** + the final flowToSpec-retire commit — dead spec-save path deleted
   (`save.ts` `performSave`/`scheduleSave`, `flow-to-spec.ts`, `structural-key.ts`);
   scene/camera persisted via new edit op `op:"scene"` → Go `writeScene` →
   `view/scene.json`; `"run"` message is now spec-less; `flowToSpec` fully retired.

### Verification done

`go build`, `go test -race ./nodes/Wiring`, `tsc --noEmit` (0 errors), `npm run build`,
vitest 69/69, guards (`no-await-on-bridge`, `message-kind-parity`, `trace-kind-parity`,
`ts-computes-no-geometry`) all green.

### NEXT for a fresh session

Exercise in the **LIVE editor**: Developer: Reload Window → "Topology: Open Editor" on the
`topology/` folder. Confirm:

- Panel opens; Go reads the tree and streams geometry.
- Edits (node-move, port-anchor, fade, wire create/delete) round-trip to disk under
  `topology/`.
- Scene/camera persists to `view/scene.json`.
- **Port-anchor survives reload** (the original gap that seeded this branch).

On sign-off: run `tools/strip-branch-local-docs.sh task/persist-geometry-from-go-stream`
and merge to main.

### Remaining follow-ups (deferred)

- **External hand-edit handling** (manual relaunch vs a future in-process reload op) —
  still deferred.
- **`topology.json` monolith** — file-mode read path kept; can be removed in a later
  cleanup once the tree path is confirmed in the live editor.

### Settled architecture (already on main — keep short)

Go-authoritative per-goroutine model: one clock; node-move + fade + port-anchor via
`MoveDispatch` (decentralized key→inbox, zero central fan-out); node/port position,
edge-curve geometry, node radius, and interior beads all Go-streamed; TS renders Go's
stream and computes no geometry (guard: `check-ts-computes-no-geometry`). TS owns only
the SCENE (camera, gitignored `view/scene.json`). One constant pulse speed
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

### PORT-DRAG (merged — gap now closed by this branch)

You can drag a **connected** edge port along its node's ring to reposition it; the edge
follows. `Port.anchor` is wired through Go (`portDir` uses `normalize(anchor)`, applied via
`MoveDispatch`); a `port-anchor` edit op flows webview → handle-message → Go stdin. The
anchor previously did **not** survive reload (TS was the sole writer; Go only READ
`topology.json`). This branch closes that gap.

### Carry-forward facts

- **Two bead trace kinds:** `node-bead` (interior, node-LOCAL offsets, children of the
  node group) and `edge-bead` (on-wire). Node geometry (center + radius + ports + interior
  beads) is Go-streamed; TS renders, computes none.
- **`topology/` tree tracked normally**; **`topology/view/scene.json` gitignored**
  (camera/labels, reconstitutes to defaults when absent).
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
  `specToFlow`) was vestigial and is retired by this branch.
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
