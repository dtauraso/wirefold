# Memory Index

## Background (stable rules — workflow, hygiene, ergonomics)

These rarely change; skim once per session and apply throughout.

- [user_background.md](user_background.md) — User designs concurrent dataflow systems with circuit-style wiring
- [feedback_code_self_defends.md](feedback_code_self_defends.md) — Solid code structure preferred over memory entries for preventing AI drift toward industry defaults
- [feedback_branch_cleanup.md](feedback_branch_cleanup.md) — Delete task branches locally and on remote once merged into main, without re-asking
- [feedback_memory_location.md](feedback_memory_location.md) — Save memory files only to repo `memory/`; skip the local Claude memory dir for this project
- [feedback_bash_cwd_persistence.md](feedback_bash_cwd_persistence.md) — Bash cwd persists across calls; use absolute paths for destructive ops

## Active (project/substrate state — may go stale, re-verify against code)

Each entry can drift; if it conflicts with current code, update or remove the memory rather than acting on it.

- [project_substrate_visual_vocabulary.md](project_substrate_visual_vocabulary.md) — Sim-substrate visual vocabulary is chan→wire + per-node running indicator (with reloop); goroutine and select are not separate visual primitives
- [project_industry_pattern_deferrals.md](project_industry_pattern_deferrals.md) — Visual-editor gaps from the 2026-05-03 industry-pattern review that are deferred until matching friction appears
- [project_local_clocks_beat_global_runner.md](project_local_clocks_beat_global_runner.md) — Per-instance clock locality helped the pause-freeze fix, but recency/surface/problem-shape/written contracts also did. Don't use ease-of-fix as a single-factor substrate signal.
- [feedback_specify_substrate_layer_first.md](feedback_specify_substrate_layer_first.md) — State the substrate-layer answer before/alongside the visible-layer spec; implicit substrate slots get filled with coordinator-shaped defaults from training data
- [feedback_substrate_vs_coordinator_bias.md](feedback_substrate_vs_coordinator_bias.md) — Before fixing substrate code, name the contract violated, not the symptom. Knob-tuning (interval, cap, timeout) is the wrong shape — find the missing local signal. Folds in: substrate cycles must be paced by the visual layer.
- [feedback_visuals_scrutiny.md](feedback_visuals_scrutiny.md) — Visual fixes should use general mechanisms over point patches; expect re-evaluation against later observations
- [feedback_per_emit_simtime_anchoring.md](feedback_per_emit_simtime_anchoring.md) — For emit→pulse animations, anchor each instance at its emit simTime and render concurrently; head-of-queue serial mount is the wrong shape (validated 2026-05-04)
- [feedback_industry_bug_class_scan.md](feedback_industry_bug_class_scan.md) — Before declaring an animation/timing/state/IPC change ready, scan against the well-known bug-class catalog and name the class in the working text
- [feedback_webview_devtools_frame.md](feedback_webview_devtools_frame.md) — VS Code webview devtools default to the outer wrapper frame; prefer file-bridge round-trips over `typeof window.X` for verification
- [feedback_runner_errors_probe_first.md](feedback_runner_errors_probe_first.md) — When the editor hangs/decouples, read `../.probe/runner-errors-last.json` first; one thrown listener often explains compound UI symptoms
- [feedback_cost_overruns.md](feedback_cost_overruns.md) — Catalog of session cost overruns; pattern is speculative tooling on top of an unverified diagnosis
- [feedback_derive_model_from_visual_spec.md](feedback_derive_model_from_visual_spec.md) — David's visual/behavioral specs are sufficient; derive the implied model up front and refuse cheap patches that preserve a wrong model.
- [feedback_enforce_required_inputs.md](feedback_enforce_required_inputs.md) — Missing required wire → editor flag (parseSpec diagnostic, red node) + precondition-gating keeps node inert; substrate does NOT reject the graph (reversed 2026-05-25, commit 0e8d843).
- [feedback_clear_button_armed_only_when_loaded.md](feedback_clear_button_armed_only_when_loaded.md) — Editor affordances clearing a slot must be disabled unless destination `slotPhase === "filled"`; don't lean on substrate-side deferral to fix UX.
- [feedback_uniform_pulse_speed.md](feedback_uniform_pulse_speed.md) — Reject per-wire `speed` props; pulse speed is uniform across all wires.
- [feedback_verify_subagent_commits.md](feedback_verify_subagent_commits.md) — Subagents have picked up unstaged working-tree edits and pushed them; spot-check `git log` deltas before pushing to main.
- [feedback_edge_seed_required_for_rings.md](feedback_edge_seed_required_for_rings.md) — Ring topologies need `data.edgeSeeds: { <inputPort>: <value> }` on the receiving node to break startup deadlock; the Go loader pre-sends it before goroutines start.
- [feedback_audit_invariant_call_sites_first.md](feedback_audit_invariant_call_sites_first.md) — On a primitive-level throw, grep every call site of the violated op first; narrow investigations only after that audit is clean.
- [feedback_discriminated_union_grep.md](feedback_discriminated_union_grep.md) — When adding a new pipeline variant, grep every existing variant name to find hidden allowlists/gates
- [feedback_schema_parser_parity.md](feedback_schema_parser_parity.md) — When extending a spec type, update the schema parser's validator in the same commit, or saves produce unparseable JSON and the editor loads blank
- [feedback_hook_block_means_stop.md](feedback_hook_block_means_stop.md) — When a PreToolUse hook returns exit 2, stop and report to the user; do not route around the block via python3, sed -i, shell redirect, or any other write path.
- [project_v0_cost_calibration.md](project_v0_cost_calibration.md) — Phase 5 v0 cost calibration; mechanical ~10%, hardening ~12%, refactor/exploratory ~15–20% of original estimate
- [project_edge_midpoint_offset_plumbing.md](project_edge_midpoint_offset_plumbing.md) — Edge `midpointOffset` + `setEdgeMidpointOffset` + EdgeActionsCtx already wired end-to-end; don't re-grep schema/adapter/mutation when extending edges
- [project_probe_log_layout.md](project_probe_log_layout.md) — Runtime logs are 4 .probe/ JSONL files (go/ts × log/errors) with ts_ms+src+step envelope; probe-merge.sh merges them
