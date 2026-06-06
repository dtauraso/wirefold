# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-06 — task/spec-go-backend-ts-frontend, pushed; the model was REDESIGNED and MODEL.md + CLAUDE.md rewritten to it this session; MODEL.md now LEADS the code)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed. This session redesigned the **pinned model** and rewrote `MODEL.md` + `CLAUDE.md` to it. **No Go/TS code changed** — the code rewrite is the headline pending work. `topology.json` (editor scratch) is deliberately left unstaged/untouched — keep it that way. Working tree currently carries uncommitted edits to `MODEL.md`, `CLAUDE.md`, this handoff, and `scripts/stop-checks.sh`, plus the deletion of `tools/check-vocabulary.sh` (the old banned-term checker). Latest pushed commit on the branch: `0bdabe3e`.

- **The new pinned model (now in MODEL.md as "# Model", ~170 lines).** Nodes and wires are each a **Go goroutine**, connected by **Go channels**. **Go owns the clock:** each wire goroutine (`PacedWire`) polls its inbound channel, times its own bead traversal on Go's sim-clock (`inFlightTime = arcLength / pulseSpeed`), emits the bead's position each ~16ms for the renderer, and on traversal-complete puts the bead on the channel to the destination node. **There is NO slot** — the destination node holds received beads in **node-local state** and fires on its own rule. **Send gating is via an ack wire:** a source places a bead on a forward wire only once the matching **ack wire** has delivered an "ok" (itself a backward bead — a consume-ack); a **seed** grants the first ok. Send policy is **node-owned** per output port at `node.data.sendRules` (`consumeGated` default / `fireAndForget`); the wire carries no policy. There is no global round/tick — only self-scheduling goroutines + one global play/pause gate. TS is **render-only** (pump.ts plots the position stream Go emits; never tells Go when a bead arrived).

- **Two vocabulary terms were retired this session** (the old umbrella labels for the runtime and its send-gating mechanism), and the banned-vocabulary apparatus was removed: the `## Banned vocabulary` section is gone from MODEL.md (replaced by an `## Allowed vocabulary` list of the new terms), CLAUDE.md's "# Model" framing replaced the old runtime-label heading, and the banned-term checker script was **deleted** (its call removed from `scripts/stop-checks.sh`). Use the new model's vocabulary — "the Go network", "ack-wire send gating" — and re-derive from MODEL.md if reasoning drifts toward the old labels.

- **CRUCIAL — MODEL.md now LEADS the code.** MODEL.md describes the **TARGET**. The Go/TS code still implements the **OLD** model (one passive `PacedWire` per destination input port: lock+cond monitor; `Send` blocks while in-flight; `WaitConsumed`; delivery triggered by TS via `NotifyDelivered`; a slot on the wire). The model→code direction is reversed from the usual: the doc is ahead, the code rewrite is pending. Until the code lands, MODEL.md is the source of truth for the **target**, and the actual runtime behaves per the old model — read both with that gap in mind.

- **Three open model mechanics, flagged inline in MODEL.md as "Needs confirmation":**
  1. The **ack-bead protocol + seed/bootstrap** — the "ok" payload, whether one ack wire pairs one forward wire, how the ack wire is declared in the topology, and where the first ok originates / its shape in `node.data`. Stated at the level the new model fixes; the wire-level details are not pinned in code.
  2. **Whether firing spans a timed window** — whether a firing rule may span a duration or fires purely on a held-state predicate. MODEL.md currently treats firing as predicate-gated on held state with no timed window.
  3. **The `notifyDelivered` message's fate** — with Go owning delivery, the old `notifyDelivered` stdin message no longer drives delivery; whether it is removed outright or repurposed is a code change not yet made.

