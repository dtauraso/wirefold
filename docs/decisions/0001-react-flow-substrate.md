# 0001 — React Flow as editor substrate

- **Status:** Accepted
- **Date:** 2026-05-01
- **Deciders:** solo

## Context

The visual topology editor lives inside a VS Code webview and must
turn diagram edits into running Go via `topogen`. The original webview
rendered with lit-html + plain SVG and hand-rolled all gestures
(selection, drag, pan/zoom, eventual port-drag, fold geometry, undo).

Two prior approaches were tried and rejected:

1. **Hand-rolled everything on lit-html + SVG.** Viable through Phases
   1–2 (codegen wiring, sidecar split, saved views, bookmarks,
   build-and-run), but Phase 3+ gestures are each their own custom
   state machine, hit-testing layer, and ghost-edge geometry. Cap-cost
   estimates put Phase 3 alone at ~2.5 caps with this substrate.
2. **A standalone browser editor decoupled from codegen** — the
   deleted `tools/topology-editor/` tree (commit `df2b101`). The
   editor was nice but produced a spec nothing read. The lesson was
   *the editor must live inside the codegen pipeline*, not *React
   Flow is wrong*.

Meanwhile, React Flow (now `@xyflow/react`) reached the maturity
threshold for this project's needs in **late 2022 with v11**:
multi-handle ports, subflows / parent-child nodes, and fully
controlled flows arrived together. Earlier versions would have
forced compromises (single-handle ports, awkward controlled state)
that defeat the purpose of adopting the library.

## Decision

Adopt **React Flow inside the existing VS Code webview** as the
rendering and interaction substrate. `topogen` remains authoritative;
`topology.json` stays the source of truth; React Flow's internal
node/edge store is a *view* fed by adapters. The webview keeps its
existing extension-host integration (debounced save, sidecar, build
and run, master playback clock).

Migration happened during Phase 3 of the visual-editor plan
(archived; see git history),
costing ~1.5 caps one-time. Phases 1–2 features (pan/zoom save,
saved views, animation-driven node state) re-port onto React Flow
primitives during the migration; the underlying behaviors and file
formats are unchanged.

## Consequences

**Enables:**

- Phase 3 selection, marquee, port-drag, edge-edit, palette become
  library calls instead of custom code (~½ cap each → ~⅛ each).
- Phase 4 fold/unfold leans on React Flow's subflow primitive instead
  of hand-rolled boundary-edge re-routing (~2 caps → ~1).
- Phase 8 undo/redo via React Flow + Zustand history pattern instead
  of bespoke command-pattern infrastructure.
- Hardened library code replaces the categories most likely to
  accumulate long-tail bugs (pointer events, coordinate transforms,
  hit-testing).

**Costs:**

- Two sources of truth (spec + React Flow store) requiring adapter
  discipline.
- Less control over rendering style — the SVG style guide is partially
  enforceable through className props but not fully.
- The animation-driven node state path moves from direct SVG attribute
  mutation to `setNodes()` calls, adding a re-render cost per frame
  that needs measuring.
- Tied to React Flow's release cadence and any future renames (the
  v10 → v11 → `@xyflow/react` rename in 2023 is the precedent).

**Forecloses:**

- Switching to a non-React rendering layer (Svelte, Solid, vanilla)
  without a substrate-level rewrite. The team-of-one solo-developer
  context makes this acceptable.

## Alternatives considered

Limited to substrates actually built or attempted on this project, not
hypothetical libraries that were never in the running.

- **Hand-rolled lit-html + SVG (the status quo through Phases 1–2).**
  Rejected for Phase 3 onward: gesture cost is the dominant cap drain
  in the remaining plan, and hand-rolled hit-testing is the
  highest-bug-density category.
- **Standalone browser editor using React Flow, decoupled from
  codegen** (the deleted `tools/topology-editor/`, commit `df2b101`).
  Rejected: the editor produced a spec nothing read. The lesson was
  *the editor must live inside the codegen pipeline*, not *React Flow
  is wrong* — which is why React Flow is reintroduced here, this time
  inside the vscode webview where `topogen` is authoritative.
