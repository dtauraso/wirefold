---
name: feedback_reflect_dont_create_store
description: "Don't use a store" (David) means don't create TS state-authority for a streamed bit; TS may only REFLECT Go-owned values read-only. Now enforced in code by check-no-webview-state — reflect the buffer via useSyncExternalStore, not a store.
metadata:
  type: feedback
---

When David says "don't use a store," he means: do **not** create a TS-side
state-authority/conduit for a streamed value. TS may only REFLECT Go's last-streamed
value, read-only.

**Why:** the model keeps all authority in Go (see [[project_go_visual_vocabulary]] and
MODEL.md "Editor surface"). A TS store quietly makes TS an authority and is drift.

**Current mechanism (post agnostic-content-buffer refactor):** there are NO webview stores
anymore — the erase deleted them and `tools/check-no-webview-state.sh` now GUARDS against
reintroducing a Zustand `create(` / stateful domain hook (code beats memory, see
[[feedback_code_self_defends]]). TS reflects Go-owned state by decoding the binary content
buffer: `overlay-flags.ts` reads the buffer's Overlay block via React `useSyncExternalStore`
subscribed to snapshot arrivals (the row-keyed reflect resources `snapshot-buffer.ts` /
`overlay-flags.ts` / `buffer-nav.ts` mirror Go and author nothing). The old reference
pattern — "scene-tori reflected in the existing camera store" — is DEAD; the camera store,
like all render stores, was erased. The overlay toggle now flows: TS binary `edit
update overlays attr=toggle` → Go flips `md.ov` + streams it in the Overlay block → the UI
reflects it via `useSyncExternalStore`. Never make TS the source of truth.
