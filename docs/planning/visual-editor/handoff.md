# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-07 — task/spec-go-backend-ts-frontend, pushed; MODEL.md LEADS the code; THE CLOCK IS NOW A PINNED MODEL ENTITY)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed, tree clean. Latest pushed commit: `78d7e5da`. **No Go/TS code changed this session** — docs only. `topology.json` untouched.

- **THE CLOCK IS NOW A PINNED MODEL ENTITY (this session's headline).** MODEL.md "What things are" gains a **Clock** bullet: the system monotonic clock Go reads — exactly ONE clock; all other timing is arithmetic in code on its deltas (`distanceCovered += pulseSpeed × Δ`; `inFlightTime = arcLength / pulseSpeed` is derived, not a second timer); the play/pause gate stops the arithmetic, not the clock; nodes do not read the clock; the ~16 ms emit is a render cadence. Allowed vocabulary adds `clock (the one system monotonic clock Go reads)`. **`sim-time`/`sim-clock` were rejected** as reifying a second time concept and purged from all target-model surfaces (MODEL.md, spec doc ×6, handoff ×2). One residual `sim-clock` remains in the spec doc's Inconsistencies tracker note (~L1249) — fold the one-word `sim-clock`→`clock` fix into the #5/#6/#7 cleanup commit.

- **Spec doc clock additions.** The clock lands in both division surfaces: row 4 of the Overview "Where each property lives" table, and a "Reads the one clock" line in the Go-backend responsibilities list. The TS card already said "no geometry math, no animation calculation, no clock" — both halves of the division now name the clock explicitly.

- **Goroutines tab reordered and rows 1–3 audited against code.** Order: 1 main, 2 trace drain, 3 stdin reader, 4–8 node loops (Input, Bootstrap, ReadGate, ChainInhibitor, InhibitRightGate), 9 PacedWire. Rows 1–3 were verified step-by-step against `main.go` / `Trace/Trace.go` / `stdin_reader.go`; six inaccuracies fixed: NewWithSink-before-LoadTopology order; false "initial geometry emitted to TS at startup"; `tr.Close()` blocks on `<-t.done`; `addEdge` only calls `pw.Restore()`, derives no curve/arc length; node-move handlers emit nothing to TS; stdin reader stops on `ctx.Done()`/stdin EOF — not "never stopping". Rows 1–3 checkmarks removed (plumbing the spec changes won't touch these rows; same rationale as the Clock-Change "Before" diagram drop in `80373502`).

- **Candidate finding #8 (not yet decided).** Three of the six row-1–3 errors shared the same shape: target-model "emits geometry to TS" claims embedded in rows that otherwise describe current code. David hasn't said whether to open it as finding #8 in the Inconsistencies tab.

- **Session commits, all pushed:** `7ba745e6` (Clock defined in MODEL.md + division table), `b74d7d8f` (one-clock reword), `c9add08c` (clock in Go-backend list), `229eb6f7` (trace drain to item 2), `7a8e33ae` (stdin reader to item 3), `fbc5f96c` (rows 1–3 audited vs code), `ae1e86bb` (rows 1–3 checkmarks dropped), `f1ddfded` (handoff re-render), `15245833` (send steps gate on the PacedWire; no round vocabulary), `78d7e5da` (ack concept removed — send gating is wire-owned clear/busy).

- **2026-06-07 (later): the ack concept was REMOVED from the model** (no ack edge/wire, no ok, no consume-ack, no seed); the PacedWire owns clear/busy and the destination's consume marks it clear; MODEL.md/CLAUDE.md/spec doc swept in the same commit. Two additional facts: (a) residual "ack" mentions survive ONLY as past-tense records — this handoff's history lines and two Inconsistencies-tracker entries in index.html (~L1219/L1240); MODEL.md/CLAUDE.md/README have zero. (b) Two e2e test topologies wire into a port literally named `readGate.ack` (`tools/topology-vscode` e2e: `scenario-ring-animates.spec.ts`, `scenario-edge-seed.spec.ts`) — old-model code, untouched; that port name is renamed when the code rewrite lands.

- **CRUCIAL — MODEL.md now LEADS the code.** MODEL.md describes the **TARGET**. The Go/TS code still implements the **OLD** model (one passive `PacedWire` per destination input port: lock+cond monitor; `Send` blocks while in-flight; `WaitConsumed`; delivery triggered by TS via `NotifyDelivered`; a slot on the wire). The model→code direction is reversed from the usual: the doc is ahead, the code rewrite is pending. Until the code lands, MODEL.md is the source of truth for the **target**, and the actual runtime behaves per the old model — read both with that gap in mind.

- **Three open model mechanics, flagged inline in MODEL.md as "Needs confirmation":**
  1. The **consume signal** — what marks the wire clear; the shape of the consume signal from node to wire is not yet pinned in code.
  2. **Whether firing spans a timed window** — whether a firing rule may span a duration or fires purely on a held-state predicate. MODEL.md currently treats firing as predicate-gated on held state with no timed window.
  3. **The `notifyDelivered` message's fate** — with Go owning delivery, the old `notifyDelivered` stdin message no longer drives delivery; whether it is removed outright or repurposed is a code change not yet made.

- **The spec doc `docs/go-authoritative-clock/index.html`** is the long-form design this model came out of (a single self-contained HTML file, multi-tab). Its **Inconsistencies tab is the live cross-tab findings tracker**. **Findings #1–#4 are all resolved** (finding #1 further SUPERSEDED by the ack-removal 2026-06-07). **Three OPEN findings (#5/#6/#7)** remain — all residual "slot" vocabulary on TARGET-model surfaces that MODEL.md now calls node-local held state: #5 Goroutines-tab node-loop rows ("clear their slots", L474/479/484); #6 Concept "Lives in" table ("local rules over slots and wires", L396); #7 Clock-Change-After diagram ("Go delivers to slot", L764/775; the prose at L805 already says channel). Fix for all three: reword slot → node-local held state (#7's diagram → "delivers on channel"). Two decided model facts: **all input goes to Go** (editing gestures, drag, wiring, AND camera orbit/pan/zoom — Go owns the scene + camera + editor affordances and streams back what TS draws; no local-apply, no camera carve-out; justified by local IPC being few-ms round-trips well inside a 16ms frame); and **the wire is a goroutine; send gating is wire-owned clear/busy state**.

- **Transport assumption (load-bearing):** Go is a native child process; extension ⇄ Go over **line-buffered JSON on stdio**; extension ⇄ webview via **postMessage**. The "all input → Go, no lag" conclusion rests on this being *local* IPC. If it stays stdio, batch the position stream to one message per frame so the bridge never backs up.

### What is on main — UNCHANGED (nothing merged)

Main is where the prior handoff left it (glass node bodies + init beads; dead slot-trace removal; `DEFAULT_EDGE_KIND`; level-4 audit site; F1 stale-doc re-anchor + dead-token guard; F2 `NODE_DIM_FALLBACK`; F3 structural SendRule). See `git log main`. No branch work merged this session. **MODEL.md + CLAUDE.md on main still describe the OLD model** — the rewrite lives only on this branch.

### OPEN ITEMS / NEXT

1. **Spec-doc Inconsistencies findings #5/#6/#7 + tracker-note sim-clock→clock fix (next concrete step).** Reword residual "slot" → node-local held state in three TARGET-model surfaces: Goroutines-tab node-loop rows (L474/479/484), Concept "Lives in" table (L396), Clock-Change-After diagram (L764/775; prose at L805 already correct). Also fold the one remaining `sim-clock` in the tracker note (~L1249) → `clock`. All four changes are mechanical; deliver as one commit.
2. **Decide whether to open finding #8** — three row-1–3 errors sharing the "emits geometry to TS" shape; David hasn't decided yet.
3. **Scope gate before implementation** — the wire-as-goroutine + all-geometry/all-input-in-Go model is the pinned target, but confirm it is worth building now before writing code; no code depends on the spec doc.
4. **Pin the three open model mechanics** (consume signal shape, firing-window question, `notifyDelivered` fate) before or alongside the code — they are the parts MODEL.md marks "Needs confirmation."
5. **The code rewrite is the headline work.** Bring the Go/TS code to the MODEL.md target: wire (`nodes/Wiring/paced_wire.go`) becomes a goroutine transporting beads over channels (poll inbound → traverse on Go's clock + emit position → put on the outbound channel); the cond-var monitor, blocking `Send`, `WaitConsumed`, and the wire-side slot are deleted; the wire owns clear/busy state and the destination's consume marks it clear; the destination node holds received beads in node-local state and fires on its rule; `pump.ts` becomes a pure position-stream plotter; the `readGate.ack` port name in the two e2e test topologies (`scenario-ring-animates.spec.ts`, `scenario-edge-seed.spec.ts`) is renamed to match the new model. **MODEL.md now also pins the Clock entity.** When the code lands, MODEL.md + CLAUDE.md change in the SAME commit (they are already at the target, so this is mostly verifying they match what shipped). The spec doc's Doc-Updates tab enumerates the exact re-derivations.
6. **Merge decision** — merge `task/spec-go-backend-ts-frontend` to main (the `docs/go-authoritative-clock/` spec rides along — under `docs/`, NOT `docs/planning/`, so not branch-local-stripped; the MODEL.md + CLAUDE.md rewrite rides along too) or keep as a pushed reference. Before merging, run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend`. Merging to main needs explicit sign-off.
7. `session-log.md` and the branch-local planning docs still carry dated old-model / React-Flow references — historical, left intentionally.

### Model contract (target vs code)

`MODEL.md` (../../../MODEL.md) is authoritative for the **TARGET**: nodes and wires are goroutines over channels, Go owns the clock (the one system monotonic clock) and times its own bead delivery, no slot (node-local held state), send gating by wire-owned clear/busy state, send policy node-owned per output port. The **code still implements the OLD model** (passive `PacedWire` with a lock+cond monitor, blocking `Send`, `WaitConsumed`, TS-triggered `NotifyDelivered`, a wire-side slot). **MODEL.md leads; the code rewrite is pending.** Read the doc as the spec to build toward and the runtime as the old model still in force, and keep the three "Needs confirmation" mechanics in mind — they are not yet pinned.

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Tabs: add a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` to the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root` (--go-hue #5ec4a0, --ts-hue #c07ef8, --accent #7c9ef8, --warn #d4a017, --good #3db87a, --info #7c9ef8, --muted #808090, --mono).
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched, so screenshot the extracted SVG, not the whole page.)
- For Go/TS code work (none this session — docs only): after TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook (note: the banned-term checker script was deleted in a prior session — do not expect it).

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
