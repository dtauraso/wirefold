---
name: feedback_guards_hardcoding_single_file_break_on_split
description: Guards that hardcode a single file path go blind or break when that file is split; scan the dir instead, and run the full guard suite on merged main
metadata:
  type: feedback
---

When splitting a god-file, any Stop-hook guard that hardcodes scanning that one
file silently breaks or goes blind. Seen 2026-06-20 splitting `scene-content.tsx`:
`check-ts-shading-from-go.sh` hardcoded `SCENE_FILE=scene-content.tsx`, so after
the split its positive assertions FAILED (shading code moved to `scene-graph.tsx`)
and its forbidden-literal scan went blind to the new files. `check-polar-only-nav.sh`
had the same single-file shape and was caught/extended during the split itself.

**Why:** guards encode "this invariant holds in this file," but the invariant is
really about a *subsystem* (the `three/` render dir), not a file. File boundaries
move; subsystem boundaries don't.

**How to apply:** (1) write guards to scan the relevant DIR
(`grep -rnE --include='*.ts' --include='*.tsx' "$pat" "$SCAN_DIR"`), like
`check-no-camera-roundtrip.sh` already does, not a single file path. (2) When a
refactor splits a file, grep the guard scripts for that filename and update them
in the SAME commit. (3) The backstop that caught this: run the FULL guard suite on
merged `main` after a multi-branch merge — the individual branches were green, but
the merged interaction (split + a guard from another branch) surfaced the break.
Links: [[feedback_code_self_defends]], [[feedback_verify_subagent_commits]].
