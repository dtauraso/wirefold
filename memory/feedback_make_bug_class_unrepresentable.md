---
name: feedback_make_bug_class_unrepresentable
description: For interaction/geometry math, pick the formulation with no free axis/sign knobs so the wrong-axis bug class is unrepresentable, not just currently-correct
metadata:
  type: feedback
---

When implementing rotation/interaction geometry, choose the formulation that has the fewest free parameters, so whole classes of bugs can't be expressed — don't ship a version that merely happens to be tuned right.

The roll-axis camera churned through ~7 rotation models (v6–v12). The screen-axis and polar-screen versions each had tunable axis assignments: which screen component maps to which rotation axis, a sign per axis, roll-vs-tumble as separate code paths, plus a global direction flip. Every one of those knobs was a place to be wrong, and David hit them all in sequence (wrong direction, tilt-instead-of-roll, inside-out). His verdict: "these issues should not be possible to make."

The fix wasn't a better-tuned knob set — it was the great-circle sphere model, which has NO axis-assignment knobs: map the cursor to a point on a true sphere, rotate so p_prev → p_cur, axis = p_prev × p_cur. The sphere geometry fully determines the axis; roll and tumble are the same single rotation seen at different surface locations, not separate paths that can disagree. Only one binary remains (global follow-direction = one invert()).

**Why:** a formulation rich enough to express the bug will eventually be set to the bug, especially across many edit cycles by a model that mis-derives signs. Removing the degrees of freedom removes the bug class. This is [[feedback_code_self_defends]] applied to math: structure prevents the error instead of vigilance.

**How to apply:** before coding interaction/geometry, count the free parameters (axis pickers, per-component signs, separate-but-must-agree paths). If the canonical single-rule form exists (here: the sphere itself defines the axis), use it even if a decomposed form looks more tunable. Folds with [[feedback_users_interaction_spec_is_the_model]] — David's spec named this exact rule ("axis perpendicular to the disk r sweeps").
