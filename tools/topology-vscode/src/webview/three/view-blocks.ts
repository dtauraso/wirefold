// view-blocks.ts — the ONE read for camera/overlay/scene, sourced from the dedicated VIEW
// stream (see memory/feedback_no_single_writer_bridge.md). WIREFOLD_STREAM_FDS "view" is
// now MANDATORY (the fd-3 SnapshotState accumulator + its fallback SCENE frame were
// deleted, memory/feedback_no_single_writer_bridge.md's final step) — null means "no frame has arrived yet",
// not "the dedicated path is off".
//
// Every render-path consumer of camera/overlay/scene (BufferCamera.tsx, NodeInstances.tsx,
// EdgeTube.tsx, NavGuides.tsx, overlay-flags.ts) reads through getViewBlocks() /
// subscribeViewBlocks() instead of decoding the VIEW frame directly, so the read is
// expressed in exactly ONE place.

import { getLatestViewFrame, subscribeViewFrame } from "../snapshot-buffer";
import { decodeViewFrame } from "./buffer-decode";

export interface ViewBlocks {
  cameraView: DataView;
  overlayView: DataView;
  sceneView: DataView;
}

/** Read the current camera/overlay/scene views from the dedicated VIEW stream. Null until
 *  the first frame has landed. Pure read — no store writes. */
export function getViewBlocks(): ViewBlocks | null {
  const viewBuf = getLatestViewFrame();
  if (!viewBuf) return null;
  return decodeViewFrame(viewBuf);
}

/** Subscribe to VIEW-stream updates (subscribe-fn shape, e.g. for a React external-store
 *  hook). */
export function subscribeViewBlocks(fn: () => void): () => void {
  return subscribeViewFrame(fn);
}
