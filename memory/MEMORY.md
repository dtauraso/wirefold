# Memory Index

## Background (stable rules — workflow, hygiene, ergonomics)

These rarely change; skim once per session and apply throughout.

- [user_background.md](user_background.md) — User designs concurrent dataflow systems with circuit-style wiring
- [feedback_code_self_defends.md](feedback_code_self_defends.md) — Solid code structure preferred over memory entries for preventing AI drift toward industry defaults
- [feedback_branch_cleanup.md](feedback_branch_cleanup.md) — Delete task branches locally and on remote once merged into main, without re-asking
- [feedback_memory_location.md](feedback_memory_location.md) — Save memory files only to repo `memory/`; skip the local Claude memory dir for this project
- [feedback_check_the_signal_the_check_emits.md](feedback_check_the_signal_the_check_emits.md) — Self-derived: before claiming "verified", make the check fail once; a check that can't fail reads like a passing one. (The stop-checks exit-code fact itself lives in CLAUDE.md's verify recipe.)
- [feedback_bash_cwd_persistence.md](feedback_bash_cwd_persistence.md) — Bash cwd persists across calls; use absolute paths for destructive ops
- [feedback_feature_audit_two_layers.md](feedback_feature_audit_two_layers.md) — Feature-audit removals need both data.js and the hand-authored features/<slug>.html page; rendered page caches data.js
- [feedback_finish_calibrated_work.md](feedback_finish_calibrated_work.md) — Once scope/style are agreed, finish every in-scope item; don't stop one short to ask
- [feedback_no_deferrals.md](feedback_no_deferrals.md) — "Fix all 100%" means every finding incl. low/off-hot-path; never defer or give subagents defer-if-risky latitude
- [feedback_text_wall_is_structural_not_word_count.md](feedback_text_wall_is_structural_not_word_count.md) — "Text wall" is structural density, not word count; detect by structure, fix to the git pattern, leave algorithm steps alone
- [feedback_two_process_editor_reload.md](feedback_two_process_editor_reload.md) — Editor is two processes; reopen-file reloads only the webview, Developer-Reload-Window reloads the extension host
- [feedback_no_nested_agents.md](feedback_no_nested_agents.md) — Delegate edits to the project `implementer` agent (no Agent tool), not `general-purpose`, so subagents can't recursively spawn nested agents

## Active (project state — may go stale, re-verify against code)

Each entry can drift; if it conflicts with current code, update or remove the memory rather than acting on it.

- [project_node_color_vocab.md](project_node_color_vocab.md) — David's node-kind nicknames: "time nodes" = HoldNewSendOld, "and nodes" = WindowAndInhibit*Gate
- [project_two_goroutine_node_split.md](project_two_goroutine_node_split.md) — STALE: described LayoutPort.run, since removed; node-move is now decentralized nodeMover goroutines (see project_lock_propagation_decentralized.md); LayoutHolder.UpdateLayout is a vestigial no-op

