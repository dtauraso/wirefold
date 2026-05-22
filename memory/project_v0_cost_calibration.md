---
name: project-v0-cost-calibration
description: Post-Phase-5 cost recalibration ratios for sizing future work
metadata:
  type: project
---

# v0 Cost Calibration

Phase 5 came in at ~$5.65 against a $110 estimate (~5% of midpoint, ~18× overestimate).
Cap-hit estimation retired post-Phase 5; the unit was calibrated against an older model
and a less mature codebase.

## Risk band ratios (post-Phase-5 recalibration)

| Phase type              | Ratio of original estimate |
|-------------------------|---------------------------|
| Mechanical              | ~10%                      |
| Hardening               | ~12%                      |
| Refactor / exploratory  | ~15–20%                   |

Mechanical phases run roughly an order of magnitude under original budget with
Opus 4.7 + existing harness/adapter/save infrastructure.

Hardening phases have more codebase exploration than pure-function authoring,
landing slightly above mechanical.

Refactor and exploratory phases carry wider risk bands: the Phase-5 efficiency
factor may not generalize fully to less-scoped work.

## Usage

Apply these ratios when sizing new work. Cap-hit column is no longer load-bearing.
