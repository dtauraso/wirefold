# Memory Index

## Background (stable rules — workflow, hygiene, ergonomics)

These rarely change; skim once per session and apply throughout.

- [user_background.md](user_background.md) — User designs concurrent dataflow systems with circuit-style wiring
- [feedback_code_self_defends.md](feedback_code_self_defends.md) — Solid code structure preferred over memory entries for preventing AI drift toward industry defaults
- [feedback_branch_cleanup.md](feedback_branch_cleanup.md) — Delete task branches locally and on remote once merged into main, without re-asking
- [feedback_memory_location.md](feedback_memory_location.md) — Save memory files only to repo `memory/`; skip the local Claude memory dir for this project
- [feedback_bash_cwd_persistence.md](feedback_bash_cwd_persistence.md) — Bash cwd persists across calls; use absolute paths for destructive ops
- [feedback_feature_audit_two_layers.md](feedback_feature_audit_two_layers.md) — Feature-audit removals need both data.js and the hand-authored features/<slug>.html page; rendered page caches data.js
- [feedback_finish_calibrated_work.md](feedback_finish_calibrated_work.md) — Once scope/style are agreed, finish every in-scope item; don't stop one short to ask
- [feedback_text_wall_is_structural_not_word_count.md](feedback_text_wall_is_structural_not_word_count.md) — "Text wall" is structural density, not word count; detect by structure, fix to the git pattern, leave algorithm steps alone
- [feedback_two_process_editor_reload.md](feedback_two_process_editor_reload.md) — Editor is two processes; reopen-file reloads only the webview, Developer-Reload-Window reloads the extension host

## Active (project state — may go stale, re-verify against code)

Each entry can drift; if it conflicts with current code, update or remove the memory rather than acting on it.

