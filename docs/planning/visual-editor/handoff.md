---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-14 — branch `task/node-runs-own-edges` IN FLIGHT, NOT merged — full-fold COMPLETE)

Context: `main` at `a6fc29ea`. This branch took the network from the multi-bead sustained train to ONE BEAD PER FIRE where each node DRIVES its own outbound bead(s) to delivery on ITS OWN goroutine — no train, no seq, no persistent flag, and now NO per-bead walker goroutine. The production bead path spawns zero goroutines; nodes drive synchronously on the one clock.

### What's DONE & VERIFIED
- Emit collapsed to one bead (StartTrain/runTrain/trainSeq/lastAcceptedSeq/persistent all deleted; Recv is plain FIFO, no dedup).
- All node kinds drive their own outbound beads: Input, HoldFlip, WindowAndGate via `Out.EmitOneDriven(ctx,v)`; ChainInhibitor (nodes 2 & 3) via place-all-then-`DriveAll` (concurrent fan-out 2→{3,5} + feedback 2→1); ReadGate via EmitOneDriven.
- Per-bead walker (`launchWalkerLocked`) + dead non-driven methods (`placeBead`, `Send`, `SendDeliverOnly`, `TryPlace`, `Out.TryEmit`/`TrySend`/`EmitOne`) DELETED. `placeBeadNoWalker` + the driven loop (`DriveBeadsToDelivery`/`DriveBeadToDelivery`/`DriveAll`, per-frame `advanceBeadLocked`) survive.
- `ReviseInFlightGeometry` keeps its arc/seg rebase but no longer relaunches walkers (driven loop re-reads live arc/seg each frame).
- User confirmed the live network (nodes 1–5: 1→2→{3,5}, 3→4→5, 2→1 feedback ring) animates correctly after each stage. go build + go test -race green throughout (8 packages).

### Key commits (this branch, oldest→newest, abbrev)
bcbc34f8 spec; 70571cf8 node1 EmitOne; 7be25ae3 node2 EmitOne+drop persistent 2→5; (node3 persistent drop); 7fecc889 node4; 033bdba5 handoff; 3a940de0 collapse to one-bead (delete train+seq); f3cac3eb remove persistent plumbing; 671f2ad2 extract advanceBeadLocked; a539cdeb node1 driven (no walker); 2d73ead4 node2 concurrent driven; 3e0051bd one-goroutine fan-out drive; 461e5caa fix node2 serial-blocking (DriveAll); 9eb4099e node4 driven; 2d387d18 node5 driven; 88fd0dc readgate driven; 4bf000be DELETE walker + dead emit paths.

### Warts / things to scrutinize (carry forward)
- `PlaceAndDrive` / `PlaceAndDriveDeliverOnly` (paced_wire.go ~164): PUBLIC methods that spawn a goroutine, used ONLY by tests (replacing the old exported test-only SendDeliverOnly). Zero production callers. A test-only public goroutine-spawner — candidate to make package-private or restructure if it bothers you.
- The walker-delete commit also added (production) `inflightBead.startedAt` + a deadline-cap on `minNext` in DriveBeadsToDelivery — added to keep geometry/anchor behavior equivalent when tests moved onto the shared driven loop. Reviewed and accepted; re-scrutinize if geometry-edit-during-flight looks off.
- Tests run CHAN-MODE and do NOT exercise the synchronous paced drive loop — green tests do not prove runtime drive behavior. Editor eyeball is the real check for any drive change.

### Open / next
- Branch is a coherent, working, verified milestone — READY TO MERGE pending sign-off (run tools/strip-branch-local-docs.sh task/node-runs-own-edges first; the spec html node-edges-goroutine-spec.html is branch-local).
- The general INPUT-vs-TIMER two-source wake was never needed: every node turned out sequential at the input level (feedback ring + anyOccupied/HasValue guards keep a node from accepting new input while its outbound bead is mid-drive). If a future topology has a node that CAN receive while driving, that wake must be designed (spec Open Q2/Q3 in node-edges-goroutine-spec.html).

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
