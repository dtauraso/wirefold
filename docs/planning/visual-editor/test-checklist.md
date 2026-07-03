---
branch: task/agnostic-content-buffer
---

# New-system test checklist

The old system is erased; the binary-buffer / Go path is unconditional. Just
**Reload Window** (Developer: Reload Window — reloads the extension host, not just
the webview) and work down the list. Both bridges are now binary: Go→TS content
buffer on fd 3, TS→Go framed binary records on stdin.

Use the `.probe/*.jsonl` logs to diagnose anything that misbehaves before theorizing.

## Rendering (from the content buffer)
- [ ] Nodes render as glassy per-kind spheres with border rings
- [ ] Node **colors** correct per kind (now from the numeric `KindId` column, not a sidecar)
- [ ] Node **label pills** correct (now decoded from the buffer label section, not a sidecar)
- [ ] Transit beads (traveling between nodes) look right in flight
- [ ] Interior beads (inside nodes) render + update
- [ ] Edges: tubes + arrowheads visible and oriented correctly
- [ ] Double-links overlay renders (cyan tubes + arrows) when toggled
- [ ] Missed-bead markers appear on a missed bead
- [ ] Sphere-rings (great-circle tori) render + oriented by the node normals
- [ ] Node status **red pulse** on error/missed
- [ ] Nothing disappears when the camera is **zoomed in close** (frustum-cull fix)

## Camera / interaction
- [ ] Orbit (rotate) — smooth, no lag **during animation** (log-flood fix)
- [ ] Zoom (ctrl-wheel / dolly)
- [ ] Pan (plain wheel) — including with the cursor **over a node or edge** (raw-hit fix)
- [ ] Home button frames the diagram like the pre-branch (unknown-kind radius fix)
- [ ] Viewpoint **persists across Reload** (read/written to `view/scene.json`, file data)
- [ ] Play / pause freezes + resumes the animation

## Selection
- [ ] Two-finger (secondary) tap selects a **node** (yellow ring + orange halo)
- [ ] Nodes **on the selected node's sphere surface** also highlight (on-surface set)
- [ ] Tap selects an **edge** (orange halo) — raw-input edge-hit fix
- [ ] Node vs edge selection is **mutually exclusive** (selecting one clears the other)

## Ports / edge authoring
- [ ] Port spheres render at each node's port directions
- [ ] **Create an edge** by dragging port→port
- [ ] Handhold (grip) orbit works off a port/handhold hit

## Overlays
- [ ] Overlays master toggle (show/hide all)
- [ ] Popover checklist toggles each flag: rings, scene-poles, node-poles, angle-labels,
      sel-sphere-poles, handholds, labels, +N badges, double-links
- [ ] Overlay states survive Reload (Go-owned, streamed in the buffer)

## Bridge (fully binary both ways — no JSON on either wire)
- [ ] All of the above still works with TS→Go as **binary records** (raw-input numeric,
      overlays as flagId/bitfield, save a bare command)
- [ ] **Save** persists the current overlay + camera state to `scene.json` (Go writes its
      own state — the bare `save` command; verify a toggled overlay survives Reload)

## Pending — not yet built (nothing to test until implemented)
- [ ] **Fade** (node/edge dimming, opacity 0.25) — next up (port `computeFade` into Go)
- [ ] **Hover** state (node/port/edge hover highlight)
- [ ] **Node-drag persistence** to disk (Go writes moved positions)
- [ ] **Ring-move persistence** to disk

## Cleanup remaining before merge
- [ ] Full Go **JSON-emitter removal** (once `.probe` decodes from the buffer)
- [ ] Merge `task/agnostic-content-buffer` → `main` on sign-off
