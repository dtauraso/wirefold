---
branch: task/labels-go-backend
---

# Make global-label visibility Go-backed (remove TS-local `globalLabelsHidden`)

> **Status: COMPLETE.** Commits A–D landed (`b3b94cea`, `a0a4fbcc`, `55149853`,
> `5320dc8e`). Verified: `go build ./...` clean, `tsc --noEmit` clean, all parity
> guards (`scripts/stop-checks.sh`) exit 0, `npm run build` clean. Commit E (verify)
> folded into this verification rather than a separate commit.
>
> Recovery note: the implementing subagent committed A–D but then a `git reset`
> orphaned them and its report claimed they were on-branch (they weren't). The 4
> commits were recovered by cherry-pick onto current main; the `ThreeView.tsx`
> conflict with main's badges-toggle was resolved by keeping `badgesHidden` and
> taking the Go-owned `globalLabelsHidden` camera-store read.

## Follow-on: badges folded into Go too (8th family member)

After labels landed, `badgesHidden` (the occlusion-count badge toggle) was the last
remaining TS-decided visibility flag, so it was converted the same way — making it the
8th member of the Go-backed visibility-toggle family. Same 4-phase structure, mirroring
the labels commits member-for-member.

| thing | value |
|-------|-------|
| trace kind | `badges-global` (`KindBadgesGlobal`) |
| Go state | `badgesGlobalVisible` (true = shown); `ToggleBadgesGlobal` / `EmitBadgesGlobal` |
| toggle op | `badges-vis` |
| guide-vis field | `badgesGlobal` (visible sense); `stdinMsg.BadgesGlobal` |
| camera-store / persisted | `badgesHidden` (hidden sense, kept) |

Commits: `f5b99679` (A), `d87dbc06` (B), `4376ce85` (C), `2b72b50f` (D).
Verified independently: commits reachable from HEAD, `go build ./...` clean,
`tsc --noEmit` clean, `scripts/stop-checks.sh` exit 0, `npm run build` clean, tree clean.

With this, **every visibility flag is Go-owned** — TS holds no display decisions, only
rendering. (Note: the implementing subagent for badges reported honestly; the labels
subagent did not — see the recovery note above. Both were verified against `git log`
regardless.)

## Principle (from the user)

> TS is for rendering, not deciding what to render.

Deciding whether labels show is a *decision* → it is state → it lives in Go. TS
renders whatever Go's trace stream says. `globalLabelsHidden` (a `useState` in
`ThreeView.tsx`) is TS deciding, so it is drift.

## Context

There is an established **family of six Go-backed visibility toggles** (tori,
scene-poles, node-poles, angle-labels, sel-sphere-poles, handholds), each with the
identical wiring:

- Go: `xxxVisible bool` field (default true) in `MoveDispatch`, `ToggleXxx`,
  `EmitXxx`; a `Kind*` trace event; `SetGuideVisibility(...)` batch-set for the
  startup push.
- Bridge: an `op: "xxx-vis"` toggle edit + the `op: "guide-vis"` batch push.
- TS: `pump.ts` case → `camera-store` setter; components read the store;
  persisted in `topology.scene.json` and re-pushed via `guide-vis` on load so it
  survives a Go respawn.

Labels is the **only** visibility toggle not in this family — it is decided in TS.
This branch makes it the **7th family member**. The persisted field name
`labelsGlobalHidden` (true = hidden) is kept for backward-compat; internally Go
and the wire use the family's **visible** sense (true = shown), flipped only at the
two persistence/render boundaries.

## Naming

| thing | value |
|-------|-------|
| trace kind | `labels-global` (`KindLabelsGlobal`) |
| Go state | `labelsGlobalVisible bool` (true = shown) |
| Go methods | `ToggleLabelsGlobal`, `EmitLabelsGlobal` |
| toggle op | `labels-vis` |
| guide-vis field | `labelsGlobal: boolean` (visible sense) |
| camera-store | `labelsGlobalHidden: boolean` (true = hidden) |

## Phases / tasks / commits

Each commit keeps the parity guards green (edit-op, message-kind, trace-kind,
generated) and compiles Go + TS.

### Commit A — trace kind + render store (Go emits, TS receives)
- [ ] A1 `Trace/Trace.go`: add `KindLabelsGlobal`; add to `TraceEventKinds`; add
  `func (t *Trace) LabelsGlobal(visible bool)`; add `KindLabelsGlobal` to the
  grouped visibility-toggle MarshalJSON case.
- [ ] A2 `nodes/Wiring/node_move.go`: add `labelsGlobalVisible bool` field
  (default true), `ToggleLabelsGlobal`, `EmitLabelsGlobal`.
- [ ] A3 regen `trace-kinds.ts` (`npm run gen:node-defs`).
- [ ] A4 `pump.ts`: `case "labels-global"` → `setLabelsGlobalHidden(!e.visible)`.
- [ ] A5 `camera-store.ts`: `labelsGlobalHidden: boolean` (default false) +
  `setLabelsGlobalHidden`.

### Commit B — toggle op (the live decision path)
- [ ] B1 `messages.ts`: add `| { type: "edit"; op: "labels-vis" }`; add trace
  event `{ kind: "labels-global"; visible: boolean }`; parse case for `labels-vis`.
- [ ] B2 `nodes/Wiring/stdin_reader.go`: `case msg.Op == "labels-vis"` →
  `md.ToggleLabelsGlobal(tr)`.
- [ ] B3 `handle-message.ts`: forward `labels-vis`.

### Commit C — wire the button, remove the TS-local decision
- [ ] C1 `camera-ui.tsx` `GlobalLabelsToggle`: read `camera-store`; click
  dispatches `{ op: "labels-vis" }` (fire-and-forget); no local state write.
- [ ] C2 `ThreeView.tsx`: delete `globalLabelsHidden` `useState` + `toggleGlobalLabels`
  view-state write; read `labelsGlobalHidden` from `camera-store`; render gate uses it.

### Commit D — persistence: extend guide-vis push with labels (respawn survival)
- [ ] D1 `messages.ts`: `guide-vis` gains `labelsGlobal: boolean`; parse validates it.
- [ ] D2 `handle-message.ts`: forward `labelsGlobal` in the guide-vis writeStdin.
- [ ] D3 `nodes/Wiring/stdin_reader.go`: `stdinMsg` gains `LabelsGlobal bool`;
  guide-vis passes it to `SetGuideVisibility`.
- [ ] D4 `nodes/Wiring/node_move.go`: `SetGuideVisibility` gains the labels param
  (+ `EmitLabelsGlobal`).
- [ ] D5 `main.tsx`: `guidePush` sends `labelsGlobal: viewerState.labelsGlobalHidden !== true`.

### Commit E — verify
- [ ] E1 `tsc --noEmit` clean.
- [ ] E2 all guards green (`scripts/stop-checks.sh`), esp. the 4 parity guards.
- [ ] E3 `npm run build` succeeds.
