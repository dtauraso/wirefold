# CLAUDE.md

## Substrate model — read first

Before changing anything in the **Go substrate** (`nodes/`, `nodes/Wiring/paced_wire.go`,
`nodes/Wiring/loader.go`, `nodes/Wiring/builders.go`) or the **pump** (`tools/topology-vscode/src/webview/three/pump.ts`),
read [MODEL.md](MODEL.md). It pins the substrate model and the banned
vocabulary that signals drift. If your reasoning uses banned
vocabulary, you are in the wrong frame — stop and re-derive from the
model. Do not propose multi-step plans with options for substrate/wire
work; state the next single concrete step and wait.

## Core concepts and backpressure

Both live in [MODEL.md](MODEL.md): the inhibitor chain, edge nodes,
partition nodes, AND-gate tree, lateral inhibition, slot-in-node
backpressure, and round-close stepping. The "Substrate model"
pointer at the top of this file is the only entry point you need.

## Substrate primitive landing rule (narrowed)

**Node kinds:** adding a kind requires three things in the same commit:
1. One file `tools/topology-vscode/src/webview/rf/nodes/<Kind>Node.tsx` —
   the React Flow custom node component (render only; no substrate logic).
2. Register the kind in the single registry:
   `tools/topology-vscode/src/webview/schema/registry.ts`.
   `RF_NODE_TYPES` is derived from `NODE_DEFS` keys — the PascalCase spec
   kinds emitted by codegen — verbatim. The RF node type name equals the
   spec kind exactly (e.g. spec kind `MyKind` → RF type `MyKind`); no
   conversion, no extra registration.
3. The Go node package under `nodes/<Kind>/`.

**Wire props:** Wire props (`WireProps` from `tools/topology-vscode/src/schema/wire-defs.ts`)
are part of `EdgeData` (`tools/topology-vscode/src/webview/types.ts`) and flow from
the store into `SingleEdgeTube` via `GraphEdges` in
`tools/topology-vscode/src/webview/three/ThreeView.tsx`. A new wire prop must be
added to `WireProps` in `wire-defs.ts` and threaded through `SingleEdgeTube` in the
same commit it is used; otherwise the editor path is silently incomplete.

**Drift rule:** if TS code outside `pump.ts` starts accumulating slot-phase,
backpressure, or firing-rule logic, that is drift — those belong in Go.

## Node kinds

Active node kinds live under
`tools/topology-vscode/src/webview/rf/nodes/` (R3F render components) with kind
metadata in `tools/topology-vscode/src/webview/schema/`. Each `<Kind>Node.tsx` is
a static React Flow custom node — render only. The per-kind role is
documented on the component itself rather than duplicated here.

## Memory

Project memory lives in `memory/` at the repo root, one file per
memory (auto-memory naming convention: `project_*`, `feedback_*`,
`user_*`). `memory/MEMORY.md` is the index.

## Bash hygiene (keep AI round-trips snappy)

Bash output goes straight into the AI's context. Wide-fan commands
return hundreds of irrelevant matches from `node_modules/`, planning
docs, and the auto-memory dir, costing tokens and time.

- **`grep`**: always scope. For code, use `--include="*.ts" --include="*.tsx"`. For repo-wide searches, exclude noise: `--exclude-dir={node_modules,out,.git,handoff-archive,memory}`.
- **`find`**: never run `find .` unguarded — `tools/topology-vscode/node_modules/` has multi-MB files. Use `-not -path "*/node_modules/*" -not -path "*/out/*" -not -path "*/.git/*"` or just scope to a specific subtree.
- **`ls`**: prefer a specific subdir over wide listings; pipe to `head` if you only need a sample.
- Planning docs (`docs/planning/visual-editor/`, `memory/`) contain domain vocabulary — grep them only when the question is about *planning state*, not when looking for code.

## Workflow

