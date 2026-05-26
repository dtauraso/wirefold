---
name: feedback_runtime_breadcrumbs_beat_static_analysis
description: For intermittent UI bugs, add cheap runtime breadcrumbs and repro before theorizing; static-analysis subagents chased a wrong load-race theory the breadcrumbs disproved in 3 reloads.
metadata:
  type: feedback
---

For an intermittent "blank 3D diagram on reload" bug, the prior session ran 5 static-analysis subagents that produced a confident but WRONG root cause (an order-dependent `load`/`view-load` message race). The fix came instead from adding lightweight runtime breadcrumbs (lifecycle phases + node/edge counts at store-load and ThreeView render, plus pre-mount window error listeners writing to `.probe/*.jsonl`) and reloading a few times. The logs showed the data path was ALWAYS healthy (`store:load nodes:6` every reload) — so the real cause was elsewhere: `CameraFitter` fit the camera once on `nodes.length` 0→N and never re-fit, so when `view-load` relocated nodes after the initial fit the camera framed stale positions (content "off to the side / blank"). Fixed with a `loadEpoch` counter the store bumps on load/view-load.

**Why:** Static reasoning over a flaky path multiplied unverified assumptions; the breadcrumbs collapsed the search space immediately by proving which layer was healthy. This is the concrete win behind the handoff's standing "evidence, not more inference" warning and the [[feedback_cost_overruns]] pattern (speculative tooling on an unverified diagnosis).

**How to apply:** For intermittent/timing/render bugs, FIRST instrument the suspected path with once-per-event breadcrumbs (avoid per-frame logging to disk) + capture errors that fire before the framework mounts, then repro to localize the failing layer. Only theorize after a repro narrows it. Prefer this over fanning out static-analysis subagents. See also [[feedback_industry_bug_class_scan]] and [[feedback_runner_errors_probe_first]].
