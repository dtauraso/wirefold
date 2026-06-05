# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-04 — task/spec-go-backend-ts-frontend, pushed, docs/spec ONLY; wire-as-goroutine model + diagrams + Inconsistencies audit; input model resolved to all-input-to-Go)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed. **DOCS/SPEC ONLY** — no Go/TS/substrate code changed. Only `docs/go-authoritative-clock/index.html` and this handoff edited. `topology.json` (editor scratch) was deliberately left unstaged/untouched throughout — keep it that way. Latest pushed commit: `91735eaa`.

- **Target model: PacedWire and the ack edge are GOROUTINES.** PacedWire = poll its inbound channel → elapse the traversal itself on Go's sim-clock (`inFlightTime = arcLength ÷ pulseSpeed`) and emit the bead's position each ~16ms → put the bead on the outbound channel; no slot-check / consume / WaitConsumed / cond-var — pure transport. The ack edge = the paced backward mirror (a visible backward bead). The send-helpers row is deleted (the cond-var unpark guard dissolves under channels + select). **Policy is NODE-OWNED** (edges pure transport; `consumeGated`/`fireAndForget` via `node.data.sendRules`); node send steps read "send X on the channel to the wire, only once its ack edge shows that wire clear; never drop." **This is a TARGET amendment, NOT implemented; MODEL.md is UNCHANGED** (still describes the current passive-PacedWire code: lock+cond monitor, Send blocks on inFlight, WaitConsumed, TS-triggered NotifyDelivered). The Doc-Updates tab lists the MODEL.md re-derivations for when it lands. **Do NOT touch MODEL.md until the code is implemented.**

