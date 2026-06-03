# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-02 — task/spec-go-backend-ts-frontend, pushed, docs/spec ONLY; wire-as-goroutine redesign + 3 diagram tabs)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed. **DOCS/SPEC ONLY all session** — no Go/TS/substrate code changed. Only `docs/go-authoritative-clock/index.html` was edited (plus this handoff). `topology.json` (editor scratch) was deliberately left unstaged/untouched the entire session — keep it that way. Latest pushed commit: `872aba5a`.

- **Headline: the spec's target model now makes PacedWire and the ack edge GOROUTINES** (they were "not a goroutine — driven by node loops via lock+cond"). The Goroutines tab now describes the target uniformly:
  - **PacedWire** = a goroutine, 3-step loop: (1) **poll** its inbound channel for a bead a source node sent; (2) mark in-flight and **elapse the traversal itself** — `inFlightTime = arcLength ÷ pulseSpeed` on Go's sim-clock — emitting the bead's position each ~16ms report (**no TS NotifyDelivered; the wire times its own delivery**); (3) on traversal-complete, **put the bead on the channel** to the destination node. No slot-check, no consume/ack, no WaitConsumed, no cond var — **pure transport**.
  - **ack edge** = a goroutine, the **paced backward mirror** of PacedWire: poll inbound for a consume-ack a destination sent → elapse a backward traversal (a **visible bead** running destination→source, emitting position) → put the ack on the channel to the source.
  - **send-helpers row deleted** — the cond-var unpark guard dissolves once coordination is channels + `select` (ctx cancellation is a native select arm; no parked `cond.Wait()` to rescue).

- **The policy fork resolved NODE-OWNED.** Edges are pure transport; the source node owns send policy (`consumeGated`/`fireAndForget` via `node.data.sendRules`). Removing PacedWire step 4 took consume+ack+backpressure entirely OFF the wire. The five node-loop **send steps** now read "**send** X on the channel to the wire, only once its **ack edge** shows that wire clear; never drop" (re-pointed from the OLD "place as a pulse in flight / only when the wire is clear" passive-wire phrasing).

- **This is a TARGET-model amendment, NOT implemented in code.** **MODEL.md is UNCHANGED** and still correctly describes the CURRENT passive-PacedWire code (lock+cond monitor, `Send` blocks on `inFlight`, `WaitConsumed`, TS-triggered `NotifyDelivered`). The spec's **Doc-Updates tab** now lists the MODEL.md re-derivations for when this lands. **Do NOT touch MODEL.md until the code is implemented.**

- **NotifyDelivered reconciliation (Goroutines tab only):** dropped the stdin-reader's `delivered → NotifyDelivered` case; the adjacent note now credits the **wire loops** with emitting positions + self-delivering. KEPT the NotifyDelivered references in the migration tabs (Clock-Change / Doc-Updates / Phases / Verify) — those correctly describe it as the thing being removed; deleting them would gut the migration plan.

