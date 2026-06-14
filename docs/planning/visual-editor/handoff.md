---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-13 — branch `task/sustained-bead-train` IN FLIGHT, NOT merged)

Branch `task/sustained-bead-train`, **not merged**. `main` is at `0c25f1bf`. This branch
builds a **sustained bead train** feature — a foundational change to the wire/send/recv
model. **NOT ready to merge** (open optional cleanups below; all known blocking issues resolved).

### Commits (oldest → newest)

- `b34bce0c` multi-bead `PacedWire` — carries a slice of concurrent in-flight beads, FIFO
  delivery, per-bead `t` preserved across geometry edits.
- `8b471087` paced 2s/400ms emission train at the send layer: a fire starts a clock-paced
  train (places the value every `beadSpacingMs=400` for `trainDurationMs=2000`; first bead
  immediate; re-fire refreshes value+window; pacer freezes on pause).
- `f020cdef` per-bead trace id (`bead` field); webview renders N beads/edge (pulse store
  keyed by `(edge, bead)`).
- `d0314540` strip consume-gates — all node sends are fire-and-forget `TryEmit`. The
  `Gated/TrySend/WaitConsumed` handshake's `if !WaitConsumed(){return}` was killing gated
  nodes after one fire.
- `33cce656` clock-gate Recv: a node consumes at most one bead per `recvGateMs=2000`
  window, dropping the rest of a train — an N-bead train collapses to ONE fire (refractory
  `lastConsumed` on the wire, active-elapsed clock).
- `af77fcc4` `WindowAndGate` window+dwell run on the pause-aware clock (the one human-speed clock) (injected
  `Now func() time.Duration`, like `EmitGeometry`).
- `c4e624f7` + `a8ef268e` rename `AndGate → WindowAndGate` (`c4e624f7` half-committed — left
  in-file edits uncommitted; `a8ef268e` fixed HEAD).
- `23de196c` reverted `7ff9cb87` (a MODEL.md locality paragraph the user asked to remove —
  do NOT re-add to MODEL.md).
- `bf0d74bd` `docs/planning/visual-editor/timing-spec.html` — tabbed timing-model reference
  (Model, Constants, Timers-current, Target-distance, Node-processing).
