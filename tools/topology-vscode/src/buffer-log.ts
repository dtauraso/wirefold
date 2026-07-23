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
  decodeNodeFrame,
  decodeEdgeFrame,
  decodeViewFrame,
  nodeLabel,
  portName,
  edgeLabel,
  INTERIOR_SLOTS_PER_NODE,
  type DecodedNodeFrame,
  type DecodedEdgeFrame,
} from "./webview/three/buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSphereR,
  readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readNodeKindId,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSrcPortRow, readEdgeDstPortRow,
  readPortNodeRow, readPortDX, readPortDY, readPortDZ, readPortIsInput,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
  readOverlaySceneTori, readOverlayScenePoles, readOverlayNodePoles,
  readOverlaySelSpherePoles, readOverlayHandholds, readOverlayLabelsGlobal,
  readOverlayOverlaysVis, readOverlayDoubleLinks,
  readPortPX, readPortPY, readPortPZ,
  readEventKind, readEventNodeRow, readEventPortRow, readEventTargetRow, readEventTargetPortRow,
  readEventEdgeRow, readEventSlot, readEventValue, readEventBead,
  readEventArcLength, readEventSimLatencyMs, readEventX, readEventY, readEventZ, readEventF,
  readSceneCX, readSceneCY, readSceneCZ, readSceneRadius,
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
  | { step: number; kind: "edge-bead"; node: string; port: string; value?: number; x: number; y: number; z: number; f: number; bead?: number }
  | { step: number; kind: "geometry"; edge: string; sx: number; sy: number; sz: number; ex: number; ey: number; ez: number }
  | { step: number; kind: "arrive"; node: string; port: string; value?: number; bead?: number }
  | { step: number; kind: "node-geometry"; node: string; label?: string; nodeKind?: string; nx: number; ny: number; nz: number; radius: number; sphereR?: number; vrx: number; vry: number; vrz: number; frx: number; fry: number; frz: number; ports: { name: string; isInput: boolean; px: number; py: number; pz: number; dx: number; dy: number; dz: number }[] }
  | { step: number; kind: "node-bead"; node: string; row: number; col: number; present: boolean; value: number; x: number; y: number; z: number }
  | { step: number; kind: "camera"; px: number; py: number; pz: number; r: number; posTheta: number; posPhi: number; upTheta: number; upPhi: number }
  | { step: number; kind: "scene-sphere"; cx: number; cy: number; cz: number; radius: number }
  | { step: number; kind: "scene-tori"; visible: boolean }
  | { step: number; kind: "scene-poles"; visible: boolean }
  | { step: number; kind: "node-poles"; visible: boolean }
  | { step: number; kind: "sel-sphere-poles"; visible: boolean }
  | { step: number; kind: "handholds"; visible: boolean }
  | { step: number; kind: "labels-global"; visible: boolean }
  | { step: number; kind: "overlays-vis"; visible: boolean }
  | { step: number; kind: "double-links"; visible: boolean }
  // Layout-link pair, from LocalPolars — NOT the Edge block (see Buffer/layout.go LayoutLink).
  | { step: number; kind: "layout-link"; node: string; target: string }
  // Go-owned click-selection: the currently-selected node id (node="" clears it).
  | { step: number; kind: "select"; node: string }
  | { step: number; kind: "hover"; node: string; port?: string; value?: number }
  // abc-drag: no dedicated payload, falls through decodeEventLine's default {node,port,value}
  // shape — same as recv/hover. The routed counterpart of the "time.abc-drag" breadcrumb;
  // the buffer's Overlay.AbcDragCount column is what the in-editor label actually reads.
  | { step: number; kind: "abc-drag"; node: string; port?: string; value?: number };

/**
 * Decode a snapshot into `.probe/go.jsonl` lines (one JSON object per line, trailing \n each).
 * Returns "" when the frame is undecodable or carries no events. Each line uses the shared
 * envelope { ts_ms, src:"go", ...fields } — the same envelope the ext host's stdout relay used
 * (minus the `step` ordinal, which the buffer path does not carry).
 *
 * `nodeFrameBuf` is the most-recently cached BUF_BLOCK_TAG_NODE frame and `edgeFrameBuf`
 * the most-recently cached BUF_BLOCK_TAG_EDGE frame (see runCommand.ts handleFd3): the
 * EVENT block's node/port row references (node-geometry, node-bead, recv/send/etc.'s
 * node+port labels) resolve against the Node/Port blocks + Label/PortName bytes, and its
 * edge row references (geometry, select) resolve against the Edge block + EdgeLabel
 * bytes — both now live in their own separate frames rather than riding the scene
 * snapshot `ab`. Passing undefined (no frame cached yet) degrades that identity to ""
 * rather than throwing — the same graceful-empty convention nodeLabel/portName already
 * use for an out-of-range row.
 */