- **TS-code tab flipped to TARGET TS** (`f483fce0`): render the position stream Go emits + write CRUD/pause-resume out; removed the curve-rebuild / progress-t / sampling / `delivered`-post from the render-loop row and the authoritative-store framing (now a passive copy). Phase-2/3 warn-callouts folded into the described state. (Left the current-TS `{type:"delivered"}` mentions on that tab's IPC/render rows as "current TS, to be migrated" — flip only if making the whole TS-code tab target-only.)

- **Three diagram tabs added** — hand-authored inline SVG matching the Clock-Change style, every one **pixel-verified** by headless-Chrome screenshot. Role-keyed colors: **green** = Go / node-loop goroutine, **blue** = PacedWire / trace pump, **amber** = ack edge / the write path, **purple** = TS render, **grey** = extension host / boundary.
  1. **Goroutine graph** — the connection unit: `Input loop` —send→ `PacedWire` goroutine —deliver→ `ReadGate loop`, with the `ack edge` goroutine returning consume→ack below. Notes: legend, "the ring tiles this unit," "singletons (main / stdin reader / trace drain) off to the side."
  2. **TS graph** — two pipes to Go: IN = Go →(position stream)→ `Trace pump` → `Render loop` (draws beads to screen); OUT = a dashed **user** arrow from Render loop into `Outbound IPC` (writeStdin), which writes **CRUD · pause/resume · pointer/wheel** to Go. **No separate Input box** (user iterated it out: input isn't a stage; pointer/wheel is just one more thing written to Go). Earlier revisions also dropped a Store box and a GPU box.
  3. **The Bridge** — kept the minimal two-arrow abstraction, ADDED an "actual path" relay diagram: `GO process` ⇄ `VS Code extension host` ⇄ `TS webview`, showing stdout→postMessage (position stream out) and postMessage→stdin/`writeStdin` (CRUD etc. in). Fire-and-forget both ways.

- **Frame confirmed with the user but DELIBERATELY NOT added to the spec** (user said don't): the Go↔TS "API medium" is the process's **stdio** (Go is a local child process, piped) relayed by the extension host — not HTTP/addresses/sockets. The "API surface" is the set of message **kinds** (a `type`/`kind` discriminator dispatched by a `switch`); fire-and-forget streams both ways, not request/response.

### What is on main — UNCHANGED this session (nothing merged)

Main is exactly where the prior handoff left it (glass node bodies + init beads; dead slot-trace removal; `DEFAULT_EDGE_KIND`; level-4 audit site; F1 stale-doc re-anchor + dead-token guard; F2 `NODE_DIM_FALLBACK`; F3 structural SendRule). See `git log main` for detail. No branch work merged this session.

### OPEN ITEMS / NEXT

1. **Merge decision (the fork that matters):** merge `task/spec-go-backend-ts-frontend` to main (the `docs/go-authoritative-clock/` spec rides along — it is under `docs/`, NOT `docs/planning/`, so it is not branch-local-stripped) or keep it as a pushed planning reference. No code depends on it. Left unmerged. Before merging, run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend`.
2. **Migration tabs lag the wire-goroutine target.** This session updated Goroutines / TS-code / Doc-Updates / diagram tabs to the wire-as-goroutine model, but **Phases / Plan / Clock-Change / Verify still describe the earlier framing** ("clock into Go / node loops self-deliver / wire loops sleep ~16ms") where the wire was not yet a first-class goroutine. Internally consistent as migration narrative, but reconcile them if you keep building the spec.
3. **Scope gate before any implementation.** The wire-as-goroutine model is a PROPOSAL — a substantial substrate change (passive struct → goroutine; cond+lock → channels; TS-triggered delivery → Go-paced). Confirm it is worth building before writing code. When implementing, MODEL.md + CLAUDE.md change in the SAME commit as the code, per the Doc-Updates tab.
4. **InhibitRightGate step-2 parity (carryover):** its loop row still reads "if it times out, clear the partial arrival and restart" — never got ReadGate's "ack the source on timeout" parity. With the redesign, "ack the source" now means send the ack on the channel to the ack-edge goroutine. Low priority.
5. `session-log.md` still has dated React-Flow refs — historical, left.

### Substrate model contract (current vs target)

`MODEL.md` (../../../MODEL.md) is authoritative for the CURRENT CODE: one `PacedWire` per destination input port, a **passive** lock+cond monitor; `Send` blocks while `inFlight`; `WaitConsumed`; delivery triggered by TS via `NotifyDelivered`; send policy node-owned per `node.data.sendRules`. The go-authoritative-clock spec proposes a TARGET that amends it — the wire becomes a **goroutine**, channels replace the cond-var monitor, Go times its own delivery, the ack edge is a paced backward goroutine, send policy stays node-owned. Until the target is implemented: **MODEL.md is the source of truth for code; the spec is the source of truth for the target design.** Do not edit MODEL.md to match the spec ahead of the code.

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Tabs: add a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` to the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root` (--go-hue #5ec4a0, --ts-hue #c07ef8, --accent #7c9ef8, --warn #d4a017, --muted #808090, --mono).
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched, so screenshot the extracted SVG, not the whole page.)
- For Go/TS code work (none this session): after TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook.

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
