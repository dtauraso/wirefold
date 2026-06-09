# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-09 — task/spec-go-backend-ts-frontend, pushed; spec doc is SETTLED, CONSISTENT, and CONCISE: 9 tabs, one open spec item (i1))

- Active branch: `task/spec-go-backend-ts-frontend`, pushed, tree clean. Latest pushed commit: `e93d42e2`.
- **NOTE:** `topology.json` has the git skip-worktree bit SET — editor churn stays out of git. Deliberate changes require `git update-index --no-skip-worktree topology.json` first, then re-set after.
- All work since the last handoff was on the **spec doc** (`docs/go-authoritative-clock/index.html`): a consistency sweep, a Tracking-tab cleanup, and an exhaustive concision pass. No Go/TS code changed; nothing merged to main.

### The settled split (the model)

The Go/TS boundary is **diagram vs viewpoint**:

- **Go owns the diagram** — nodes/edges/beads, the 3D math for the diagram (curve control points, arc length, node/port world positions, per-frame bead positions), animations, clock + pulseSpeed, the per-wire fade flag. Go already has the static-geometry math (`curve_params.go: BezierArcLength` / `PortCurveArcLength`, `port_geometry.go: nodeWorldPos` / `portWorldPos`) — the TS twins (`buildEdgeCurve`, `rfArcLength`) are duplicates to delete, not ports.
- **TS owns the viewpoint** — camera, scene navigation (orbit/pan/zoom), projection, raycast-picking, GPU render. TS keeps **r3f/three.js for the 3D scene-navigation machinery** (Go lacks the libs to replace it cheaply); the control *feel* stays bespoke (substance — not a controls library). "TS computes none" means none of the *diagram*; the viewpoint math is TS's.
- **Input split:** navigation input (pointer/wheel for the camera) stays in TS; only **action input** — CRUD carrying the picked Go id — goes to Go.
- **Store:** the zustand store is a **no-op holder** — Go is the sole writer of its data (NOT removed, NOT "moved to Go").
- **CRUD surfaces** (TS, talk to Go): play/pause, menu, click events.

### The only new Go build — Chain 1 (the PacedWire rewrite)

Go owns no clock today: it emits discrete `send`/`done` events with a one-shot `arcLength`/`simLatencyMs` budget, and **TS** tweens with r3f's `useFrame` clock. New Go work:
1. **Clock + per-frame stepping** — stdlib `time`, monotonic, with a paused-time accumulator (elapsed must not advance while paused); ~16 ms tick.
2. **Per-frame bead-position eval** — eval each bead's `t = elapsed / inFlightTime` and the bézier (control points already exist) → 3D position, streamed per frame. `inFlightTime = arcLength / pulseSpeed`. The out-stream changes shape: discrete events + budget → a per-frame position stream.

Go needs **no 3D-math/projection library** — camera/projection/picking are TS (three.js). Camera + picking are **TS, not new Go builds**.

### The Bridge — JSONL protocol

- **JSONL over stdio**, both directions, one object per line.
- The VS Code **extension host is a dumb pipe** — forwards each line verbatim. **stdout carries protocol only** (logs → stderr / `.probe/*.jsonl`).
- **One id space — Go's.** Every object Go streams to TS carries its Go id; a user interaction forms its outbound message directly from that id. TS keeps no TS-specific ids and does no interaction→component lookup.
- **Escape hatch (unbuilt):** a localhost WebSocket from the webview straight to a Go server — only if `postMessage` ever bottlenecks.

### Active experimental network — UNCHANGED

`topology.json` holds only `in08` (Input, init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor) + edge `i0ToI1`. `topology.inactive.json` (editor never reads it) holds the inactive nodes/edges.

### The in08↔i0 handshake — spec'd, NOT yet coded

