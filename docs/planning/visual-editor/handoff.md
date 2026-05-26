# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — audit recut: deferral bucket eliminated; items removed or promoted to accepted-for-build.)

**Active branch:** `task/feature-audit`. Branched off main after editor-r3f was merged at ccbff474.

Branch-local doc: `docs/planning/visual-editor/feature-audit.md` (frontmatter `branch: task/feature-audit`). Will be stripped by `tools/strip-branch-local-docs.sh` before any merge unless relocated.

### Last completed — second recut: deferral bucket eliminated

The "Deferred Industry Patterns (14)" bucket was deleted in full. Each item was individually triaged:

- **11 removed outright** as not-worth-building. Includes auto-layout, which was removed on substrate grounds: node arrangement is dictated by topology+timing, so an aesthetic layout engine breaks the algorithm — it is not a deferred feature, it is a wrong feature.
- **3 promoted into new §3c Accepted for Build:** bend points/waypoints, multi-node alignment guides, undo coalescing at gesture level.

The gap structure is now THREE causes (§3a / §3b / §3c):

- **§3a Cutover Debt (15 total):** worked in RF editor, lost or half-wired in the R3F move. 10 restore-parity items (node delete, edge delete, multi-select, edge reconnect, node palette, sublabel inline edit, PseudoPanel, port drag, edge-kind context menu, edge midpoint drag); 5 half-wired (undo, view-save-on-settle, fit-view hotkey, folds-mesh, z-coord).
- **§3b Never-Specced (1):** Fold Go primitive — needs explicit yes/no decision (become a Go substrate node, or stay view-state forever).
- **§3c Accepted for Build (3):** bend points/waypoints, multi-node alignment guides, undo coalescing at gesture level.

**New scorecard:** 26 implemented; 15 cutover-debt (10 restore-parity + 5 half-wired); 1 never-specced; 3 accepted-for-build.

### Open decisions / next

**(a) feature-audit.md fate:** still branch-local (frontmatter `branch: task/feature-audit`), will be stripped by `tools/strip-branch-local-docs.sh` on merge. Decide relocate-to-survive vs let-it-strip. Relocation likely worth it now that it's a clean three-bucket reference.

**(b) Fold decision:** the only open never-specced item — resolving it zeroes that bucket. Needs explicit yes/no: become a Go substrate node, or stay view-state forever.

**(c) audit-correctness.md stale claims:** H1 and H2 claim `setSpecMeta` and `markViewSynced` are "never called" — both are factually wrong against current code (`setSpecMeta` IS called store.ts:66; `markViewSynced` IS called store.ts:76). Annotate with a correction note or drop before merge. Correction note is preserved in feature-audit.md §4.

**(d) Highest-friction cutover-debt:** node/edge delete and multi-select — were present in the RF editor, conspicuously absent in 3D editor. Pick one as the next task branch when friction justifies it.

**(e) Interactive view-save gap:** `markViewSynced` is not called after camera moves or node drags, so positions are lost on reload without a manual Save. Small scope, clear contract.

**(f) topology.json working-tree modification:** node-drag view positions are modified but uncommitted intentionally. Do NOT stage or discard.

**(g) Latent substrate-contract question:** is pulse arrival anchored to logical simTime or to geometric edge length? If geometric, that is a contract violation — arrival order must be arrangement-invariant, independent of any layout feature. Read `pump.ts` / `pulse-state.ts` to determine which governs travel time before closing this question.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — single zustand source of truth (nodes/edges/selection, load/save actions). `setSpecMeta` called at :66; `markViewSynced` called on loadView at :76.
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts `{ type: "ready" }` to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/three/pump.ts` — read from the store.
- `tools/topology-vscode/src/webview/three/pulse-state.ts` — R3F pulse read-store (getPulseMap, setPulse).
- `tools/topology-vscode/src/webview/types.ts` — local `RFNode`/`RFEdge` type aliases (no reactflow import).
- `tools/topology-vscode/src/webview/state/adapter/{spec-to-flow,flow-to-spec}.ts` — pure adapters, RF-free.
- `tools/topology-vscode/src/webview/rf/` — two residual re-export/metadata files (`adapter.ts`, `animation-fields.ts`); folder name is a misnomer post-retirement.
- `tools/topology-vscode/src/webview/schema/` — node-defs.ts + registry.ts (relocated from rf/).
- `docs/planning/visual-editor/feature-audit.md` — branch-local audit doc (frontmatter `branch: task/feature-audit`).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Unchanged by the
3D move — going 3D is a medium change; the Go substrate,
slot-phase/AND-gate/backpressure model, and `pump.ts` firing logic stay
untouched.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change (deferred branch): `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

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