/** camera/overlay/scene views resolved from EITHER source — the SCENE frame's embedded
 *  blocks (fallback, no dedicated view fd) OR the dedicated VIEW frame (see
 *  webview/three/view-blocks.ts's ext-host-side mirror). Null fields mean neither
 *  source has landed for that block yet. */
interface ViewBlocksOrNull {
  cameraView: DataView | null;
  overlayView: DataView | null;
  sceneView: DataView | null;
}

// decodeBufferLog decodes the VIEW frame's own trailing EVENTS section — the fallback
// bucket of trace kinds not yet decentralized to their own owner fd (Fire/Recv/Send/
// Select/Hover/AbcDrag*/Camera/SceneSphere/overlay toggles/LayoutLink — see Buffer/
// pack.go's viewEventsSection). The fd-3 SCENE frame no longer carries an EVENT block at
// all (memory/feedback_no_single_writer_bridge.md); genuinely decentralized kinds
// (NodeGeometry/Geometry/Position/Arrive/NodeBead) arrive on their OWN owner fd instead —
// see decodeStreamFrameEvents below, called once per node/edge/interior stream frame.
export function decodeBufferLog(viewFrameBuf: ArrayBuffer, nodeFrameBuf?: ArrayBuffer, edgeFrameBuf?: ArrayBuffer): string {
  const dv = decodeViewFrame(viewFrameBuf);
  if (!dv || dv.eventCount === 0) return "";
  const dn = nodeFrameBuf ? decodeNodeFrame(nodeFrameBuf) : null;
  const de = edgeFrameBuf ? decodeEdgeFrame(edgeFrameBuf) : null;
  const vb: ViewBlocksOrNull = { cameraView: dv.cameraView, overlayView: dv.overlayView, sceneView: dv.sceneView };
  return decodeEventsFromView(dv.eventCount, dv.eventView, dn, de, vb);
}

function decodeEventsFromView(eventCount: number, eventView: DataView, dn: DecodedNodeFrame | null, de: DecodedEdgeFrame | null, vb: ViewBlocksOrNull): string {
  const now = Date.now();
  let out = "";
  for (let i = 0; i < eventCount; i++) {
    const line = decodeEventLine(eventView, dn, de, vb, i);
    if (line) out += JSON.stringify({ ts_ms: now, src: "go", ...line }) + "\n";
  }
  return out;
}

// decodeStreamFrameEvents decodes ONE per-owner stream frame's (node/edge/interior) OWN
// trailing EVENTS section — the genuinely decentralized kinds (memory/
// feedback_no_single_writer_bridge.md: each emitting goroutine resolved its OWN rows at
// record time, on its OWN goroutine — see nodes/Wiring/owner_events.go). No cross-frame
// row resolution is attempted here (that owner goroutine already resolved what it could);
// node/port/edge identity strings are best-effort (dn/de optional) — the numeric rows the
// event already carries are always present regardless.
export function decodeStreamFrameEvents(eventCount: number, eventView: DataView, dn?: DecodedNodeFrame | null, de?: DecodedEdgeFrame | null): string {
  const now = Date.now();
  let out = "";
  for (let i = 0; i < eventCount; i++) {
    const line = decodeEventLine(eventView, dn ?? null, de ?? null, { cameraView: null, overlayView: null, sceneView: null }, i);
    if (line) out += JSON.stringify({ ts_ms: now, src: "go", ...line }) + "\n";
  }
  return out;
}

function overlayFlag(vb: ViewBlocksOrNull, kind: string): number {
  const v = vb.overlayView;
  if (!v) return 0;
  switch (kind) {
    case "scene-tori": return readOverlaySceneTori(v);
    case "scene-poles": return readOverlayScenePoles(v);
    case "node-poles": return readOverlayNodePoles(v);
    case "sel-sphere-poles": return readOverlaySelSpherePoles(v);
    case "handholds": return readOverlayHandholds(v);
    case "labels-global": return readOverlayLabelsGlobal(v);
    case "overlays-vis": return readOverlayOverlaysVis(v);
    case "double-links": return readOverlayDoubleLinks(v);
    default: return 0;
  }
}

const OVERLAY_KINDS = new Set([
  "scene-tori", "scene-poles", "node-poles", "sel-sphere-poles",
  "handholds", "labels-global", "overlays-vis", "double-links",
]);