- `in08` loop: read the signal on wire `i0→in08` (1/0); `i += signal` (1 advances the read head, 0 holds); send `Init[i]` on wire `in08→i0`.
- `i0` loop: held copies `in08`'s value (init `-1`); if the arriving value DIFFERS from held → set held, send `1` to `in08`; else send `0`; forward held value to `i1`.
- `i1` loop: receives `i0`'s held value; it is a SINK; behavior beyond receiving is UNSPECIFIED (the one open spec item).
- Needs TWO new wires (`in08→i0`, `i0→in08`) + new PORTS on the Input and ChainInhibitor kinds + `i0`'s held init `0`→`-1`. None exists in code yet. The goroutine graph draws the handshake routed through TWO PacedWire goroutines (one per direction).

### The PacedWire loop — spec'd, NOT yet coded

1. Add the beads received this round to the set already held (multi-bead).
2. Each round, for every bead, compute its next position from the Go clock and the human-speed slowdown (`pulseSpeed`).
3. If that position is still on the wire, send the bead's 3D position to TS.
4. If past the end, the bead is done — hand the bead's VALUE to the destination node and remove the bead (the off-the-end position is never sent to TS).
5. Loop.

### Spec doc state — 9 tabs, consistent, concise

`docs/go-authoritative-clock/index.html`:
- **9 tabs:** The split · Goroutines · TS · The Bridge · Clock · TS → Go · Plan · Verify · Tracking (merged down from 16). The inline `<script>` wires tabs dynamically by `data-panel`.
- **Consistency-swept:** camera/picking uniformly TS; "all input to Go" superseded; store uniformly "no-op holder"; slot vocabulary → "held state" / "channel" everywhere (the Clock **"Before"** diagram intentionally keeps "slot" for contrast).
- **Concise:** every prose block over ~40 words was tightened to bullets / tables / one-line claims (exhaustive sweep — 0 blocks over 40 words remain, excepting the contract box and the "Spec is authoritative" callout).
- **Tracking tab** is trimmed to ONE open item: **i1's behavior beyond receiving is unspecified.** All resolved findings and dated changelog records were removed — that history lives in git + this handoff.

### What is on main — UNCHANGED

Nothing merged. Main still describes the OLD model. All session work lives on this branch.

### OPEN ITEMS / NEXT

1. **The code rewrite is the headline.** Chain 1 (clock + per-frame bead eval = the PacedWire rewrite) is the first domino. The Go/TS code still implements the OLD model (consumeGated/single-bead/full ring; TS owns the clock + animation and computes geometry).
2. **The in08↔i0 handshake** needs 2 new wires + new ports on Input/ChainInhibitor + `i0` held init `-1` (Go code).
3. **i1's behavior** beyond receiving is the lone open spec question — sink for now.
4. **Camera/picking are TS** (three.js), not Go builds. r3f stays for 3D scene navigation; control feel bespoke (no OrbitControls).
5. **Store** = no-op holder, Go sole writer — a TS-side change when the rewrite lands.
6. **The node-contract principle is deliberately UNPINNED** (totality in / no guarantees out; reliability lives in the node, not the channel). Let it manifest in code — do NOT re-pitch pinning it to MODEL.md/memory.
7. **Merge decision** — run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend` before merging; needs explicit sign-off.

(The slot-vocabulary inconsistency listed as open in the prior handoff is now RESOLVED.)

### Model contract (target vs code)

`MODEL.md` is authoritative for the TARGET. The spec doc now fully, consistently, and concisely describes the Go/TS split (diagram vs viewpoint), the JSONL bridge, the single Go id space, and Chain 1. The **code still implements the OLD model**. The rewrite is pending; Chain 1 is the first step. The firing-window "Needs confirmation" still stands (the W coincidence-window is its concrete local answer, unpinned because ReadGate/InhibitRightGate are inactive).

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. 9 tabs. Add a tab with a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` in the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root`.
- **Concision rule:** keep prose blocks under ~40 words — convert any wall to bullets, a table, or a one-line claim. The doc is currently at 0 blocks over 40 words; keep it there.
- **Active topology** is `in08`/`i0`/`i1`. `topology.json` is skip-worktree — deliberate edits require clearing the bit first.
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched; screenshot the extracted SVG.)
- For Go/TS code work: after a TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after a Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook.

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
