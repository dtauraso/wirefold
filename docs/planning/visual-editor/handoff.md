# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-09 — task/spec-go-backend-ts-frontend, pushed; the Go/TS split is SETTLED: diagram (Go) vs viewpoint (TS); spec doc restructured 16→9 tabs)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed, tree clean. Latest pushed commit: `73de19cb`.
- **NOTE:** `topology.json` has the git skip-worktree bit SET — editor churn stays out of git. Deliberate changes require `git update-index --no-skip-worktree topology.json` first, then re-set after.
- All work this session was on the **spec doc** (`docs/go-authoritative-clock/index.html`). No Go/TS code changed; nothing merged to main.

### The settled split (this session's headline)

The Go/TS ownership boundary was fully worked out and the spec reconciled to it. The line is **diagram vs viewpoint**:

- **Go owns the diagram** — the nodes/edges/beads, the 3D math FOR the diagram (curve control points, arc length, node/port world positions, per-frame bead positions), the animations, the clock + pulseSpeed, and the per-wire fade flag. Go already has the static-geometry math (`curve_params.go: BezierArcLength` / `PortCurveArcLength`, `port_geometry.go: nodeWorldPos` / `portWorldPos`) — the TS twins (`buildEdgeCurve`, `rfArcLength`) are duplicates to delete, not ports.
- **TS owns the viewpoint** — camera, scene navigation (orbit/pan/zoom), projection, raycast-picking, GPU render. TS keeps **r3f/three.js for the 3D scene-navigation machinery** (Go lacks the libs to replace it cheaply: mathgl gives matrices but not the scene/raycast/nav machinery); the control *feel* stays bespoke (substance — not a controls library). "TS computes none" means none of the *diagram*; the viewpoint math is TS's.
- **Input split:** navigation input (pointer/wheel for the camera) stays in TS; only **action input** — CRUD carrying the picked Go id — goes to Go.
- **Store:** the zustand store is a **no-op holder** — Go is the sole writer of its data (NOT removed, NOT "moved to Go"). Camera/viewer state is separate, TS-local.
- **CRUD surfaces** (TS, talk to Go): play/pause, menu, click events.

