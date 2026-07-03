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
  readOverlayBadgesGlobal, readOverlayOverlaysVis, readOverlayDoubleLinks,
  readEventKind, readEventNodeRow, readEventPortRow, readEventTargetRow, readEventTargetPortRow,
  readEventEdgeRow, readEventSlot, readEventValue, readEventBead,
  readEventArcLength, readEventSimLatencyMs, readEventX, readEventY, readEventZ, readEventF,
} from "./schema/buffer-layout";

/** KindId sentinel for an unknown node kind (matches KindIDUnknown in Buffer/node_kind_id_gen.go). */
const KIND_ID_UNKNOWN = 0xff;

type Line = Record<string, unknown>;

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
    case "double-links": return readOverlayDoubleLinks(v);
    default: return 0;
  }
}

const OVERLAY_KINDS = new Set([
  "scene-tori", "scene-poles", "node-poles", "angle-labels", "sel-sphere-poles",
  "handholds", "labels-global", "badges-global", "overlays-vis", "double-links",
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
      px: cx + dx * radius, py: cy + dy * radius, pz: cz + dz * radius,
      dx, dy, dz,
    });
  }
  const l: Line = { kind: "node-geometry", node };
  if (node) l.label = node;
  if (kindId !== KIND_ID_UNKNOWN && NODE_KIND_NAMES[kindId] !== undefined) l.nodeKind = NODE_KIND_NAMES[kindId];
  l.nx = cx; l.ny = cy; l.nz = cz; l.radius = radius;
  if (sphereR !== 0) l.sphereR = sphereR;
  l.vrx = readNodeVRX(n, nodeRow); l.vry = readNodeVRY(n, nodeRow); l.vrz = readNodeVRZ(n, nodeRow);
  l.frx = readNodeFRX(n, nodeRow); l.fry = readNodeFRY(n, nodeRow); l.frz = readNodeFRZ(n, nodeRow);
  l.ports = ports;
  return l;
}
