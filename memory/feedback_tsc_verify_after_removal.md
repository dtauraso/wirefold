---
name: feedback_tsc_verify_after_removal
description: After deleting/refactoring TS in the webview, verify with `tsc --noEmit`, not just `npm run build` — esbuild bundles without type-checking and lets dangling references through to runtime
metadata:
  type: feedback
---

When removing or refactoring TypeScript in the webview (deleting a
variable, field, function, or feature), verify with
`cd tools/topology-vscode && npx tsc --noEmit` in addition to
`npm run build`.

**Why:** `npm run build` uses esbuild, which **bundles without
type-checking**. A dangling reference to a deleted symbol (e.g. a JSX
prop still reading a removed `const`) compiles and bundles cleanly,
then throws a `ReferenceError` at render time and blanks the diagram.
This bit the validation-flag removal (2026-06-01): two
`emissiveIntensity={flagged ? ...}` props survived after `const flagged`
was deleted; `npm run build` passed, the webview crashed with "flagged
is not defined", fixed in commit a319cbf1.

**How to apply:** for any code-removal/refactor task on webview TS, the
verify step is BOTH `tsc --noEmit` (catches undefined refs) AND
`npm run build` (refreshes `out/webview.js`). Neither alone is
sufficient — see [[feedback_subagent_discovery_mandate]] for giving the
subagent a grep-first sweep so no reference is missed in the first place.
