---
name: feedback_feature_audit_two_layers
description: Feature-audit removals need both data.js and the hand-authored features/<slug>.html page
metadata:
  type: feedback
---

The feature audit at `docs/planning/visual-editor/feature-audit/` has TWO layers: `data.js` (an array `window.AUDIT_DATA` that drives the index card grid) and a **hand-authored** `features/<slug>.html` detail page per feature. There is **no generator** — the HTML pages are independent static files.

**Why:** removing a feature by editing only `data.js` leaves an orphaned `features/<slug>.html`, and a sibling feature's prose may still name the removed one. A `midpointOffset` removal took three passes because of this.

**How to apply:** to remove a feature, (1) delete its object from `data.js`, (2) `git rm features/<slug>.html`, (3) strip any other entry's `depends_on` and prose references to the slug, (4) grep the whole audit dir to confirm zero refs. Also: `index.html` loads `data.js` via a plain `<script>`, so the rendered page caches it — hard-refresh (Cmd+Shift+R) before concluding it's "still there."
