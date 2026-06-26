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
  // guidelinesActive is the TS-only master gate for the whole polar-guideline group
  // (tori + all pole frames + angle labels). When false the toolbar hides their individual
  // buttons and NavGuides suppresses every guide; each guide's own visibility above is
  // left untouched, so reactivating restores the prior states. Not Go-owned.
  guidelinesActive: boolean;
  set: (c: PolarCamera) => void;
  setSceneToriVisible: (v: boolean) => void;
  setScenePolesVisible: (v: boolean) => void;
  setNodePolesVisible: (v: boolean) => void;
  setAngleLabelsVisible: (v: boolean) => void;
  setSelSpherePolesVisible: (v: boolean) => void;
  setGuidelinesActive: (v: boolean) => void;
}

export const useCameraStore = create<CameraState>((set) => ({
  camera: null,
  sceneToriVisible: true,
  scenePolesVisible: true,
  nodePolesVisible: true,
  angleLabelsVisible: true,
  selSpherePolesVisible: true,
  guidelinesActive: true,
  set: (c) => set({ camera: c }),
  setSceneToriVisible: (v) => set({ sceneToriVisible: v }),
  setScenePolesVisible: (v) => set({ scenePolesVisible: v }),
  setNodePolesVisible: (v) => set({ nodePolesVisible: v }),
  setAngleLabelsVisible: (v) => set({ angleLabelsVisible: v }),
  setSelSpherePolesVisible: (v) => set({ selSpherePolesVisible: v }),
  setGuidelinesActive: (v) => set({ guidelinesActive: v }),
}));

export function getCameraState(): CameraState {
  return useCameraStore.getState();
}