- [project_go_visual_vocabulary.md](project_go_visual_vocabulary.md) — Go visual vocabulary is chan→wire + per-node running indicator (with reloop); goroutine and select are not separate visual primitives
- [feedback_ease_of_fix_is_confounded.md](feedback_ease_of_fix_is_confounded.md) — "This fix was easy/hard" is not standalone evidence an architecture change is paying off; recency and surface area always flatter the newest code.
- [feedback_specify_go_layer_first.md](feedback_specify_go_layer_first.md) — State the Go-layer answer before/alongside the visible-layer spec; implicit Go-layer slots get filled with coordinator-shaped defaults from training data
- [feedback_go_vs_coordinator_bias.md](feedback_go_vs_coordinator_bias.md) — Before fixing Go code, name the contract violated, not the symptom. Knob-tuning (interval, cap, timeout) is the wrong shape — find the missing local signal.
- [feedback_abc_times_constant_not_rederive.md](feedback_abc_times_constant_not_rederive.md) — Update polar positions as abc-index × step-constant (arithmetic) + fixed pole increments; only trig is the cartesian↔polar boundary. Demo: docs/demos/polar-drag-3d.html.
- [feedback_visuals_scrutiny.md](feedback_visuals_scrutiny.md) — Visual fixes should use general mechanisms over point patches; expect re-evaluation against later observations
- [feedback_per_emit_simtime_anchoring.md](feedback_per_emit_simtime_anchoring.md) — For emit→pulse animations, anchor each instance at its emit simTime and render concurrently; head-of-queue serial mount is the wrong shape (validated 2026-05-04)
- [feedback_industry_bug_class_scan.md](feedback_industry_bug_class_scan.md) — Before declaring an animation/timing/state/IPC change ready, scan against the well-known bug-class catalog and name the class in the working text
- [feedback_webview_devtools_frame.md](feedback_webview_devtools_frame.md) — VS Code webview devtools default to the outer wrapper frame; prefer file-bridge round-trips over `typeof window.X` for verification
- [feedback_runner_errors_probe_first.md](feedback_runner_errors_probe_first.md) — On editor hang/decouple/compound symptoms, read the `.probe` ERROR logs (go-errors.jsonl / ts-errors.jsonl) first; one throw often explains several symptoms (updated post-refactor: JS-runner era gone, Go owns the clock)
- [feedback_cost_overruns.md](feedback_cost_overruns.md) — Catalog of session cost overruns; pattern is speculative tooling on top of an unverified diagnosis
- [feedback_derive_model_from_visual_spec.md](feedback_derive_model_from_visual_spec.md) — David's visual/behavioral specs are sufficient; derive the implied model up front and refuse cheap patches that preserve a wrong model.
- [feedback_enforce_required_inputs.md](feedback_enforce_required_inputs.md) — Editor does NOT flag missing required inputs (red node / parseSpec diagnostic removed 2026-06-01); Go does NOT reject the graph; precondition-gating keeps unfed nodes inert.
- [feedback_clear_button_armed_only_when_loaded.md](feedback_clear_button_armed_only_when_loaded.md) — An affordance must be disabled when its state can't accept the action, never silently bank it for later; don't lean on Go-side deferral to fix a UI contract.
- [feedback_uniform_pulse_speed.md](feedback_uniform_pulse_speed.md) — Reject per-wire `speed` props; pulse speed is uniform across all wires.
- [feedback_verify_subagent_commits.md](feedback_verify_subagent_commits.md) — Subagents have picked up unstaged working-tree edits and pushed them; spot-check `git log` deltas before pushing to main.
- [feedback_edge_seed_required_for_rings.md](feedback_edge_seed_required_for_rings.md) — Ring startup deadlock is broken by a dedicated Input bootstrap node (`data.init=[<seed>]`, `data.repeat=false`) wired via a real edge into the feedback port; edgeSeeds was removed.
- [feedback_audit_invariant_call_sites_first.md](feedback_audit_invariant_call_sites_first.md) — On a primitive-level throw, grep every call site of the violated op first; narrow investigations only after that audit is clean.
- [feedback_discriminated_union_grep.md](feedback_discriminated_union_grep.md) — When adding a new pipeline variant, grep every existing variant name to find hidden allowlists/gates
- [feedback_schema_parser_parity.md](feedback_schema_parser_parity.md) — When extending a spec type, update the schema parser's validator in the same commit, or saves produce unparseable JSON and the editor loads blank
- [feedback_hook_block_means_stop.md](feedback_hook_block_means_stop.md) — When a PreToolUse hook returns exit 2, stop and report to the user; do not route around the block via python3, sed -i, shell redirect, or any other write path.
- [project_v0_cost_calibration.md](project_v0_cost_calibration.md) — Phase 5 v0 cost calibration; mechanical ~10%, hardening ~12%, refactor/exploratory ~15–20% of original estimate
- [project_lock_persistence_survives_respawn.md](project_lock_persistence_survives_respawn.md) — Polar node-node locks enforce only in mover memory; each follower must self-persist or run/respawn reloads stale positions
- [project_probe_log_layout.md](project_probe_log_layout.md) — Runtime logs are per-owner .probe/ JSONL files (go.jsonl=view bucket + go-node/go-edge/go-interior.jsonl, plus ts/errors + go-debug.jsonl breadcrumbs); events decode from each per-owner stream frame's trailing EVENTS section (fd3 EVENT block retired in c5347078); probe-merge.sh merges them at READ time only
- [feedback_no_single_writer_bridge.md](feedback_no_single_writer_bridge.md) — Go→TS bridge must not funnel through 1 pipe or 1 serialized writer; 1 goroutine = 1 binary channel over inherited stdio fds (no socket/server)
- [project_interaction_control_is_substance.md](project_interaction_control_is_substance.md) — 3D-editor interaction control is substance (not medium); OrbitControls sacrifices control = wrong pattern-match; use the recoverable-by-device test
- [feedback_subagent_discovery_mandate.md](feedback_subagent_discovery_mandate.md) — Give subagents a grep-first discovery mandate, not a curated reading list, or integration points get missed
- [feedback_parallel_subagent_worktrees_can_trample.md](feedback_parallel_subagent_worktrees_can_trample.md) — Parallel implementer subagents may run in worktrees and copy back over a concurrently-active branch; treat the worktree as canonical, reset main tree, re-apply onto the intended branch
- [feedback_runtime_breadcrumbs_beat_static_analysis.md](feedback_runtime_breadcrumbs_beat_static_analysis.md) — For intermittent UI bugs, add cheap runtime breadcrumbs + repro before theorizing; static analysis chased a wrong load-race theory the breadcrumbs disproved in 3 reloads
- [feedback_headless_repro_verifies_persistence.md](feedback_headless_repro_verifies_persistence.md) — Green unit tests hid live persistence/bridge failures 3×; verify by driving the real binary headlessly and reading on-disk/on-wire bytes, and make tests use the runtime's input form (file-vs-dir, empty-vs-populated)
- [feedback_no_manufactured_shortcuts.md](feedback_no_manufactured_shortcuts.md) — Don't offer partial/local-patch variants as decision options when the model already prescribes the full path; subagent typing cost is not a tradeoff axis
- [feedback_invariants_drive_design.md](feedback_invariants_drive_design.md) — Treat user-stated invariants as axioms that drive design and verify framing; simulate frame-by-frame, not just steady-state
- [feedback_dont_invent_doctrine.md](feedback_dont_invent_doctrine.md) — Don't paraphrase a one-off note into a "rule" and cite the paraphrase as project doctrine; grep for the literal phrasing first
- [feedback_tsc_verify_after_removal.md](feedback_tsc_verify_after_removal.md) — After deleting/refactoring webview TS, verify with `tsc --noEmit` too; esbuild (`npm run build`) skips type-checking and lets dangling refs reach runtime
- [feedback_node_model_not_networking_handshake.md](feedback_node_model_not_networking_handshake.md) — Nodes do local work + drive their outputs; no TCP-handshake/ack-nack/send-gating delivery guarantees
- [feedback_paced_tryrecv_blocks.md](feedback_paced_tryrecv_blocks.md) — In.TryRecv/PacedWire.Recv deleted as dead code (2026-07); live non-blocking receive is In.PollRecv/PacedWire.PollRecv
- [feedback_per_goroutine_bridge.md](feedback_per_goroutine_bridge.md) — David's bridge invariant: each goroutine sends to TS, TS sends what the goroutine picks up. The old "geometry is the deviation" complaint is resolved — don't act on it.
- [project_wire_is_straight_line_not_chain.md](project_wire_is_straight_line_not_chain.md) — Wires are straight PacedWire lines; bead-item chain model was built then rejected (O(N²) follow latency); don't re-propose it
- [feedback_place_all_then_drive_concurrently.md](feedback_place_all_then_drive_concurrently.md) — Place all outbound beads before driving concurrently (DriveAll); serial per-edge drive causes fan-out timing regression (node-2 lesson, 2026-06-14)
- [feedback_no_foreground_sim_runs.md](feedback_no_foreground_sim_runs.md) — Never run the sim in the foreground; it can fail to exit and hang the call. Background it or wrap in tools/run-bounded.sh, keep run-and-grep in the main session
- [project_layout_model_evolution.md](project_layout_model_evolution.md) — Layout models tried and why each was rejected (plane/lattice/rooted/relax/dials/coordinate plans) → polar model; don't re-tread the dead ends
- [project_gopls_stale_after_external_edits.md](project_gopls_stale_after_external_edits.md) — Editor diagnostics are often PHANTOM after a file split or field rename (gopls holds the old copy of a shrunken file); `go build` is authoritative, never "fix" a diagnostic without checking it
- [project_rootmove_is_per_pointer_move.md](project_rootmove_is_per_pointer_move.md) — RootMove runs per pointer-move event, NOT once per drag; per-drag state belongs at the FSM drag-start edge (two bugs from this trap)
- [project_lock_propagation_decentralized.md](project_lock_propagation_decentralized.md) — Node-move propagation is decentralized (node writes only itself, no worklist); rule/gate/anchor equalize/trigger cascade DELETED 2026-07-18, replaced by one-hop neighbor edge re-quantize (neighborSetC)
- [feedback_users_interaction_spec_is_the_model.md](feedback_users_interaction_spec_is_the_model.md) — David's plain-language interaction spec already names the exact model; don't pattern-match it onto a textbook algorithm (arcball detour cost several fixes)
- [feedback_make_bug_class_unrepresentable.md](feedback_make_bug_class_unrepresentable.md) — For interaction/geometry math, pick the formulation with no free axis/sign knobs so the wrong-axis bug class can't be expressed (great-circle sphere vs decomposed trackball)
- [feedback_guards_hardcoding_single_file_break_on_split.md](feedback_guards_hardcoding_single_file_break_on_split.md) — Single-file-path guards break/blind on a file split; scan the dir, grep guards for the filename when splitting, and run the full suite on merged main
- [feedback_reflect_dont_create_store.md](feedback_reflect_dont_create_store.md) — "Don't use a store" = no TS state-authority for a streamed bit; now GUARDED by check-no-webview-state — reflect the buffer read-only via useSyncExternalStore (overlay-flags.ts), no stores exist
- [project_theta_phi_tilted_camera.md](project_theta_phi_tilted_camera.md) — Apparent θ mismatch is usually φ seen through a tilted camera; measure θ from world +y. Includes the open layout pole singularity (φ blow-up; mirror spherical.go bearing form)
- [project_torus_colinearity_future_equation.md](project_torus_colinearity_future_equation.md) — port∈torus is now polar-only (no node-center z-drag; ended the (3,r)=(6,r) blow-up); port/edge/torus colinearity to be rebuilt as a NEW polar equation, never cartesian z
