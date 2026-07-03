---
name: feedback_runner_errors_probe_first
description: When the editor hangs, decouples, or shows compound UI symptoms, read the .probe ERROR logs (go-errors.jsonl / ts-errors.jsonl) before forming hypotheses — one thrown listener or Go error often explains several independent-looking symptoms.
metadata:
  type: feedback
---

When a symptom is some combination of "play button stuck", "animation frozen", "interaction decoupled", "X broken since I touched Y", or any compound/unexplained editor misbehavior — read the `.probe` ERROR logs FIRST, before hypotheses or asking for a console stack.

The `.probe/` set is four JSONL files (verify against [[project_probe_log_layout]]):
- `.probe/go-errors.jsonl` — the Go process's STDERR (panics, persister write failures, etc.).
- `.probe/ts-errors.jsonl` — webview errors, including ones that fire before React mounts.
- `.probe/go.jsonl` / `.probe/ts.jsonl` — normal event/breadcrumb streams (go.jsonl now DECODES from the buffer EVENT block; there is no JSON-trace stdout source anymore).

**Why:** one upstream throw can produce three independent-looking UI symptoms — a thrown listener aborts a loop and leaves downstream state half-updated. The error log is the authoritative trace; symptom-side code is usually a wild goose chase when the real cause is one throw. (Original 2026-05-04 case was a JS-runner-era listener throw; that runner is gone now that Go owns the clock, but the "read the error probe first" discipline is unchanged.)

**How to apply:** `cat .probe/go-errors.jsonl` and `.probe/ts-errors.jsonl` first — a stack/error there is almost certainly the root cause. Only then look at symptom-side code. See also [[feedback_runtime_breadcrumbs_beat_static_analysis]] (add a breadcrumb if the error logs are empty but behavior is wrong) and [[feedback_headless_repro_verifies_persistence]] (for persistence/bridge bugs, drive the binary and read the files). NOTE: Go-side ad-hoc debug output now goes through the Go breadcrumb channel to `.probe` (not stdout) — reach for it instead of scattering `Fprintf(os.Stderr)`.
