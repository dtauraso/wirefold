---
name: feedback_users_interaction_spec_is_the_model
description: David's plain-language interaction spec already names the exact model; don't pattern-match it onto a textbook algorithm
metadata:
  type: feedback
---

When David describes an interaction in plain language, treat it as a precise model spec, not a vibe to be mapped onto the nearest named algorithm.

For the roll-axis camera, David said up front: "the mouse is a polar coordinate and moving it is like grabbing the radius and moving it around the center... that should look like a roll in whatever view is perpendicular to the disks the radius traces out." That *is* the screen-axis trackball: each frame's rotation axis is perpendicular to the cursor's screen motion, so drag direction (not grab point) sets the axis, and roll IS the accumulated turning from circling the cursor (geometric phase). I instead pattern-matched it onto the textbook Shoemake arcball (axis = p1 × p0), which bakes in the grab location and produces roll from off-center straight drags. Then I burned several fixes (camera-relative sphere, frozen-pose, inside-out flip) debugging symptoms of the wrong model before re-reading his original words.

**Why:** the Shoemake arcball was the "industry-correct" translation of "grab a sphere and rotate it"; David's spec was the actual substance, and they diverge exactly at where roll comes from (grab location vs. path curvature). This is the medium-vs-substance trap from CLAUDE.md applied to interaction control (which is substance — see [[project_interaction_control_is_substance]]).

**How to apply:** before implementing an interaction David described, restate his words as a concrete axis/decomposition rule and check whether the named algorithm I'm reaching for actually matches it. If a "fix" cycle starts chasing artifacts (speed-varies-by-position, inside-out, jitter), suspect the model is wrong, re-read his original phrasing, don't tune knobs. Folds with [[feedback_derive_model_from_visual_spec]] and [[feedback_go_vs_coordinator_bias]].
