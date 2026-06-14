---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-14 — branch `task/port-ring-anchors` IN FLIGHT, NOT merged — DESIGN COMPLETE, ready to implement)

Context: `main` at `e699cc17`. Branch `task/port-ring-anchors` (off main). This task redesigns node port PHYSICAL LAYOUT. The spec is DONE and all design questions are resolved — only IMPLEMENTATION remains. Docs only on this branch; no code yet.

### The design (FINAL — see docs/planning/visual-editor/port-ring-anchors-spec.html, 6 tabs)
CORE IDEA: replace the existing DIRECTIONAL layout system — `side` (top/bottom/left/right) x `slot`, i.e. four directional buckets each independently sized, plus ad-hoc free `anchor` 3D vectors that override them — with ONE FLAT ARRAY of evenly-spaced ring anchors.
- Each node has a torus; **R = torus major radius (circle traced by the tube center), derived from the node's SIZE per kind (node_dims)** — bigger node => bigger ring => more anchors, so N varies by kind. Circumference C = 2*pi*R.
- **N = floor( C / (d + p) )** evenly-spaced anchor positions = the array `anchors[0..N-1]`. d = port anchor diameter, p = padding; both are TUNABLE CONSTANTS (defaults d=8 wu, p=2 wu), adjusted by eye — NOT a design decision.
- **Per-port field (data model):** each port record swaps its three directional fields (`side`/`slot`/`anchor`) for ONE `anchorId` field = its index into the ring array. e.g. topology/nodes/<id>/outputs/ToNext0.json becomes `{ "name": "ToNext0", "anchorId": 5 }`. There is NO separate name->id map structure — placement is a per-port field, exactly as side/slot were. Named roles are kept; node logic addresses ports BY NAME; anchorId is just placement.
- **Interaction:** editing a port = changing its `anchorId`. TS captures the pointer and unprojects it via the camera (input handling, TS's domain) into a WORLD-SPACE drag target; sends the bridge edit `{ op:update, nodeId, portName, target }` (a position, NOT an anchorId). GO owns the ring geometry AND the port records: it SNAPS target to the nearest anchor index, writes `anchorId` onto the port record, and re-streams geometry. Go produces the id (the snap is geometry = Go-owned); TS never names an id. Same shape as existing node-move (node positions are Go-authoritative). Reuse the existing `update` edit op — no new message kind; parity needed in messages.ts / handle-message.ts / Go stdin_reader.go.

### Resolved questions (all in the spec's Open Questions tab)
- Q1 named roles kept; per-port `anchorId` field. Q2 d/p are tunable constants (not a decision). Q3 R scales with node size (N varies by kind). Q4 N = array length; ports occupy a subset of indices, unused indices empty. Q5 editing reuses `update` op carrying a world-space target; Go snaps + writes anchorId. NONE design-blocking remain.

### Spec commits (this branch)
b8fdabb2 handoff setup; d14fd2ae spec created; 06c1b3f9 Q1; 011f26fc Q3+Q2; e5b4f51f/810e7bab/ab711a00/edd024ce interaction iterations; f475ddbd lead-with-core-idea (one array); da440dfc per-port anchorId field (final data model).

### Code map (where to implement — from a fresh audit)
- Go port-direction compute (AUTHORITATIVE): nodes/Wiring/port_geometry.go:136-216 (portDir) — REPLACE the side/slot/anchor logic with: compute N from R (node size), place anchor i at angle i*2pi/N around the ring; a port's position = anchors[port.anchorId].
- Slot % constants to retire/replace: nodes/Wiring/curve_params.go:52-56 (CurveParamSlotPct0/1/2) — replaced by ring spacing from d/p.
- Go port struct: nodes/Wiring/loader.go:40-45 specPort {Name, Side, Slot *int, Anchor *specVec3} — replace Side/Slot/Anchor with AnchorId int.
- Node dims (R source): nodes/Wiring/node_dims_gen.go (GENERATED from nodes/<Kind>/SPEC.md View; regen: cd tools/topology-vscode && npm run gen:node-defs).
- TS schema: tools/topology-vscode/src/schema/types.ts:22-36 (port side|slot|anchor -> anchorId); parse-nodes-edges.ts:44-48 (slot 0|1|2 validation -> anchorId range check 0..N-1). Per feedback_schema_parser_parity, update schema + parser together.
- TS render fallback: tools/topology-vscode/src/webview/three/geometry-helpers.ts:101-142 (portDirLocal mirrors Go) — update to ring-array. Go streams authoritative geometry (node-geometry trace, node-geometry.ts / useNodeGeometryStore); fallback disabled after first event.
- Bridge: messages.ts (parser) + handle-message.ts (per-op forward) + nodes/Wiring/stdin_reader.go for the `update` op carrying the drag target; Go snaps + writes anchorId.
- Topology data migration: rewrite every topology/nodes/<id>/{inputs,outputs}/*.json — drop side/slot/anchor, add anchorId.

### Suggested implementation order
1. Go geometry: port_geometry.go computes the N-anchor ring from R and positions a port by its anchorId index; loader specPort gets AnchorId. 2. Migrate topology port JSONs (side/slot/anchor -> anchorId). 3. TS schema+parser (anchorId, drop side/slot/anchor). 4. Editor interaction: drag -> world target -> `update` edit -> Go snap -> re-stream. 5. Regenerate node-defs; verify go build + go test -race + tsc + vitest + editor eyeball.

### Carry-forward
- GEOMETRY IS GO-OWNED (MODEL.md/CLAUDE.md). Go computes the ring + snaps; TS only unprojects the pointer (camera/input) and renders Go's stream. TS computes no layout.
- SWARM CAUTION (this session): a background Agent dispatch fanned out into ~20 duplicate agents, duplicated commits, and committed one unrequested change (reverted). Keep subagents FOREGROUND on this branch; spot-check git log for stray commits before pushing.
- port-ring-anchors-spec.html is branch-local (not frontmatter-tagged; HTML — strip-branch-local-docs won't catch it; decide at merge whether it rides to main).

### Next step
Begin implementation at step 1 (Go geometry). No open design questions block it.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