- `144541c7` docs(timing): one-clock language; drop node-processing-distance direction (timing-spec.html).
- `9551cc89` docs(timing): uniform pause-aware ✅ cells in timers table.
- `9d75d8f9` fix(windowandgate): fixed 120wu (~3000ms) coincidence window — decoupled from wire latency; node 5 now fires reliably (was issue #2). VERIFIED via headless trace (node 5 fires on left+right coincidence, ToPassed=1).
- `6014b68b` fix(nodes): poll loops (windowandgate/readgate/holdflip) park on the one clock via injected `WaitUntil` closure — pause-aware, not wall-clock `time.After` (was issue #3).
- `891c6f8c` fix(wire): receiver dedups by per-train seq, not time window — removes the `recvGate==trainDuration` cross-node timing coupling (the core locality fix). `recvGateMs`/`RecvGateMs` deleted. `inflightBead` gains `seq`; `PacedWire` gains `trainSeq` + `lastAcceptedSeq`; `delivered` is now `[]deliveredBead{val,seq}`; Recv/PollRecv accept iff `b.seq > lastAcceptedSeq`. VERIFIED via headless trace: 90-bead train → 18 recv accepts (~5:1 collapse), node 5 fires once per complete coincidence, build + `-race` tests green across Wiring/windowandgate/readgate/holdflip/input.

### What works

All three previously-open blocking issues are resolved:

- **Issue #2 (node 5 never fires) — RESOLVED.** `windowWu=120` (~3000ms fixed span, like a neuron membrane time constant) decoupled from wire latency. Window opens on first input and lasts a fixed distance; both inputs within window → AND output, else erase.
- **Issue #3 (poll loops not pause-aware) — RESOLVED.** All poll loops (windowandgate/readgate/holdflip) now park on the one clock via injected `WaitUntil`; no wall-clock `time.After` remains in node poll paths.
- **Locality invariant enforced (was root cause of issue #2).** The `recvGate==trainDuration` coupling was one node affecting another's timing — a locality violation. Train-seq identity dedup fixes it structurally: receiver accepts a bead iff its `seq` exceeds `lastAcceptedSeq` (first of each train accepted; rest dropped). No time window, no cross-node coupling.

Nodes 1–5 emit uniform trains and process correctly. Node 5 (`WindowAndGate`) fires reliably on coincident left+right inputs.

### Clock and timing model (authoritative — `timing-spec.html`, NOT MODEL.md)

- **ONE human-speed clock.** Everything reads it. There is no "sim clock" or "pacer clock" — do not introduce that language.
- **Node processing is INSTANT** and stays so. The per-node `processingDistance` direction was **DROPPED this session** — do not re-add.
- **`WindowAndGate` model (user-stated):** window opens on first input, lasts a FIXED span (`windowWu=120`, ~3000ms at pulseSpeed). This is a fixed per-node distance, like a neuron membrane time constant — NOT derived from wire latency. Both inputs within window → AND output; else erase. ~3000ms sits ~97% of the way to the ~3104ms input cadence (~100ms headroom); cross-tick pairing possible but currently yields 0 (benign). Dropping to 80wu would restore margin if wanted.
- **Cadence (old issue #1) reframed:** not a code bug primarily. Measured per-hop = wire travel (arcLength/pulseSpeed, 0.5–1.7s/hop on the user's long experiment wires) dominates. The 2s `recvGate` floor is now gone (recvGate removed). To speed up: shorten wires (editor, user's lever) and/or make `trainDuration` a per-node distance (now safe since no receiver depends on it).

### Open / optional (none blocking)

1. **`trainDuration` could become a per-node distance** — now safe since no receiver depends on it. Only if per-node train speeds are wanted.
2. **Free-floating-ms → distance refactor** (dwell 800=32wu, train 2000=80wu, spacing 400=16wu, poll 5ms) — behavior-preserving cleanup; user deprioritized it (no visual change). `windowWu` and `fireDwell` already converted where touched.

### Uncommitted (intentionally left)

Topology layout autosaves — `topology/view/nodes/{2,3,4,5}.json` and
`topology/nodes/4/inputs/In.json`, `outputs/Out.json`. Leave them or commit as a chore.

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
  webview-only changes hot-reload on build (edges survive via geometry resend). The
  reload/restart loop for spec changes is gone — topology is a directory tree; the default
  `-topology` flag is the `topology/` dir. See `feedback_two_process_editor_reload`.
- **Runner is a prebuilt binary**, not `go run .`: the editor spawns
  `<repoRoot>/.wirefold-cache/wirefold` (gitignored). Webview-only changes hot-reload via
  the bundle watcher; `.go` changes rebuild the binary via an eager `**/*.go` watcher (and
  a lazy staleness check at `run()`). First launch after a fresh checkout does a one-time
  `go build`. Orphaned sims from crashed sessions are SIGKILLed on launch.
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
- **Port slots are `0|1|2` per side** (top/bottom/left/right each hold at most 3 ports).
  The webview parser (`parse-nodes-edges.ts`) throws a `load-error` on slot 3+, which blanks
  the whole diagram (chrome renders, graph does not; Fit does nothing). Surfaces in
  `.probe/ts-errors.jsonl` as `spec.nodes[N].outputs[M].slot: expected 0|1|2, got 3`.
- **Sustained-train model (updated):** a fire emits a 2s/400ms train (multi-bead wire); the
  receiver deduplicates by per-train seq (`b.seq > lastAcceptedSeq`) so a train collapses to
  ONE fire — no time window, no cross-node coupling. `recvGateMs`/`RecvGateMs` are **deleted**.
  Sends are fire-and-forget (consume-gates stripped). All on the one active-elapsed clock
  (pauses cleanly).
- **Locality invariant:** one node must NOT affect another's timing. The old
  `recvGate==trainDuration` coupling violated it. Train-seq identity dedup is the structural
  fix — no shortcut. Do not reintroduce time-window recv gating.
- **Timing-as-distance is the design target:** per-node local durations expressed as
  distances at pulseSpeed off the one clock — human-speed + locality + one-button pause.
  Reference: `timing-spec.html`. Do NOT put this in MODEL.md (user reverted that).
- **Subagent half-commit trap (recurrence):** the `AndGate → WindowAndGate` rename
  (`c4e624f7`) committed the file move + generated refs but left the
  package-decl/Register/SPEC edits uncommitted, leaving HEAD inconsistent (built only in the
  dirty working tree). Fixed in `a8ef268e`. After any subagent rename, check `git status` for
  leftover in-file edits before trusting HEAD. Reinforces `feedback_verify_subagent_commits`.

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`):
  `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` +
  `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-message-kind-parity`,
  `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file
  for webview-only. No reload/restart loop for spec changes (topology is a directory tree).
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
