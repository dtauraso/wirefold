# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-08 — task/spec-go-backend-ts-frontend, pushed; MODEL SHIFT: send-gating removed, local-units/no-guarantees, active net in08/i0/i1)

- Active branch: `task/spec-go-backend-ts-frontend`, pushed, tree clean. Latest pushed commit: `b33d7349`.
- **NOTE:** `topology.json` has the git skip-worktree bit SET — editor churn stays out of git. Deliberate changes require `git update-index --no-skip-worktree topology.json` first, then re-set after.

### The model shift (this session's headline)

This session fundamentally simplified the pinned model, away from delivery/coordination machinery toward local units with no guarantees:

- **Send-gating was REMOVED** from MODEL.md (the `## Send gating` section is gone, replaced by `## Sending`: a node places a bead on its outgoing wire whenever its own rule says to — no clear/busy, no ack, no back-pressure; a wire may carry more than one bead at once; coordination is the topology, not a delivery guarantee). The removal swept CLAUDE.md (the `## Core concepts and send gating` heading + drift rule) and the spec doc too. The Go code still implements the OLD send-gating (consumeGated/clear-busy) — that is the pending rewrite.

- **The conceptual conclusion** (present for context, deliberately NOT pinned as doctrine): the topology can be unreliable but the node must be reliable; a node trusts and owes only its own input-handling and output (totality in, no guarantees out); reliability lives in the node, not the channel; forcing reliability onto the channel generated the deadlock/seed/coupling. David asked NOT to add these principles to MODEL.md/memory ("don't add these things") — they are meant to manifest in the code/structure, not in prose. A future session should NOT re-pitch pinning them.

- **The "neuron" analogy** was used in conversation then scrubbed: removed from the memory file (renamed `feedback_node_model_not_networking_handshake.md`) AND from commit messages via a history rewrite (force-push with `--force-with-lease`; trees verified byte-identical). Zero "neuron" in content or history.

- **Clock** was pinned earlier this session: one system monotonic clock Go reads; all other timing is code arithmetic on its readings (`pulseSpeed` = the human-speed slowdown).

- **Memory added:** `memory/feedback_node_model_not_networking_handshake.md` — nodes do local work + drive outputs; no TCP-handshake/ack-nack/send-gating delivery guarantees. (MEMORY.md indexed.)

### Active experimental network

`topology.json` holds ONLY the active set: nodes `in08` (Input, init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor); one active edge `i0ToI1` (`i0`→`i1`).

`topology.inactive.json` (NEW sibling file the editor never reads, so it survives editor saves) holds the inactive nodes `bootstrap_rg`, `readGate1`, `inhibitRight0` and 6 inactive edges. Restore by copying objects back into `topology.json`.

The spec doc (`docs/go-authoritative-clock/index.html`) was reduced to be ONLY about `in08`/`i0`/`i1`: goroutines table = Input(`in08`)/i0/i1/PacedWire rows; ring/ChainInhibitor-extra/InhibitRightGate/ReadGate/bootstrap content removed; diagram reworked to `in08`⇄`i0` + `i0`→`i1`; the W coincidence-window note removed (its gates are inactive).

### The in08↔i0 handshake — spec'd in the goroutines table but NOT yet wired in topology/code

- `in08` loop: read the signal on wire `i0→in08` (1/0); `i += signal` (1 advances the read head, 0 holds); send `Init[i]` to `i0`.
- `i0` loop: held copies `in08`'s value (init `-1`); if the arriving value DIFFERS from held → set held, send `1` to `in08`; else send `0`; forward held value to `i1`.
- `i1` loop: receives `i0`'s held value; it is a SINK (its outputs are inactive); behavior beyond receiving is UNSPECIFIED (open).
- This needs TWO new wires (`in08→i0`, `i0→in08`) and new PORTS on the Input and ChainInhibitor node kinds (Go struct fields), plus `i0`'s held init changed `0`→`-1`. None of that exists yet — pending Go code.

### The PacedWire loop — the most fully-specified piece, in the goroutines table

"PacedWire cares only about the beads it receives and the beads it sends":

