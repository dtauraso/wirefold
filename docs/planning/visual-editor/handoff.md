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
model. **NOT ready to merge** (foundational change with known open issues below).

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

### What works

Nodes 1,2,3,4 emit uniform trains and process correctly; the recv-gate collapses each
incoming train to one fire (nodes 2/3/4 verified by user); window/dwell are pause-aware.

### Open issues (all must respect locality — fix LOCALLY per node, NOT via shared/global constants)

1. **Cadence slow (~10–40 s/cycle):** three ~2 s delays stack per hop (wire latency +
   `recvGate` 2000 + train 2000). Speeding up must be PER-NODE, not by lowering the global
   `trainDurationMs`/`recvGateMs` (those couple all nodes).
2. **Node 5 (`WindowAndGate`) never fires:** its coincidence window mis-phases — opens on a
   stale `FromLeft` and clears ~8 ms before the matching `FromRight` lands (inputs arrived
   67 ms apart but the window had just cleared). `FromRight` also lags via the longer path
   (2→3→4→5). Local to node 5.
3. **`WindowAndGate` poll loop (`node.go:170`)** still uses wall-clock
   `time.After(pollInterval=5ms)` — not pause-aware; the window/dwell CHECKS are pause-aware,
   only the poll wake isn't. Minor; convert to read the one clock.

### Active design direction (reference = `timing-spec.html`, NOT MODEL.md — user removed the MODEL.md version)

- **Time = distance ÷ one `pulseSpeed` (0.04 wu/ms).** Each node AND wire is its own
  independent goroutine with its OWN local timing distance. Human speed = every distance
  advances at the same pulseSpeed. Play/pause = one button freezing all per-node timers (the
  one shared clock — the ONLY shared timing).
- **Free-floating ms timers** (dwell 800, poll 5, train 2000, recvGate 2000) should become
  DISTANCES at pulseSpeed (e.g. dwell 32 wu, train 80 wu, spacing 16 wu, recvGate 80 wu) read
  off the active-elapsed clock — human-speed AND pausing together. (`timing-spec.html`
  "Target (distance)" tab.)
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
- **Sustained-train model:** a fire emits a 2s/400ms train (multi-bead wire); the receiver
  clock-gates Recv so a train collapses to ONE fire (`recvGateMs` window). Sends are
  fire-and-forget (consume-gates stripped). All on the one active-elapsed clock (pauses
  cleanly).
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