- **Commit and push freely on task branches.** Per-commit sign-off is no longer required (relaxed post-v0; editing or reverting committed code is cheap). Sign-off IS still required for: merging a task branch into `main`, force-pushes, branch deletion, dependency removal, and any other destructive or shared-state action called out in the system prompt's "Executing actions with care" section.
- Build and run before reporting a change as ready; verify output matches previous run. If verification fails, fix forward or revert — don't leave broken state on the branch. **`tsc --noEmit` alone does not refresh `out/webview.js`** — if a TS change needs to be exercised in the live editor, run `npm run build` (the Stop hook does this automatically; manual subagent verifications do not).
- One logical change per commit.
- Push each commit to the current task branch.
- **Cost markers:** only record a `($N.NN)` cost marker on a commit (or bundle of commits) when the work was sized at **≥$5 expected** beforehand. Sub-$5 work lands without a marker. Bundle small commits into ≥$5 chunks for marker purposes. Pre-v0 sub-$5 markers stay as historical record but are no longer the convention.
- **Branch hygiene:** task-named branches (`task/<short-kebab-description>`) that merge to `main` quickly. Avoid long-lived feature branches like the v0 `visual-editor` pattern.
- Channel names encode which two nodes are connected — preserve this convention.
- **Medium vs. substance.** Before adopting a **medium** dependency (rendering library, framework, parser, bundler, file watcher, test runner, package manager, language/runtime version, editor integration), explicitly ask "what's the dominant choice the rest of the world converged on for this category?" and justify deviating if not adopting it. The medium is where industry has solved your problem; being weird there is pure overhead. Do **not** apply this heuristic to the **substance** of the system — the execution model, what a node is, how time/ticks work, what a wire is, how nodes coordinate, the substrate that runs nodes. Industry defaults there encode "logic in procedures, topology as plumbing," which is the inversion this project exists to challenge. For substance, ask "what does this system actually need?" and ignore industry — the whole point is that the answer is different. (Prior failure: the await/Promise substrate was the industry-correct JS translation of goroutines+channels, and it hid pacing inside the event loop, coupling nodes that should have been independent. Right answer for the medium, wrong answer for the substance.)

## Planning docs are branch-local

Planning docs (anything under `docs/planning/` except `handoff.md`, `session-log.md`,
and `continuation-prompt-template.md`) are authored on the
task branch where the work happens and do not ride the merge to main. Each new planning
doc starts with frontmatter naming its originating branch:

```
---
branch: task/<short-name>
---
```

Before merging a task branch, run `tools/strip-branch-local-docs.sh task/<branch>` to
remove all docs tagged with that branch. The script is the source of truth — no judgment
per file required at merge time.

This rule is forward-only. Existing untagged docs stay until individually judged.

## Session handoff

Live state of the active task branch lives at
[docs/planning/visual-editor/handoff.md](docs/planning/visual-editor/handoff.md).
Read it first — it names the branch, contract status, open options,
and the ALWAYS clause that keeps the loop self-perpetuating. Schema
is in
[docs/planning/visual-editor/continuation-prompt-template.md](docs/planning/visual-editor/continuation-prompt-template.md).
Do not rely on chat history for handoff context; the next session may
be a fresh model with no transcript.

## Posture (post-v0)

Visual editor reached v0. New work is friction-driven, not phase-driven (per-phase plans are archived under `docs/planning/visual-editor/archive/`); justify changes from real-world editor use logged in [session-log.md](docs/planning/visual-editor/session-log.md). Working mode: user drives the editor and narrates; assistant logs and makes changes.

## Delegation

**When to delegate (apply each prompt):** delegation is the default, not the exception.

- More than ~2 read-only lookups on a topic → spawn an `Explore` subagent with `model: "haiku"`.
- A clear, scoped edit spec (rename, flag removal, mechanical refactor) → spawn a general-purpose subagent with `model: "sonnet"`.
- A single targeted Read/grep with a known path → just do it inline; delegation overhead isn't worth it.
- **Main session never writes.** Every `Edit`, `Write`, or scripting `Bash` call goes to a subagent. Main session outputs prose, decisions, and dispatch prompts only.

If the main session catches itself doing executor-style work, that's a miss — note it and route the next similar task to a subagent.

**Model routing:**

| Work type | Model |
|---|---|
| File scans, log/grep, reading session-log or memory to surface a fact, simple multi-file finds, running deterministic audit scripts | `haiku` |
| Mechanical edits with a clear spec, refactors inside a single file, writing tests against an existing pattern, doc updates, running CI-backed audits (1–3) when red and triaging output, follow-up fixes from audit findings | `sonnet` |
| Planning a new task branch, judgment-heavy audits (6 security, 9 complexity, 10 architecture, 19 reading-trip economy), debugging non-obvious behavior, designing spec/view split when adding fields | Opus (default) |

Apply via `Agent({ model: "sonnet", ... })` or by spawning a subagent of the matching kind. If unsure, downshift first and escalate only if the cheaper model produces poor output — the cost asymmetry favors trying cheap first.

**Prompt style (~15 lines):** one-line goal; files to read (paths only); bulleted concrete edits with `file:line` when known; one-line verify command; one-line constraints (branch, no merge, no amend, push or not). Skip rationale paragraphs, alternative-considerations, and "if ambiguous…" hedging — the agent will ask if blocked.

## Language / runtime

Go 1.23.0 — `github.com/dtauraso/wirefold`