This reverses an earlier same-session framing ("Chain 2" / "Go owns the camera / all input to Go"); that framing is now superseded across the spec (old Inconsistencies #2 → SUPERSEDED by #12).

### The only new Go build — Chain 1 (the PacedWire rewrite)

Go owns no clock today: it emits discrete `send`/`done` events with a one-shot `arcLength`/`simLatencyMs` budget, and **TS** tweens with r3f's `useFrame` clock. The new Go work:
1. A monotonic **clock + per-frame stepping** (stdlib `time`; not a 3D problem).
2. **Per-frame bead-position evaluation** — eval each bead's `t` from the clock × pulseSpeed and the bézier (control points already exist) → 3D position, streamed per frame. The out-stream changes shape: discrete events + budget → a per-frame position stream.

Go needs **no 3D-math/projection library** — camera/projection/picking are TS (three.js). Bead eval is a few vector lerps over existing control points. Camera + picking are **TS, not new Go builds** (the earlier "Chain 2" is deleted).

### The Bridge — JSONL protocol (in the Bridge tab)

- **JSONL over stdio**, both directions, one object per line — same framing the probe logs use.
- The VS Code **extension host is a dumb pipe** — forwards each line verbatim, no logic/transform. **stdout carries protocol only** (logs → stderr / `.probe/*.jsonl`).
- **One id space — Go's.** Every object Go streams to TS carries its Go id; a user interaction forms its outbound message directly from that id (e.g. `{"t":"node-move","id":"i0",…}`). TS keeps no TS-specific ids and does no interaction→component lookup.
- **Escape hatch (unbuilt):** a localhost WebSocket from the webview straight to a Go server — only if `postMessage` ever bottlenecks.

### Active experimental network — UNCHANGED

`topology.json` still holds only `in08` (Input, init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor) + edge `i0ToI1`. `topology.inactive.json` (editor never reads it) holds the inactive nodes/edges. Restore by copying objects back.

### The in08↔i0 handshake — spec'd, NOT yet coded

- `in08` loop: read the signal on wire `i0→in08` (1/0); `i += signal` (1 advances the read head, 0 holds); send `Init[i]` to `i0`.
- `i0` loop: held copies `in08`'s value (init `-1`); if the arriving value DIFFERS from held → set held, send `1` to `in08`; else send `0`; forward held value to `i1`.
- `i1` loop: receives `i0`'s held value; it is a SINK (outputs inactive); behavior beyond receiving is UNSPECIFIED.
- Needs TWO new wires (`in08→i0`, `i0→in08`) + new PORTS on the Input and ChainInhibitor kinds (Go struct fields) + `i0`'s held init `0`→`-1`. None exists in code yet. The goroutine graph now draws the handshake routed through TWO PacedWire goroutines (one per direction), matching `i0→i1`.

### The PacedWire loop — spec'd, NOT yet coded

1. Add the beads it received this round to the set it already holds (multi-bead).
2. Each round, for every bead, compute its next position from the Go clock and the human-speed slowdown (`pulseSpeed`).
3. If that position is still on the wire, send the bead's 3D position to TS.
4. If past the end, the bead is done — hand the VALUE the bead holds to the destination node and remove the bead (the off-the-end position is never sent to TS).
5. Loop.

### Spec doc structure — restructured to 9 tabs

`docs/go-authoritative-clock/index.html` was merged 16→9 tabs: **The split · Goroutines · TS · The Bridge · Clock · TS → Go · Plan · Verify · Tracking**. Each absorbed tab is now a `<h2>` section in its new home (The split = Overview→Division→Contract; Goroutines = table+graph; TS = code+graph; Clock = Clock Change+Play/Pause; Plan = Plan+Phases; Tracking = Doc Updates→Inconsistencies). The inline `<script>` wires tabs dynamically by `data-panel`.

### What is on main — UNCHANGED

Nothing merged this session. Main still describes the OLD model. All the above lives only on this branch.

### OPEN ITEMS / NEXT

1. **The code rewrite is the headline.** Chain 1 (clock + per-frame bead eval = the PacedWire rewrite) is the first domino. The Go/TS code still implements the OLD model (consumeGated/single-bead/full ring; TS owns the clock + animation and computes geometry).
2. **The in08↔i0 handshake** needs 2 new wires + new ports on Input/ChainInhibitor + `i0` held init `-1` (Go code).
3. **Camera/picking are TS** (three.js) — not a Go build. r3f stays for 3D scene navigation; the control feel is bespoke (no OrbitControls).
4. **Store** = no-op holder, Go sole writer — a TS-side change when the rewrite lands.
5. **Slot terminology still open** (Inconsistencies #5/#6/#7): the Overview "slots and wires" phrasing (and a couple other spots) should become "held state and wires" — deliberately left out of the ownership pass.
6. **The node-contract principle is deliberately UNPINNED** (totality in / no guarantees out; reliability lives in the node, not the channel). Let it manifest in code — do NOT re-pitch pinning it to MODEL.md/memory.
7. **Merge decision** — run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend` before merging; needs explicit sign-off.

### Model contract (target vs code)

`MODEL.md` is authoritative for the TARGET. The spec doc now fully and self-consistently describes the Go/TS split (diagram vs viewpoint), the JSONL bridge, and the single Go id space. The **code still implements the OLD model**. The rewrite is pending; Chain 1 is the first step. The firing-window "Needs confirmation" still stands (the W coincidence-window is its concrete local answer, unpinned because ReadGate/InhibitRightGate are inactive).

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Now 9 tabs. Add a tab with a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` in the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root`.
- **Active topology** is `in08`/`i0`/`i1`. `topology.json` is skip-worktree — deliberate edits require clearing the bit first.
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched; screenshot the extracted SVG, not the whole page.)
- For Go/TS code work: after a TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after a Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook (the banned-term checker script was deleted in a prior session — do not expect it).

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
