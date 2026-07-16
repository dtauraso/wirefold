---
name: project-theta-phi-tilted-camera
description: Apparent θ (height) mismatch in the editor is usually a φ (longitude) difference seen through a tilted camera; measure θ from world +y, not the screen. Plus the layout pole singularity.
metadata:
  type: project
---

**θ vs φ through a tilted camera.** What looks like a θ (height/latitude) difference
between two nodes in the editor is often a φ (azimuth/longitude) difference projected by
a rotated camera — screen-up ≠ world +y. Measure θ from world **+y**, not from the
screen, before claiming a θ bug. The polar frame markers (NavGuides.tsx: +y green / +x
red / +z blue, camera-independent) exist to make the world frame visible for exactly this.

Validated 2026-06: the reported "persistent θ mismatch between nodes 3 and 7" was NOT a θ
bug — 3 & 7 share θ exactly (θ-lock holds, `pair_theta` d=0.0000) and differ only in φ
(~171° apart); the mismatch was the φ separation seen through a tilted camera.

**Layout pole singularity (related, still open — see branch `task/layout-pole-singularity`).**
The (θ,φ) chart is singular at the +y pole in the LAYOUT: `surfaceCoord`→`cart2polar`
snaps φ to 0 on the axis, and the θ-lock keeps each node's φ, so near the pole φ blows up
and positions get unstable. The CAMERA math (`spherical.go`) already avoids this via the
epsilon-free great-circle bearing form (`atan2` of two unnormalized terms — no `/sinθ`).
If the θ-lock/layout needs a pole fix, mirror that bearing-form approach. Relates to
[[feedback_make_bug_class_unrepresentable]] (pick the formulation with no pole special-case).
