// buffer-log.ts — ext-host buffer-decoded .probe logger.
//
// Decodes one content-buffer snapshot's EVENT block (+ the existing column blocks and the
// label / port-name / edge-label string sections) into `.probe/go.jsonl` lines equivalent
// to the trace-event lines Go's (removed) JSON-on-stdout path produced. This is the concrete
// realization of the spec goal "the .probe log is a DECODE of the same binary content buffer".
//
// The render path ignores the EVENT block; this is the only consumer. Node/port/edge
// identities resolve through row indices → the label / port-name / edge-label sections, so
// no id/port/edge strings are streamed per event. Field VALUES are float32 (the buffer is
// float32 throughout, as the render path already is) — geometry/position coords therefore
// carry float32 precision rather than the old float64 JSON, an inherent, expected nuance.

import { TRACE_EVENT_KINDS } from "./schema/trace-kinds";
import { NODE_KIND_NAMES } from "./schema/node-defs";
import {
  decodeSnapshot,
  nodeLabel,
  portName,
  edgeLabel,
  INTERIOR_SLOTS_PER_NODE,
  type DecodedSnapshot,
} from "./webview/three/buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSphereR,
  readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readNodeKindId, readNodeFaded,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ, readEdgeFaded,
  readPortNodeRow, readPortDX, readPortDY, readPortDZ, readPortIsInput,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
  readOverlaySceneTori, readOverlayScenePoles, readOverlayNodePoles, readOverlayAngleLabels,
  readOverlaySelSpherePoles, readOverlayHandholds, readOverlayLabelsGlobal,
  readOverlayBadgesGlobal, readOverlayOverlaysVis,
  readPortPX, readPortPY, readPortPZ,
  readEventKind, readEventNodeRow, readEventPortRow, readEventTargetRow, readEventTargetPortRow,
  readEventEdgeRow, readEventSlot, readEventValue, readEventBead,
  readEventArcLength, readEventSimLatencyMs, readEventX, readEventY, readEventZ, readEventF,
  UNKNOWN_KIND_ID,
} from "./schema/buffer-layout";

type Line = Record<string, unknown>;

// DecodedEventLine pins the field shape decodeEventLine (below) emits per Go event kind —
// the SAME shape the removed JSON-on-stdout path used to emit. Kept as a typed contract
// so trace-event-fields.test.ts's hand-curated fixture stays pinned to what this decoder
// actually produces; this type has no runtime effect on decodeEventLine itself (which
// returns the looser `Line`).
export type DecodedEventLine =
  | { step: number; kind: "recv" | "fire"; node: string; port?: string; value?: number }
  | { step: number; kind: "send"; node: string; port?: string; value?: number; arcLength?: number; simLatencyMs?: number; target?: string; targetHandle?: string }
  | { step: number; kind: "done"; node: string; port: string }
  | { step: number; kind: "edge-bead"; node: string; port: string; value?: number; x: number; y: number; z: number; f: number; bead?: number }
  | { step: number; kind: "geometry"; edge: string; sx: number; sy: number; sz: number; ex: number; ey: number; ez: number }
  | { step: number; kind: "pulse-cancelled"; node: string; port: string; value?: number; bead?: number }
  | { step: number; kind: "arrive"; node: string; port: string; value?: number; bead?: number }
  | { step: number; kind: "node-geometry"; node: string; label?: string; nodeKind?: string; nx: number; ny: number; nz: number; radius: number; sphereR?: number; vrx: number; vry: number; vrz: number; frx: number; fry: number; frz: number; ports: { name: string; isInput: boolean; px: number; py: number; pz: number; dx: number; dy: number; dz: number }[] }
  | { step: number; kind: "node-bead"; node: string; row: number; col: number; present: boolean; value: number; x: number; y: number; z: number }
  | { step: number; kind: "camera"; px: number; py: number; pz: number; r: number; posTheta: number; posPhi: number; upTheta: number; upPhi: number }
  | { step: number; kind: "scene-tori"; visible: boolean }
  | { step: number; kind: "scene-poles"; visible: boolean }
  | { step: number; kind: "node-poles"; visible: boolean }
  | { step: number; kind: "angle-labels"; visible: boolean }
  | { step: number; kind: "sel-sphere-poles"; visible: boolean }
  | { step: number; kind: "handholds"; visible: boolean }
  | { step: number; kind: "labels-global"; visible: boolean }
  | { step: number; kind: "badges-global"; visible: boolean }
  | { step: number; kind: "overlays-vis"; visible: boolean }
  // Go-owned click-selection: the currently-selected node id (node="" clears it).
  | { step: number; kind: "select"; node: string }
  | { step: number; kind: "fade"; fadedNodes: string[]; fadedEdges: string[] }
  | { step: number; kind: "hover"; node: string; port?: string; value?: number }
  // Go-owned polar rule-builder session state (gesture.go trySelectSphereRule); full-mirror
  // on every state change, like fade above.
  | { step: number; kind: "rule-builder"; ruleCenter: string; ruleHasPending: boolean; rulePendingCode: number; ruleTerms: { node: string; code: number }[] }
  // Go-owned COMMITTED polar-equation lock list (locks.go, md.polarEqs) + the panel's
  // focused row (selectedLockIndex=-1 = none); full-mirror on every mutation (rule
  // completion, toggle, select, delete, load), like rule-builder above.
  | { step: number; kind: "polar-locks"; polarLocks: { center: string; aNode: string; aCode: number; bNode: string; bCode: number; active: boolean }[]; selectedLockIndex: number };