- [project_go_visual_vocabulary.md](project_go_visual_vocabulary.md) — Go visual vocabulary is chan→wire + per-node running indicator (with reloop); goroutine and select are not separate visual primitives
- [project_industry_pattern_deferrals.md](project_industry_pattern_deferrals.md) — Visual-editor gaps from the 2026-05-03 industry-pattern review that are deferred until matching friction appears
- [project_local_clocks_beat_global_runner.md](project_local_clocks_beat_global_runner.md) — Per-instance clock locality helped the pause-freeze fix, but recency/surface/problem-shape/written contracts also did. Don't use ease-of-fix as a single-factor Go-layer signal.
- [feedback_specify_go_layer_first.md](feedback_specify_go_layer_first.md) — State the Go-layer answer before/alongside the visible-layer spec; implicit Go-layer slots get filled with coordinator-shaped defaults from training data
- [feedback_go_vs_coordinator_bias.md](feedback_go_vs_coordinator_bias.md) — Before fixing Go code, name the contract violated, not the symptom. Knob-tuning (interval, cap, timeout) is the wrong shape — find the missing local signal. Folds in: Go node cycles must be paced by the visual layer.
- [feedback_visuals_scrutiny.md](feedback_visuals_scrutiny.md) — Visual fixes should use general mechanisms over point patches; expect re-evaluation against later observations
- [feedback_per_emit_simtime_anchoring.md](feedback_per_emit_simtime_anchoring.md) — For emit→pulse animations, anchor each instance at its emit simTime and render concurrently; head-of-queue serial mount is the wrong shape (validated 2026-05-04)
- [feedback_industry_bug_class_scan.md](feedback_industry_bug_class_scan.md) — Before declaring an animation/timing/state/IPC change ready, scan against the well-known bug-class catalog and name the class in the working text
- [feedback_webview_devtools_frame.md](feedback_webview_devtools_frame.md) — VS Code webview devtools default to the outer wrapper frame; prefer file-bridge round-trips over `typeof window.X` for verification
- [feedback_runner_errors_probe_first.md](feedback_runner_errors_probe_first.md) — When the editor hangs/decouples, read `../.probe/runner-errors-last.json` first; one thrown listener often explains compound UI symptoms
- [feedback_cost_overruns.md](feedback_cost_overruns.md) — Catalog of session cost overruns; pattern is speculative tooling on top of an unverified diagnosis
- [feedback_derive_model_from_visual_spec.md](feedback_derive_model_from_visual_spec.md) — David's visual/behavioral specs are sufficient; derive the implied model up front and refuse cheap patches that preserve a wrong model.
- [feedback_enforce_required_inputs.md](feedback_enforce_required_inputs.md) — Editor does NOT flag missing required inputs (red node / parseSpec diagnostic removed 2026-06-01); Go does NOT reject the graph; precondition-gating keeps unfed nodes inert.
- [feedback_clear_button_armed_only_when_loaded.md](feedback_clear_button_armed_only_when_loaded.md) — Editor affordances clearing a slot must be disabled unless destination `slotPhase === "filled"`; don't lean on Go-side deferral to fix UX.
- [feedback_uniform_pulse_speed.md](feedback_uniform_pulse_speed.md) — Reject per-wire `speed` props; pulse speed is uniform across all wires.
- [feedback_verify_subagent_commits.md](feedback_verify_subagent_commits.md) — Subagents have picked up unstaged working-tree edits and pushed them; spot-check `git log` deltas before pushing to main.
- [feedback_edge_seed_required_for_rings.md](feedback_edge_seed_required_for_rings.md) — Ring startup deadlock is broken by a dedicated Input bootstrap node (`data.init=[<seed>]`, `data.repeat=false`) wired via a real edge into the feedback port; edgeSeeds was removed.
- [feedback_audit_invariant_call_sites_first.md](feedback_audit_invariant_call_sites_first.md) — On a primitive-level throw, grep every call site of the violated op first; narrow investigations only after that audit is clean.
- [feedback_discriminated_union_grep.md](feedback_discriminated_union_grep.md) — When adding a new pipeline variant, grep every existing variant name to find hidden allowlists/gates
- [feedback_schema_parser_parity.md](feedback_schema_parser_parity.md) — When extending a spec type, update the schema parser's validator in the same commit, or saves produce unparseable JSON and the editor loads blank
- [feedback_hook_block_means_stop.md](feedback_hook_block_means_stop.md) — When a PreToolUse hook returns exit 2, stop and report to the user; do not route around the block via python3, sed -i, shell redirect, or any other write path.
- [project_v0_cost_calibration.md](project_v0_cost_calibration.md) — Phase 5 v0 cost calibration; mechanical ~10%, hardening ~12%, refactor/exploratory ~15–20% of original estimate
- [project_probe_log_layout.md](project_probe_log_layout.md) — Runtime logs are 4 .probe/ JSONL files (go/ts × log/errors) with ts_ms+src+step envelope; probe-merge.sh merges them
- [project_interaction_control_is_substance.md](project_interaction_control_is_substance.md) — 3D-editor interaction control is substance (not medium); OrbitControls sacrifices control = wrong pattern-match; use the recoverable-by-device test
- [feedback_subagent_discovery_mandate.md](feedback_subagent_discovery_mandate.md) — Give subagents a grep-first discovery mandate, not a curated reading list, or integration points get missed
- [feedback_runtime_breadcrumbs_beat_static_analysis.md](feedback_runtime_breadcrumbs_beat_static_analysis.md) — For intermittent UI bugs, add cheap runtime breadcrumbs + repro before theorizing; static analysis chased a wrong load-race theory the breadcrumbs disproved in 3 reloads
- [feedback_no_manufactured_shortcuts.md](feedback_no_manufactured_shortcuts.md) — Don't offer partial/local-patch variants as decision options when the model already prescribes the full path; subagent typing cost is not a tradeoff axis
- [feedback_invariants_drive_design.md](feedback_invariants_drive_design.md) — Treat user-stated invariants as axioms that drive design and verify framing; simulate frame-by-frame, not just steady-state
- [feedback_dont_invent_doctrine.md](feedback_dont_invent_doctrine.md) — Don't paraphrase a one-off note into a "rule" and cite the paraphrase as project doctrine; grep for the literal phrasing first
- [feedback_tsc_verify_after_removal.md](feedback_tsc_verify_after_removal.md) — After deleting/refactoring webview TS, verify with `tsc --noEmit` too; esbuild (`npm run build`) skips type-checking and lets dangling refs reach runtime
- [feedback_node_model_not_networking_handshake.md](feedback_node_model_not_networking_handshake.md) — Nodes do local work + drive their outputs; no TCP-handshake/ack-nack/send-gating delivery guarantees
- [feedback_paced_tryrecv_blocks.md](feedback_paced_tryrecv_blocks.md) — Paced TryRecv blocks (not a poll); judge from paced_wire.go impl, not the Try/ok idiom
- [feedback_per_goroutine_bridge.md](feedback_per_goroutine_bridge.md) — Go↔TS bridge is per-goroutine (each goroutine sends/picks up); geometry's central emitter is the deviation
- [project_wire_is_straight_line_not_chain.md](project_wire_is_straight_line_not_chain.md) — Wires are straight PacedWire lines; bead-item chain model was built then rejected (O(N²) follow latency); don't re-propose it