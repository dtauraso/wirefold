---
branch: task/arcball-rework
---

# Arcball rework

## Goal

Reintroduce arcball camera rotation, removed in merge `bda401e1` (`task/xy-pan-camera`) when the camera was locked square-on. This is a rework, not a verbatim restore of the old empty-space-drag arcball.

## Spec (from user, 2026-05-30)

- **Trigger:** single pointer click-and-drag.
- **Rotation pivot:** the currently-selected node.
- **Pivot fallback:** when no node is selected, rotate around the **world origin** `(0,0,0)`.

## Three distinct interactions — distinct triggers

| Interaction | Event source | Trigger condition | Handler |
|---|---|---|---|
| Pinch-zoom | wheel event | `e.ctrlKey` (`if (e.ctrlKey)`) — trackpad pinch arrives as ctrl+wheel | `onWheelNative` ctrlKey branch |
| xy pan | wheel event | `ctrlKey` false (`else` branch) — two-finger scroll | `onWheelNative` non-ctrl branch |
| Arcball rotation | pointer event | single click-and-drag (pointerdown→pointermove on empty space) | pointer-drag branch (new) |

These three never share a trigger: zoom and pan are wheel-only and split by `ctrlKey`; arcball is pointer-drag-only and never touches the wheel path.

## Hard constraint — do NOT change xy pan

xy pan must behave exactly as today. It lives on a separate input path:

- **xy pan** = two-finger trackpad scroll → `onWheelNative` (wheel events) in `interaction-controls.ts`. **Do not touch.**
- **arcball** = single pointer click-and-drag → the `pointerdown`/`pointermove` branch (the empty-space drag that currently stays inert / "pending"). All arcball work happens here.

## Square-on lock must become conditional

Rotation cannot persist while the camera is force-leveled. Today:

- `scene-content.tsx:~310` — comment: never apply a saved quaternion.
- `scene-content.tsx:~318` — `cam.up.set(0,1,0); cam.lookAt(cam.position.x, cam.position.y, 0)` forces square-on on every load.

This lock must become load-only or conditional so an arcball-rotated camera is not snapped back square-on. The `HomeButton` (`camera-ui.tsx`) re-leveling stays as the explicit way back to square-on.

## Affected files (expected)

- `tools/topology-vscode/src/webview/three/interaction-controls.ts` — pointer-drag branch: implement arcball rotation; `prevX/prevY` in `ControlState` already reserved.
- `tools/topology-vscode/src/webview/three/scene-content.tsx` — relax the square-on lock to load-only/conditional.
- Possibly `geometry-helpers.ts` — pivot/rotation math.

## Open questions / risks

- Interaction with `view-save-on-settle`: rotated camera quaternion should now persist (it is already serialized but actively overridden on load — overriding stops once the lock is conditional).
- `HomeButton` re-level must still work as the deliberate return to square-on.
- Verify pan + zoom still behave identically after the lock change.

## Status

- [x] Square-on lock made conditional (load-only)
- [x] Arcball rotation wired to single pointer drag, pivot = selected node, fallback world origin
- [x] xy pan + pinch-zoom verified unchanged
- [x] Hands-on verified by user

## Implementation notes (landed in working tree, uncommitted)

- `interaction-controls.ts`: empty-space drag past `MOVE_SLOP_PX` enters a `"rotating"` phase. Rotation is an anchored **Shoemake arcball** — at gesture start it snapshots the camera offset/up and basis + screen-ball center/radius; each move maps the down-point and current point onto a virtual sphere (in world space via the start basis) and applies the single quaternion between them to the frozen start offset/up. Path-independent: a circular mouse loop returns to identity (no roll accumulation). Pivot = selected node world pos, else the screen-center point on the z=0 plane (via `unprojectToPlane`). Rotate-pointerup resets to idle without `onSelect`. `commitCamera` per move persists; tilt survives reload via the `CameraRefBridge` quaternion restore.
- `ThreeView.tsx`: `selectedIdRef` mirror threaded into `useInteractionControls`.
- `scene-content.tsx`: `CameraRefBridge` now restores a saved quaternion when present (preserves tilt across reload); falls back to square-on only when none saved. `Camera3D.quaternion` was already a required field + written by `commitCamera`, so the round-trip was already wired.
- `onWheelNative` and `camera-ui.tsx` untouched — xy pan / pinch-zoom unchanged by construction.
