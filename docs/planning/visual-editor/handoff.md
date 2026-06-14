---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-13 — branch `task/node-runs-own-edges` IN FLIGHT, NOT merged)

`main` is at `a6fc29ea` (just merged `task/persistent-edge-emit`: per-edge persistent flag — a wire re-sends its held value with a fresh seq; includes the `d3a79e2b` stall fix where persistent wires return `Occupied()==false`).

This branch `task/node-runs-own-edges` **REVERSES direction**: move the network from the multi-bead sustained train toward a **ONE-BEAD-PER-FIRE, node-owned-wire** model. A node emits ONE bead per fire; the bead rides a wire the source node owns; the node's own goroutine advances it; no train, no per-wire pacer goroutine, no seq, no bead-count/occupancy rule. (This unwinds the sustained-bead-train and persistent-train work.)

### Commits on this branch (oldest → newest)

- `bcbc34f8` docs(spec): `docs/planning/visual-editor/node-edges-goroutine-spec.html` — tabbed spec (Model, Was (train), Target, Invariants, Migration, Open Qs) for the one-bead/node-owned model; node-1-first rollout in Migration.
- `70571cf8` feat: node 1 emits ONE bead per fire via new `Out.EmitOne` (`nodes/Wiring/ports.go`) instead of `TryEmit`→`StartTrain`. `EmitOne` calls `placeBeadSeq(v, placement, true)` — one bead, fresh seq, NO pacer/train; the walker still animates+delivers it. Both node-1 emit sites in `nodes/input/node.go` switched. `paced_wire.go` NOT edited.
- `7be25ae3` feat: node 2 (ChainInhibitor) all outgoing emits → `EmitOne`; dropped persistent flag on 2→5.
- (node 3, also ChainInhibitor) dropped persistent on 3→4 — Go emits already `EmitOne` via the shared kind.
- `7fecc889` feat: node 4 (HoldFlip) emit → `EmitOne` (line 90); dropped persistent on 4→5.

### What's done & VERIFIED

- Nodes 1–4 all emit ONE bead per fire via `Out.EmitOne` (one bead, fresh seq, no train pacer; walker still animates+delivers). Node 5 (WindowAndGate) is the sink — no outgoing edges, nothing to convert.
- All three persistent flags removed (2→5, 3→4, 4→5).
- User confirmed the full network works in the editor (one bead end-to-end across 1→2→5, 3→4→5, and the 2→1 feedback).
- `go build` + `go test -race` green throughout. `paced_wire.go` NOT edited (read-only per constraint).

### Hard constraints (carry forward)

- **`paced_wire.go` still UNEDITED** — the train apparatus (`StartTrain`/`runTrain` pacer, `trainSeq`, walker, `Recv` seq gate) is all still present, just no longer invoked by the emit path. `EmitOne` uses `placeBeadSeq(..., true)`: one bead + fresh seq + walker, no pacer.
- The wire is the source node's arc-geometry + position-over-time animation before handing the one bead to the next node — NOT a buffer/FIFO. Don't reintroduce queue framing.
- `seq` is still present (the bead carries a fresh bumped seq to pass `PacedWire`'s `Recv` gate). Full seq removal is part of the `paced_wire` conversion, not yet done.

### Next step (the remaining phase)

- **`paced_wire.go` conversion** (previously deferred, now unblocked since nothing emits trains): remove the seq gate + the walker goroutine and fold the bead's position animation into the source node's own goroutine; then remove the now-dead train machinery (`StartTrain`/`runTrain`/`trainSeq`/persistent flag handling in `loader.go` + `wire-defs.ts`). This is where the true "one bead, no seq, node owns the animation" model lands. Resolve spec Open Qs (Q2 two-source wake, Q3 loop location) here.
- Alternatively: this branch is in a coherent working state and could be merged as-is (emit path fully on one-bead) before tackling the deeper `paced_wire` conversion on a fresh branch.

### Open questions (spec)

- **Q2:** two-source wake (timer-as-channel vs wake-on-arm) — relevant when the walker folds into the node loop.
- **Q3:** loop location — generic wrapper vs per-kind `Update()`.
- Fate of persistent flag machinery (`loader.go`, `wire-defs.ts`) and `Occupied`/back-pressure once `paced_wire` is converted (both likely removed; persistent edge data already dropped).

### Carry-forward facts

- **Two bead trace kinds:** `node-bead` (interior, node-LOCAL offsets, children of the node group) and `edge-bead` (on-wire). Node geometry (center + radius + ports + interior beads) is Go-streamed; TS renders, computes none.
- **`topology/` tree tracked normally**; **`topology/view/scene.json` gitignored** (camera/labels, reconstitutes to defaults when absent).
- **Fading a load-bearing ring edge stalls the whole ring** (token dropped); unfade does NOT revive — restart re-seeds from node `1`'s Input init. EXPECTED model behavior.
- **Node editing requires Go alive** (positions Go-authoritative): if Go is stopped/crashed, NO node moves until restart.
- **Two-process editor:** extension-host changes need **Developer: Reload Window**; webview-only changes hot-reload on build (edges survive via geometry resend).
- **Runner is a prebuilt binary**, not `go run .`: the editor spawns `<repoRoot>/.wirefold-cache/wirefold` (gitignored). Webview-only changes hot-reload via the bundle watcher; `.go` changes rebuild the binary via an eager `**/*.go` watcher. First launch after a fresh checkout does a one-time `go build`. Orphaned sims from crashed sessions are SIGKILLed on launch.
- **Parser/message-kind + trace-kind parity in LOCKSTEP:** changing a TS↔Go message shape updates `messages.ts` parser AND the Go stdin-reader together. Guards: `check-message-kind-parity`, `check-trace-kind-parity`.
- **A new edit op must be forwarded in THREE places:** `messages.ts`, `handle-message.ts` `case "edit"` per-op forward, and the Go `stdin_reader`.
- **Subagent commit hygiene:** subagents have repeatedly swept incidental `topology.json` autosave churn into commits — instruct them to `git add` specific paths only, and spot-check net diffs before merge.
- **React Flow is fully removed;** `RF`-named code was vestigial and retired.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't re-propose; O(N²) follow latency.
- **Port slots are `0|1|2` per side** (top/bottom/left/right each hold at most 3 ports). The webview parser throws a `load-error` on slot 3+, blanking the whole diagram.
- **Locality invariant:** one node must NOT affect another's timing. Do not reintroduce time-window recv gating or cross-node timing dependencies.
- **Timing-as-distance is the design target:** per-node local durations expressed as distances at pulseSpeed off the one clock — human-speed + locality + one-button pause. Do NOT put this in MODEL.md.

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`): `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` + `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-message-kind-parity`, `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file for webview-only.
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
