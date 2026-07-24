# docs/ — index

Entry point for the 30 files under `docs/`. Grouped by topic; one line each.

**Reading the `.html` explainers:** `scripts/block-open-html-hook.py` blocks opening them
in a browser. Read them as text, or use the editor's HTML preview. They are self-contained
static pages (no external assets).

**Planning docs are branch-local going forward** (CLAUDE.md → "Planning docs are
branch-local"): new docs under `docs/planning/` carry a `branch:` frontmatter and are
stripped before merge. The existing untagged ones below predate that rule and stay until
individually judged. `session-log.md` is the one durable exception — it always rides to main.

## Concurrency & lock architecture

The mutex-removal work: each `sync.Mutex`/`Cond` replaced by single-owner state. Start with
`framings.md` for the overview, then the per-lock pages.

| Doc | What it covers |
|---|---|
| [framings.md](framings.md) | The framing ledger — what replaced what, and the architecture built for the old model. No locks remain. |
| [concurrency-map.html](concurrency-map.html) | Map of the concurrency model — goroutines, channels, who owns what. |
| [mutex-architecture.html](mutex-architecture.html) | Overview of the mutex architecture (and its removal). |
| [outbox-architecture.html](outbox-architecture.html) | `outbox.mu` resolved — per-direction channels replaced the shared move queue. |
| [trace-mutex-architecture.html](trace-mutex-architecture.html) | `Trace.mu` resolved — events ride each owner's own stream. |
| [debounced-persister-architecture.html](debounced-persister-architecture.html) | `debouncedPersister.mu` resolved — inline per-caller writes, no shared timer. |
| [layout-holder-architecture.html](layout-holder-architecture.html) | `LayoutHolder.mu` resolved — every caller runs on the owning node's goroutine. |
| [scene-persist-architecture.html](scene-persist-architecture.html) | `scene_persist` — the last unexamined locks; per-writer file ownership. |

## Design specs & audits

| Doc | What it covers |
|---|---|
| [go-authoritative-clock/index.html](go-authoritative-clock/index.html) | Go-authoritative clock design spec — Go owns the one clock. |
| [level4-audit/index.html](level4-audit/index.html) | Level-4 audit of the system. |

## Polar geometry & layout

| Doc | What it covers |
|---|---|
| [polar-sphere.html](polar-sphere.html) | The polar coordinate system for a sphere. |
| [pole-singularity.html](pole-singularity.html) | The layout pole singularity — φ grid vs great-circle bearing. |
| [demos/polar-drag-3d.html](demos/polar-drag-3d.html) | Interactive demo — rotation abc + fixed-increment pole nudge in 3D. |
| [demos/polar-drag-log.jsonl](demos/polar-drag-log.jsonl) | Recorded drag log backing the polar-drag demo. |

## Visual-editor planning (`docs/planning/visual-editor/`)

Durable:

| Doc | What it covers |
|---|---|
| [session-log.md](planning/visual-editor/session-log.md) | Real-world editor session log — the friction record driving new work. |

Planning/spec (untagged, predate the branch-local rule):

| Doc | What it covers |
|---|---|
| [camera-navigation.html](planning/visual-editor/camera-navigation.html) | 3D camera navigation model. |
| [edit-hop-audit.html](planning/visual-editor/edit-hop-audit.html) | Edit round-trip audit — why 12 hops. |
| [node-edges-goroutine-spec.html](planning/visual-editor/node-edges-goroutine-spec.html) | A node runs its own outgoing edges. |
| [sphere-chain-layout-spec.html](planning/visual-editor/sphere-chain-layout-spec.html) | Sphere-chain node layout. |
| [timing-spec.html](planning/visual-editor/timing-spec.html) | Wirefold timing spec. |
| [timing-window.html](planning/visual-editor/timing-window.html) | Timing-window spec. |
| [animation-drag-issues.md](planning/visual-editor/animation-drag-issues.md) | Live-observed open issues in animation & dragging. |
| [double-link-polar-model.md](planning/visual-editor/double-link-polar-model.md) | Double-link polar movement model. |
| [existing-lock-system-record.md](planning/visual-editor/existing-lock-system-record.md) | Lock-system record kept before the double-link rewrite. |
| [layout-on-domain-network.md](planning/visual-editor/layout-on-domain-network.md) | Layout on the domain network (rebuild). |
| [one-clock-sleep-only.md](planning/visual-editor/one-clock-sleep-only.md) | One clock, sleep-only pacing (decided model). |
| [polar-frame-rewrite.md](planning/visual-editor/polar-frame-rewrite.md) | Polar-frame rewrite plan (preserved from a deleted task branch). |

Screenshots: `planning/visual-editor/screenshots/` — panguide triangle-drift (2026-06-17,
×2) and saturated pulse-wires (2026-07-14).