/**
 * Decode a snapshot into `.probe/go.jsonl` lines (one JSON object per line, trailing \n each).
 * Returns "" when the frame is undecodable or carries no events. Each line uses the shared
 * envelope { ts_ms, src:"go", ...fields } — the same envelope the ext host's stdout relay used
 * (minus the `step` ordinal, which the buffer path does not carry).
 */
export function decodeBufferLog(ab: ArrayBuffer): string {
  const d = decodeSnapshot(ab);
  if (!d || d.eventCount === 0) return "";
  const now = Date.now();
  let out = "";
  for (let i = 0; i < d.eventCount; i++) {
    const line = decodeEventLine(d, i);
    if (line) out += JSON.stringify({ ts_ms: now, src: "go", ...line }) + "\n";
  }
  return out;
}

function overlayFlag(d: DecodedSnapshot, kind: string): number {
  const v = d.overlayView;
  switch (kind) {
    case "scene-tori": return readOverlaySceneTori(v);
    case "scene-poles": return readOverlayScenePoles(v);
    case "node-poles": return readOverlayNodePoles(v);
    case "angle-labels": return readOverlayAngleLabels(v);
    case "sel-sphere-poles": return readOverlaySelSpherePoles(v);
    case "handholds": return readOverlayHandholds(v);
    case "labels-global": return readOverlayLabelsGlobal(v);
    case "badges-global": return readOverlayBadgesGlobal(v);
    case "overlays-vis": return readOverlayOverlaysVis(v);
    default: return 0;
  }
}

const OVERLAY_KINDS = new Set([
  "scene-tori", "scene-poles", "node-poles", "angle-labels", "sel-sphere-poles",
  "handholds", "labels-global", "badges-global", "overlays-vis",
]);

