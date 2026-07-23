// edge-stream-blocks.ts — the per-edge dedicated-stream either/or, mirroring
// view-blocks.ts's role for the VIEW stream (memory/feedback_no_single_writer_bridge.md).
// EdgeTube.tsx/BeadInstances.tsx read edge geometry/selection and beads through this ONE
// accessor rather than each re-implementing the "dedicated fds active, or fall back to the
// fd-3 Edge/Bead frame" branch.

import { getLatestEdgeStreamFrames } from "../snapshot-buffer";
import { decodeEdgeStreamFrame, type DecodedEdgeStreamFrame } from "./buffer-decode";
import { readEdgeSrcPortRow, readEdgeDstPortRow, readEdgeSelected, readBeadValue, readBeadX, readBeadY, readBeadZ } from "../../schema/buffer-layout";

export interface EdgeAccessor {
  /** One past the highest edge ROW that has posted at least one dedicated-stream frame —
   *  NOT frames.size (a row can arrive out of order at startup; using the size would
   *  misnumber a sparse row set as a dense 0..size-1 range, corrupting the row identity
   *  every downstream pick/selection lookup depends on). A row with no frame yet reads as
   *  "unresolved" (-1 / not selected / no beads), same treatment writeEdgeBlock gives an
   *  unresolved port. */
  edgeCount: number;
  srcPortRow(row: number): number;
  dstPortRow(row: number): number;
  selected(row: number): boolean;
  /** This edge row's current live beads (val/x/y/z), or an empty array if unresolved. */
  beads(row: number): Array<{ val: number; x: number; y: number; z: number }>;
}

function decodedFor(frames: ReadonlyMap<number, ArrayBuffer>, row: number): DecodedEdgeStreamFrame | null {
  const buf = frames.get(row);
  return buf ? decodeEdgeStreamFrame(row, buf) : null;
}

/**
 * getEdgeStreamAccessor returns the per-edge dedicated-stream accessor when at least one
 * edge-stream frame has arrived (the dedicated path is active — WIREFOLD_STREAM_FDS
 * carried an "edge" entry), else null (the required fallback: callers read the fd-3 Edge/
 * Bead frames instead, exactly as before this migration).
 */
export function getEdgeStreamAccessor(): EdgeAccessor | null {
  const frames = getLatestEdgeStreamFrames();
  if (frames.size === 0) return null;
  let maxRow = -1;
  for (const r of frames.keys()) if (r > maxRow) maxRow = r;
  const edgeCount = maxRow + 1;
  return {
    edgeCount,
    srcPortRow(row) {
      const d = decodedFor(frames, row);
      return d ? readEdgeSrcPortRow(d.edgeView, 0) : -1;
    },
    dstPortRow(row) {
      const d = decodedFor(frames, row);
      return d ? readEdgeDstPortRow(d.edgeView, 0) : -1;
    },
    selected(row) {
      const d = decodedFor(frames, row);
      return d ? readEdgeSelected(d.edgeView, 0) > 0 : false;
    },
    beads(row) {
      const d = decodedFor(frames, row);
      if (!d) return [];
      const out: Array<{ val: number; x: number; y: number; z: number }> = [];
      for (let i = 0; i < d.beadCount; i++) {
        out.push({
          val: readBeadValue(d.beadView, i),
          x: readBeadX(d.beadView, i),
          y: readBeadY(d.beadView, i),
          z: readBeadZ(d.beadView, i),
        });
      }
      return out;
    },
  };
}