1. Add the beads it received this round to the set it holds (multi-bead, not single).
2. Each round, for every bead, compute its next position from the Go clock and the human-speed slowdown (`pulseSpeed`).
3. If that position is still on the wire, send the bead's 3D data (its position) to TS.
4. If that position is past the end of the wire, the bead is done — hand the VALUE the bead is holding to the destination node and remove the bead (the off-the-end position is never sent to TS).
5. Loop.

---

### What is on main — UNCHANGED (nothing merged)

Nothing merged this session. Main still describes the OLD model (send-gating, the full ring, etc.). All the above lives only on this branch. Main is where the prior sessions left it (glass node bodies + init beads; dead slot-trace removal; `DEFAULT_EDGE_KIND`; level-4 audit site; F1/F2/F3 fixes). See `git log main`.

### OPEN ITEMS / NEXT

1. **The code rewrite is the headline.** Target is now MUCH simpler: no send-gating/clear-busy/ack; wires multi-bead (PacedWire loop above); nodes emit on local state; PacedWire sends on-wire 3D positions to TS and hands a finished bead's value to the node. The Go/TS code still implements the OLD model.
2. **The in08↔i0 handshake** needs 2 new wires + new ports on Input/ChainInhibitor + `i0` held init `-1` (Go code).
3. **i1's behavior** beyond receiving is unspecified — sink for now.
4. **The node-contract principle** is deliberately unpinned — let it land in code, do not re-pitch pinning it to MODEL.md/memory.
5. **The W coincidence-window** is the concrete (local) answer to MODEL.md's firing-window "Needs confirmation" — a windowed gate spans a duration to scope which inputs are considered together; not pinned because ReadGate/InhibitRightGate are inactive.
6. **Minor history hygiene:** a couple of Inconsistencies-tracker entries phrase the retired send-gating apparatus in present tense — fold into a future cleanup commit.
7. **Merge decision** — run `tools/strip-branch-local-docs.sh task/spec-go-backend-ts-frontend` before merging; needs explicit sign-off.

### Model contract (target vs code)

`MODEL.md` (`../../../MODEL.md`) is authoritative for the **TARGET**: one Clock; `## Sending` (nodes emit on local state, multi-bead wires, no delivery guarantee/back-pressure/clear-busy); active network reduced to `in08`/`i0`/`i1`. The **code still implements the OLD model** (consumeGated, single-bead, the full ring). MODEL.md leads; the rewrite is pending and now aims at the simpler no-guarantee model. The firing-window "Needs confirmation" still stands.

## Dev-loop

- **The spec is a single self-contained HTML file** (`docs/go-authoritative-clock/index.html`) — no build. Tabs: add a `<button class="tab-btn" role="tab" data-panel="X">Label</button>` to the `.tabs` nav and a `<div class="panel" id="panel-X" role="tabpanel">` panel; the inline `<script>` auto-wires by `data-panel`. Palette CSS vars in `:root` (--go-hue #5ec4a0, --ts-hue #c07ef8, --accent #7c9ef8, --warn #d4a017, --good #3db87a, --info #7c9ef8, --muted #808090, --mono).
- **Active topology** is `in08`/`i0`/`i1` (not the old ring). `topology.json` is skip-worktree — deliberate edits require clearing the bit first.
- **Verify a diagram visually:** extract its `<svg>…</svg>`, wrap in `<body style="background:#0e0e10">…</body>`, screenshot headless — `"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless=new --disable-gpu --screenshot=/tmp/x.png --window-size=W,H file:///tmp/x.html` — then read the PNG. (Tabs are JS-switched, so screenshot the extracted SVG, not the whole page.)
- For Go/TS code work: after TS edit `npm run build` from `tools/topology-vscode/` (and `npx tsc --noEmit` for removals); after Go change `go build ./...` + `go test ./nodes/...`; `tools/check-generated.sh` after shared `CurveParam*` / SPEC.md `## View` changes. The ring has no headless run (`go run .` deadlocks after the first hop). Guard scripts run via the Stop hook (note: the banned-term checker script was deleted in a prior session — do not expect it).

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