function decodeEventLine(d: DecodedSnapshot, i: number): Line | null {
  const ev = d.eventView;
  const kindId = readEventKind(ev, i);
  const kind = TRACE_EVENT_KINDS[kindId];
  if (kind === undefined) return null;
  const nodeRow = readEventNodeRow(ev, i);
  const portRow = readEventPortRow(ev, i);
  const targetRow = readEventTargetRow(ev, i);
  const targetPortRow = readEventTargetPortRow(ev, i);
  const edgeRow = readEventEdgeRow(ev, i);
  const value = readEventValue(ev, i);
  const bead = readEventBead(ev, i);
  const node = nodeRow >= 0 ? nodeLabel(d, nodeRow) : "";
  const port = portName(d, portRow);

  switch (kind) {
    case "recv":
      return { kind, node, port, value };
    case "fire":
      return { kind, node };
    case "done":
      return { kind, node, port };
    case "send": {
      const arc = readEventArcLength(ev, i);
      const lat = readEventSimLatencyMs(ev, i);
      if (arc !== 0 || lat !== 0) {
        const l: Line = { kind, node, port, value, arcLength: arc, simLatencyMs: lat };
        const t = targetRow >= 0 ? nodeLabel(d, targetRow) : "";
        if (t) l.target = t;
        const th = portName(d, targetPortRow);
        if (th) l.targetHandle = th;
        return l;
      }
      return { kind, node, port, value };
    }
    case "edge-bead": {
      const l: Line = { kind, node, port, value, x: readEventX(ev, i), y: readEventY(ev, i), z: readEventZ(ev, i), f: readEventF(ev, i) };
      if (bead !== 0) l.bead = bead;
      return l;
    }
    case "arrive":
    case "pulse-cancelled": {
      const l: Line = { kind, node, port, value };
      if (bead !== 0) l.bead = bead;
      return l;
    }
    case "geometry": {
      const edge = edgeLabel(d, edgeRow);
      return {
        kind, edge,
        sx: readEdgeSX(d.edgeView, edgeRow), sy: readEdgeSY(d.edgeView, edgeRow), sz: readEdgeSZ(d.edgeView, edgeRow),
        ex: readEdgeEX(d.edgeView, edgeRow), ey: readEdgeEY(d.edgeView, edgeRow), ez: readEdgeEZ(d.edgeView, edgeRow),
      };
    }
    case "node-geometry":
      return nodeGeometryLine(d, nodeRow, node);
    case "node-bead": {
      const slot = readEventSlot(ev, i);
      const irow = nodeRow * INTERIOR_SLOTS_PER_NODE + slot;
      return {
        kind, node, row: Math.floor(slot / 2), col: slot % 2,
        present: readInteriorPresent(d.interiorView, irow) === 1,
        value: readInteriorValue(d.interiorView, irow),
        x: readInteriorOX(d.interiorView, irow), y: readInteriorOY(d.interiorView, irow), z: readInteriorOZ(d.interiorView, irow),
      };
    }
    case "camera": {
      const c = d.cameraView;
      return {
        kind,
        px: readCameraPX(c), py: readCameraPY(c), pz: readCameraPZ(c), r: readCameraR(c),
        posTheta: readCameraPosTheta(c), posPhi: readCameraPosPhi(c),
        upTheta: readCameraUpTheta(c), upPhi: readCameraUpPhi(c),
      };
    }
    case "select":
      // stdout marshals select via the default {node,port,value} shape (edge label not emitted).
      return { kind, node, port: "", value };
    case "hover":
      return { kind, node, port, value };
    case "fade": {
      // Fade line lists the DIRECTLY-faded seed sets. The buffer carries the fixpoint Faded
      // columns (nodes/edges dimmed), which for a single fade toggle equals the seed set here;
      // reconstruct fadedNodes/fadedEdges from the faded columns.
      const nodes: string[] = [];
      for (let r = 0; r < d.nodeCount; r++) {
        if (readNodeFaded(d.nodeView, r) === 1) nodes.push(nodeLabel(d, r));
      }
      const edges: string[] = [];
      for (let r = 0; r < d.edgeCount; r++) {
        if (readEdgeFaded(d.edgeView, r) === 1) edges.push(edgeLabel(d, r));
      }
      return { kind, fadedNodes: nodes, fadedEdges: edges };
    }
    default:
      if (OVERLAY_KINDS.has(kind)) return { kind, visible: overlayFlag(d, kind) === 1 };
      return { kind, node, port, value };
  }
}

function nodeGeometryLine(d: DecodedSnapshot, nodeRow: number, node: string): Line {
  const n = d.nodeView;
  const cx = readNodeCX(n, nodeRow), cy = readNodeCY(n, nodeRow), cz = readNodeCZ(n, nodeRow);
  const radius = readNodeRadius(n, nodeRow);
  const sphereR = readNodeSphereR(n, nodeRow);
  const kindId = readNodeKindId(n, nodeRow);
  const ports: Line[] = [];
  for (let pr = 0; pr < d.portCount; pr++) {
    if (readPortNodeRow(d.portView, pr) !== nodeRow) continue;
    const dx = readPortDX(d.portView, pr), dy = readPortDY(d.portView, pr), dz = readPortDZ(d.portView, pr);
    ports.push({
      name: portName(d, pr),
      isInput: readPortIsInput(d.portView, pr) === 1,
      px: readPortPX(d.portView, pr), py: readPortPY(d.portView, pr), pz: readPortPZ(d.portView, pr),
      dx, dy, dz,
    });
  }
  const l: Line = { kind: "node-geometry", node };
  if (node) l.label = node;
  if (kindId !== UNKNOWN_KIND_ID && NODE_KIND_NAMES[kindId] !== undefined) l.nodeKind = NODE_KIND_NAMES[kindId];
  l.nx = cx; l.ny = cy; l.nz = cz; l.radius = radius;
  if (sphereR !== 0) l.sphereR = sphereR;
  l.vrx = readNodeVRX(n, nodeRow); l.vry = readNodeVRY(n, nodeRow); l.vrz = readNodeVRZ(n, nodeRow);
  l.frx = readNodeFRX(n, nodeRow); l.fry = readNodeFRY(n, nodeRow); l.frz = readNodeFRZ(n, nodeRow);
  l.ports = ports;
  return l;
}
