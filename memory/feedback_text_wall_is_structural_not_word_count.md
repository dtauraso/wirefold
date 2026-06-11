---
name: feedback_text_wall_is_structural_not_word_count
description: A "text wall" is a dense multi-claim run-on block, not just a >40-word block; word-count scans miss them — detect by structure, fix to the git pattern.
metadata:
  type: feedback
---

When the user flags a "text wall" in the spec doc (`docs/go-authoritative-clock/index.html`), do NOT rely on a word-count threshold scan — a >40-word scan returned "0 blocks" yet real walls remained under 40 words. A wall is a dense run-on packing several distinct claims (multiple `;`/`—` separators), typically a `<li>`/`<td>` that does NOT lead with `<strong>`.

**Why:** the doc's concise pattern leads each bullet with `<strong>lead</strong> — tight claim` (file-refs in `--ts-hue`/`--go-hue` mono spans); walls are the inverse, and many sit just under the 40-word line.

**How to apply:** (1) extract the prior fix pattern from git first (`git show` the concision commits) so fixes match house style; (2) sweep by structure/density, not length; (3) discriminate genre — sequential algorithm / `<ol>` loop steps are settled spec and must NOT be reworded into claim-bullets. See [[feedback_finish_calibrated_work]].