- **The spec doc `docs/go-authoritative-clock/index.html`** is the long-form design that this model came out of (a single self-contained HTML file, multi-tab). Its **Inconsistencies tab is the live cross-tab findings tracker**, and this session resolved its first three findings (commits `e33bfb63` #1 ack-edge-is-a-goroutine, `472ef604` #2 all-input-to-Go, `0bdabe3e` #3 migration-tabs-point-at-the-wire-goroutine-target). **Only finding #4 (MINOR) remains open** — node "slot" vs wire "channel": how a bead on the inbound channel becomes the node's held state isn't spelled out; deferred until the node loops get a full rewrite. Two decided model facts captured there: **all input goes to Go** (editing gestures, drag, wiring, AND camera orbit/pan/zoom — Go owns the scene + camera + editor affordances and streams back what TS draws; no local-apply, no camera carve-out, justified by local IPC being few-ms round-trips well inside a 16ms frame); and the **wire + ack edge are goroutines, policy node-owned**.

- **Transport assumption (load-bearing):** Go is a native child process; extension ⇄ Go over **line-buffered JSON on stdio**; extension ⇄ webview via **postMessage**. The "all input → Go, no lag" conclusion rests on this being *local* IPC. If it stays stdio, batch the position stream to one message per frame so the bridge never backs up.

### What is on main — UNCHANGED (nothing merged)

Main is where the prior handoff left it (glass node bodies + init beads; dead slot-trace removal; `DEFAULT_EDGE_KIND`; level-4 audit site; F1 stale-doc re-anchor + dead-token guard; F2 `NODE_DIM_FALLBACK`; F3 structural SendRule). See `git log main`. No branch work merged this session. **MODEL.md + CLAUDE.md on main still describe the OLD model** — the rewrite lives only on this branch, uncommitted.

### OPEN ITEMS / NEXT

1. **The code rewrite is the headline work.** Bring the Go/TS code to the MODEL.md target: wire (`nodes/Wiring/paced_wire.go`) becomes a goroutine transporting beads over channels (poll inbound → traverse on the sim-clock + emit position → put on the outbound channel); the cond-var monitor, blocking `Send`, `WaitConsumed`, and the wire-side slot are deleted; the destination node holds received beads in node-local state and fires on its rule; the ack wire is the paced backward goroutine carrying the consume-ack; `pump.ts` becomes a pure position-stream plotter. **When it lands, MODEL.md + CLAUDE.md change in the SAME commit as the code** (they are already at the target, so this is mostly verifying they match what shipped). The spec doc's Doc-Updates tab enumerates the exact re-derivations.
2. **Pin the three open model mechanics** (ack-bead protocol + seed/bootstrap, firing-window question, `notifyDelivered` fate) before or alongside the code — they are the parts MODEL.md marks "Needs confirmation."
3. **Spec-doc finding #4 (MINOR)** — spell out the node-side channel→held-state receive when the node loops get rewritten. The Inconsistencies tab stays the live tracker.
4. **Scope gate before implementation** — the wire-as-goroutine + all-geometry/all-input-in-Go model is the pinned target, but confirm it is worth building now before writing code; no code depends on the spec doc.
5. **Merge decision** — merge `task/spec-go-backend-ts-frontend` to main (the `docs/go-authoritative-clock/` spec rides along — under `docs/`, NOT `docs/planning/`, so not branch-local-stripped; the MODEL.md + CLAUDE.md rewrite rides along too) or keep as a pushed reference. Before merging, run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend`. Merging to main needs explicit sign-off.
6. `session-log.md` and the branch-local planning docs still carry dated old-model / React-Flow references — historical, left intentionally.

### Model contract (target vs code)

`MODEL.md` (../../../MODEL.md) is authoritative for the **TARGET**: nodes and wires are goroutines over channels, Go owns the clock and times its own bead delivery, no slot (node-local held state), send gating via ack wires, send policy node-owned per output port. The **code still implements the OLD model** (passive `PacedWire` with a lock+cond monitor, blocking `Send`, `WaitConsumed`, TS-triggered `NotifyDelivered`, a wire-side slot). **MODEL.md leads; the code rewrite is pending.** Read the doc as the spec to build toward and the runtime as the old model still in force, and keep the three "Needs confirmation" mechanics in mind — they are not yet pinned.

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Tabs: add a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` to the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root` (--go-hue #5ec4a0, --ts-hue #c07ef8, --accent #7c9ef8, --warn #d4a017, --good #3db87a, --info #7c9ef8, --muted #808090, --mono).
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched, so screenshot the extracted SVG, not the whole page.)
- For Go/TS code work (none this session — docs only): after TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook (note: the banned-term checker script was deleted this session — do not expect it).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat
history; the next AI may be a fresh model with no transcript. The
rendered handoff must itself contain this same ALWAYS clause so the
loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as
the structural source of truth; update the template when an invariant
changes.