function decodeEventLine(ev: DataView, dn: DecodedNodeFrame | null, de: DecodedEdgeFrame | null, vb: ViewBlocksOrNull, i: number): Line | null {
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
  const node = dn && nodeRow >= 0 ? nodeLabel(dn, nodeRow) : "";
  const port = dn ? portName(dn, portRow) : "";

  switch (kind) {
    case "recv":
      return { kind, node, port, value };
    case "fire":
      return { kind, node };
    case "send": {
      const arc = readEventArcLength(ev, i);
      const lat = readEventSimLatencyMs(ev, i);
      if (arc !== 0 || lat !== 0) {
        const l: Line = { kind, node, port, value, arcLength: arc, simLatencyMs: lat };
        const t = dn && targetRow >= 0 ? nodeLabel(dn, targetRow) : "";
        if (t) l.target = t;
        const th = dn ? portName(dn, targetPortRow) : "";
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
    case "arrive": {
      const l: Line = { kind, node, port, value };
      if (bead !== 0) l.bead = bead;
      return l;
    }
    case "geometry": {
      const edge = de ? edgeLabel(de, edgeRow) : "";
      // The Edge block carries NO endpoint coordinates — SrcPortRow/DstPortRow reference the
      // NODE frame's Port block, the ONLY place the endpoint's world position lives (see
      // bufLayoutEdge's doc comment, Buffer/layout.go).
      let sx = 0, sy = 0, sz = 0, ex = 0, ey = 0, ez = 0;
      if (de && dn && edgeRow >= 0 && edgeRow < de.edgeCount) {
        const srcRow = readEdgeSrcPortRow(de.edgeView, edgeRow);
        const dstRow = readEdgeDstPortRow(de.edgeView, edgeRow);
        if (srcRow >= 0) { sx = readPortPX(dn.portView, srcRow); sy = readPortPY(dn.portView, srcRow); sz = readPortPZ(dn.portView, srcRow); }
        if (dstRow >= 0) { ex = readPortPX(dn.portView, dstRow); ey = readPortPY(dn.portView, dstRow); ez = readPortPZ(dn.portView, dstRow); }
      }
      return { kind, edge, sx, sy, sz, ex, ey, ez };
    }
    case "node-geometry":
      return dn ? nodeGeometryLine(dn, nodeRow, node) : { kind, node };
    case "node-bead": {
      if (!dn) return { kind, node };
      const slot = readEventSlot(ev, i);
      const irow = nodeRow * INTERIOR_SLOTS_PER_NODE + slot;
      return {
        kind, node, row: Math.floor(slot / 2), col: slot % 2,
        present: readInteriorPresent(dn.interiorView, irow) === 1,
        value: readInteriorValue(dn.interiorView, irow),
        x: readInteriorOX(dn.interiorView, irow), y: readInteriorOY(dn.interiorView, irow), z: readInteriorOZ(dn.interiorView, irow),
      };
    }
    case "camera": {
      const c = vb.cameraView;
      if (!c) return { kind };
      return {
        kind,
        px: readCameraPX(c), py: readCameraPY(c), pz: readCameraPZ(c), r: readCameraR(c),
        posTheta: readCameraPosTheta(c), posPhi: readCameraPosPhi(c),
        upTheta: readCameraUpTheta(c), upPhi: readCameraUpPhi(c),
      };
    }
    case "scene-sphere": {
      const sc = vb.sceneView;
      if (!sc) return { kind };
      return { kind, cx: readSceneCX(sc), cy: readSceneCY(sc), cz: readSceneCZ(sc), radius: readSceneRadius(sc) };
    }
    case "layout-link": {
      const target = dn && targetRow >= 0 ? nodeLabel(dn, targetRow) : "";
      return { kind, node, target };
    }
    case "select":
      // stdout marshals select via the default {node,port,value} shape (edge label not emitted).
      return { kind, node, port: "", value };
    case "hover":
      return { kind, node, port, value };
    default:
      if (OVERLAY_KINDS.has(kind)) return { kind, visible: overlayFlag(vb, kind) === 1 };
      return { kind, node, port, value };
  }
}

function nodeGeometryLine(dn: DecodedNodeFrame, nodeRow: number, node: string): Line {
  // A node-geometry event riding the VIEW bucket resolves its node columns against the
  // last cached fd-3 node frame, which can be a STALE generation with fewer rows than the
  // topology — reading nodeRow past nodeView would throw. Degrade to the label-only line
  // (same graceful-empty contract as nodeLabel/portName), never crash the .probe logger.
  if (nodeRow < 0 || nodeRow >= dn.nodeCount) return { kind: "node-geometry", node };
  const n = dn.nodeView;
  const cx = readNodeCX(n, nodeRow), cy = readNodeCY(n, nodeRow), cz = readNodeCZ(n, nodeRow);
  const radius = readNodeRadius(n, nodeRow);
  const sphereR = readNodeSphereR(n, nodeRow);
  const kindId = readNodeKindId(n, nodeRow);
  const ports: Line[] = [];
  for (let pr = 0; pr < dn.portCount; pr++) {
    if (readPortNodeRow(dn.portView, pr) !== nodeRow) continue;
    const dx = readPortDX(dn.portView, pr), dy = readPortDY(dn.portView, pr), dz = readPortDZ(dn.portView, pr);
    ports.push({
      name: portName(dn, pr),
      isInput: readPortIsInput(dn.portView, pr) === 1,
      px: readPortPX(dn.portView, pr), py: readPortPY(dn.portView, pr), pz: readPortPZ(dn.portView, pr),
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
