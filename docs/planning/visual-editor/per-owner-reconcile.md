---
branch: task/per-owner-reconcile
---

# Per-owner buffer reconcile onto latest main

## Goal

Land the per-owner buffer-streaming work (SnapshotState accumulator deleted,
one binary stream per goroutine, Trace decentralized) on top of the current
`main`, which independently rewrote the Go node-move / broadcast / single-edge
model. Produce one clean linear branch off `main` that passes `stop-checks.sh`.

## Three inputs (all committed, all preserved)

- **`main`** `3dca4fe7` — destination. Rewrote the Go model:
  - node-move decentralized to a `nodeMovers` map (per-node mover goroutines)
  - fan-out renamed to **broadcast** (`PortBroadcast` / `AppendBroadcastWithHandle`)
  - single-edge-input / no-fan-in (`check-no-fan-in.sh`)
  - atomics → channels (`centerSnap` removed, no `atomic` in node move)
  - EdgeTube made imperative (`forwardRef` + `useImperativeHandle`, parent `useFrame`)
  - new guards: `check-test-integrity`, `check-no-webview-state`, `check-no-fan-in`
- **`task/per-owner-buffer-rows`** `bfea7ab4` — original branch work, based off the
  old base `d7efc279`. Unique contribution:
  - per-owner stream frames with trailing EVENTS section (`Buffer/stream_events.go`,
    `nodes/Wiring/owner_events.go`, `view_stream.go`)
  - `Buffer.SnapshotState` central accumulator DELETED (`pack.go`/`snapshot.go` gone)
  - Trace collapsed to breadcrumb-only writer; central drain deleted
  - TS: `view-blocks.ts`, `node-stream-blocks.ts`, `edge-stream-blocks.ts`; EdgeTube rewrite
  - Buffer Edge SX..EZ columns removed (endpoint tear was a duplication artifact)
- **`reconcile-scratch`** `46581711` — the rebase-onto-main attempt. Its `e611f120`
  is the per-owner series already replayed **linearly on top of main**; the tip
  `46581711` is a BROKEN WIP partial reconcile (does not build). Kept as reference.

## Current branch state

`task/per-owner-reconcile` is off `main`, currently pointed at `reconcile-scratch`'s
`e611f120` (the replayed series). **It does not build** — that broken tree is the
starting point this doc addresses, not a regression.

## The conflict surface (from the build errors)

The replay put per-owner code beside main's rewritten model; they disagree in:

1. **Broadcast rename** — per-owner `builders.go` calls `OutMulti`; main provides
   `PortBroadcast` / `AppendBroadcastWithHandle`. Adopt main's names.
2. **Node-move model** — `quantized_move.go` / `node_move.go` / `node_mover.go` still
   use old `md.positions`, `md.extRoute`, `moveMsg.PartnerCenter`,
   `nodeMover.neighborCenters`, and old `commitNodeMoveLocal` /
   `neighborSetCRequantize` signatures. Rebuild on main's `nodeMovers`-map model.
3. **`centerSnap` / `atomic`** — removed on main (atomics → channels doctrine). Drop
   the reintroductions.
4. **Buffer edge columns** — per-owner deleted Edge SX..EZ; `buffer_layout_gen_test.go`
   is stale (`SetEdgeRow` arity, `BufEdgeColSX..EZ`). Regenerate + fix test.
5. **TS view path** — `./view-blocks` module + removed exports (`DecodedSnapshot`,
   `getLatestSnapshot`, `decodeSnapshot`); main's `RawHit` now needs `x,y,z`. Port
   per-owner TS consumers onto main's shapes.
6. **Guards** — `check-test-integrity` flags net −97 assertions (legit: retired
   equivalence test + deleted accumulator/drain tests) → needs an
   `[allow-test-weakening]` commit note with reasons; `check-doc-symbols` ghosts
   (`reviseCh`, `validateNoFanIn` in MODEL.md) → delete the stale claims.

## Resolution doctrine (the ordering rule)

**Take main's model wholesale; layer per-owner's buffer / Trace / SnapshotState-deletion
on top of it** — never the reverse. Where per-owner code assumes the pre-main
node-move / broadcast world, rewrite it to main's world. The per-owner contribution is
the *buffer streaming*, not the *node-move mechanics* — keep the former, discard the
latter's now-obsolete scaffolding.

## Execution order (each a clean commit; gate = empty `stop-checks.sh` stdout)

1. **Go Wiring builds** — fix domains 1–3 (broadcast rename, node-move onto `nodeMovers`
   map, drop `centerSnap`/`atomic`). `go build ./...` green.
2. **Buffer/tests build** — regen buffer layout, fix `buffer_layout_gen_test.go`.
   `go test ./Buffer/...` green.
3. **TS view path** — port domain 5 onto main's decode / RawHit shapes. `tsc --noEmit`
   + webview build + vitest green.
4. **Guards** — `[allow-test-weakening]` note listing the retired tests; delete the
   doc-symbols ghost claims. Full suite green.
5. **Report** `git diff main..HEAD --stat` — the net surviving contribution
   ("what's left").

## Open decisions

- **Executor** — inline (recommended: bounded, watched each step) vs. a fresh
  implementer agent (the prior long unwatched run was killed).
- **Scratch branches** — keep `reconcile-scratch` and `task/per-owner-buffer-rows` as
  reference until `task/per-owner-reconcile` is green, then delete on sign-off.

## Guardrails

No merge to main, no push, no branch deletion without sign-off.