- **Geometry-ready points in the Goroutines tab** (commit `e72099cb`): `main`/LoadTopology derives the full geometry at load — each port's world-position (node pos + side), each edge's curve control points (endpoints + route), each arc length (from the curve) — and emits that initial geometry to TS first; the stdin reader re-derives affected geometry on edits (`node-move` → moved node's port positions + each incident edge's curve/arc-length; `addEdge` → new edge's curve/arc-length) and emits the change. Tying note "Geometry is Go's; TS only draws it": static geometry derived at load + re-derived on edits, sent over the same out-stream as the per-bead position stream; TS plots, computes none.

- **Three diagram tabs** — hand-authored inline SVG, pixel-verified by headless-Chrome screenshot; role colors: green = Go/node loop, blue = PacedWire/trace-pump, amber = ack edge/the write path, purple = TS render, grey = extension host/screen. **Goroutine graph** (the connection unit: Input loop —send→ PacedWire —deliver→ ReadGate loop, ack edge returning consume→ack). **TS graph** (two pipes to Go: position stream in → trace pump → render loop draws to screen; out = writeStdin carrying CRUD/pause-resume/input). **The Bridge** (kept the minimal two-arrow abstraction, added an "actual path" relay: GO process ⇄ VS Code extension host ⇄ TS webview, stdout→postMessage / postMessage→stdin). The TS graph went through revisions — dropped a Store box, a GPU box, and the Input box (input isn't a separate stage).

- **Inconsistencies tab added** (commit `78633294`, the last tab) — a cross-tab audit; findings tagged FIX READY / DECIDE / SEAM / MINOR / RESOLVED. It is the live findings tracker:
  - **#1 FIX READY (un-fixed)** — the Goroutines-tab note still says "the ack edge remains data, not a goroutine," contradicting its own (now-goroutine) row and the Doc-Updates bullet. Update the note to call the ack edge a goroutine (the backward-paced mirror of PacedWire).
  - **#2 RESOLVED → all input to Go** (commit `91735eaa`) — see the input-model bullet below.
  - **#3 SEAM** — Phases / Plan / Clock Change still credit the *node* loop and "call NotifyDelivered internally," vs the updated tabs' *wire* loop + "put the bead on the channel."
  - **#4 MINOR** — nodes still "clear their slots" while the wire "puts the bead on the channel"; the node-side channel→slot receive isn't spelled out.

- **Input model resolved: ALL input goes to Go.** Editing gestures, drag, wiring, AND camera orbit/pan/zoom are forwarded to Go as signals; Go owns the scene + the camera + the editor affordances (node-menu data, drag-preview icon) and streams back what TS draws. **No local-apply, no camera carve-out** — it is all one local machine, so a round-trip is a few ms (postMessage + a pipe write + a trivial Go update), well inside a 16ms frame, so no perceptible lag. (Earlier "live-gesture vs committed" and "camera is special" splits were explored and dropped — a network-latency pattern-match that does not apply to local IPC.) So the TS-graph "pointer/wheel" label is correct. **Remaining reconcile (now FIX-READY):** four spots still say input is consumed in TS — the TS-code Input-handlers row, the TS-code Outbound-IPC row, the Bridge relay diagram, and the TS → Go tab — flip them to "all input → Go; Go owns the camera too."

- **Transport assumption (load-bearing):** Go is a native child process; extension ⇄ Go over **line-buffered JSON on stdio**; extension ⇄ webview via **postMessage**. The "all input → Go, no lag" conclusion rests on this being *local* IPC (few-ms round-trips). If the target ever moved to Go-as-WASM-in-webview / shared memory, the round-trip vanishes; if it stays stdio, batch the position stream to one message per frame so the bridge never backs up.

- **Node-menu / instantiate / drag-preview flow — NOT yet written into the model tabs.** The node menu's contents are Go's data (TS renders the menu from it); instantiating a node sends only a signal to Go; Go streams the dragging-icon data and TS displays it. Referenced in finding #2's resolution but not yet captured in The Bridge / TS → Go — a follow-up.

- **Frame confirmed but DELIBERATELY NOT in the spec** (user said don't): the Go↔TS "API medium" is the process's stdio, not HTTP/addresses/sockets; the API surface is the set of message kinds dispatched by a switch; fire-and-forget streams both ways, not request/response.

### What is on main — UNCHANGED (nothing merged)

Main is where the prior handoff left it (glass node bodies + init beads; dead slot-trace removal; `DEFAULT_EDGE_KIND`; level-4 audit site; F1 stale-doc re-anchor + dead-token guard; F2 `NODE_DIM_FALLBACK`; F3 structural SendRule). See `git log main`. No branch work merged this session.

### OPEN ITEMS / NEXT

1. **Merge decision** — merge `task/spec-go-backend-ts-frontend` to main (the `docs/go-authoritative-clock/` spec rides along — under `docs/`, NOT `docs/planning/`, so not branch-local-stripped) or keep as a pushed planning reference. No code depends on it. Before merging, run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend`.
2. **The Inconsistencies tab is the live findings tracker.** Open there: **#1** (ack-edge note — FIX READY), **#3** (migration-tab seam), **#4** (slot vs channel — minor). Plus **#2's remaining reconcile** — flip the four input-handling spots (TS-code Input-handlers, TS-code Outbound-IPC, Bridge relay, TS → Go tab) to all-input-to-Go; now FIX-READY since the direction is decided.
3. **Capture the node-menu / instantiate / drag-preview flow** in The Bridge or TS → Go (model detail only referenced so far).
4. **Migration tabs lag (= finding #3):** Phases / Plan / Clock Change still describe node-loop self-delivery + NotifyDelivered-reuse; reconcile to the wire-goroutine target, or mark the NotifyDelivered reuse explicitly as a Phase-1 stepping-stone.
5. **Scope gate before any implementation** — the wire-as-goroutine + all-geometry/all-input-in-Go model is a PROPOSAL; confirm it is worth building before writing code. When implementing, MODEL.md + CLAUDE.md change in the SAME commit as the code (per the Doc-Updates tab).
6. `session-log.md` still has dated React-Flow refs — historical, left.

### Substrate model contract (current vs target)

`MODEL.md` (../../../MODEL.md) is authoritative for the CURRENT CODE: one passive `PacedWire` per destination input port (lock+cond monitor); `Send` blocks while `inFlight`; `WaitConsumed`; delivery triggered by TS via `NotifyDelivered`; send policy node-owned per `node.data.sendRules`. The go-authoritative-clock spec proposes a TARGET that amends it — wire becomes a goroutine, channels replace the cond-var monitor, Go times its own delivery, the ack edge is a paced backward goroutine, all geometry + all input live in Go, send policy stays node-owned. Until the target is implemented: **MODEL.md is the source of truth for code; the spec is the source of truth for the target design.** Do not edit MODEL.md to match the spec ahead of the code.

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Tabs: add a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` to the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root` (--go-hue #5ec4a0, --ts-hue #c07ef8, --accent #7c9ef8, --warn #d4a017, --good #3db87a, --info #7c9ef8, --muted #808090, --mono).
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
