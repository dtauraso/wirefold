// camera-store.ts — Go-owned scene state, written by pump on trace events.
// The store holds the last snapshot reported by Go; consumers read it for rendering.

import { create } from "zustand";

export type PolarCamera = {
  pivot: [number, number, number];
  r: number;
  pos: [number, number];
  up: [number, number];
};

interface CameraState {
  camera: PolarCamera | null;
  sceneToriVisible: boolean;
  scenePolesVisible: boolean;
  nodePolesVisible: boolean;
  angleLabelsVisible: boolean;
  selSpherePolesVisible: boolean;
  handholdsVisible: boolean;
  // labelsGlobalHidden is true when global node-label visibility is off. Default false (labels shown).
  // Written by pump on "labels-global" trace events; read by ThreeView's label overlay gate.
  labelsGlobalHidden: boolean;
  // badgesHidden is true when global occlusion-badge visibility is off. Default false (badges shown).
  // Written by pump on "badges-global" trace events; read by ThreeView's badge render gate.
  badgesHidden: boolean;
  // overlaysVisible is the Go-owned master gate for all 8 overlays. When false the toolbar
  // hides the individual sub-buttons and NavGuides/ThreeView suppress every overlay;
  // each overlay's own Go-owned visibility is left untouched, so reactivating restores
  // the prior per-overlay states. Written by pump on "overlays-vis" trace events.
  overlaysVisible: boolean;
  // doubleLinksVisible is the Go-owned toggle for the double-link overlay. When true,
  // edge tubes are dimmed and each edge shows a cyan bidirectional arrow line at port endpoints.
  // Written by pump on "double-links" trace events.
  doubleLinksVisible: boolean;
  set: (c: PolarCamera) => void;
  setSceneToriVisible: (v: boolean) => void;
  setScenePolesVisible: (v: boolean) => void;
  setNodePolesVisible: (v: boolean) => void;
  setAngleLabelsVisible: (v: boolean) => void;
  setSelSpherePolesVisible: (v: boolean) => void;
  setHandholdsVisible: (v: boolean) => void;
  setLabelsGlobalHidden: (v: boolean) => void;
  setBadgesHidden: (v: boolean) => void;
  setOverlaysVisible: (v: boolean) => void;
  setDoubleLinksVisible: (v: boolean) => void;
}

export const useCameraStore = create<CameraState>((set) => ({
  camera: null,
  sceneToriVisible: true,
  scenePolesVisible: true,
  nodePolesVisible: true,
  angleLabelsVisible: true,
  selSpherePolesVisible: true,
  handholdsVisible: true,
  labelsGlobalHidden: false,
  badgesHidden: false,
  overlaysVisible: true,
  doubleLinksVisible: false,
  set: (c) => set({ camera: c }),
  setSceneToriVisible: (v) => set({ sceneToriVisible: v }),
  setScenePolesVisible: (v) => set({ scenePolesVisible: v }),
  setNodePolesVisible: (v) => set({ nodePolesVisible: v }),
  setAngleLabelsVisible: (v) => set({ angleLabelsVisible: v }),
  setSelSpherePolesVisible: (v) => set({ selSpherePolesVisible: v }),
  setHandholdsVisible: (v) => set({ handholdsVisible: v }),
  setLabelsGlobalHidden: (v) => set({ labelsGlobalHidden: v }),
  setBadgesHidden: (v) => set({ badgesHidden: v }),
  setOverlaysVisible: (v) => set({ overlaysVisible: v }),
  setDoubleLinksVisible: (v) => set({ doubleLinksVisible: v }),
}));
