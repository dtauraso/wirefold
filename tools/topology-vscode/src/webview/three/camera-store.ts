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
  set: (c: PolarCamera) => void;
  setSceneToriVisible: (v: boolean) => void;
  setScenePolesVisible: (v: boolean) => void;
  setNodePolesVisible: (v: boolean) => void;
  setAngleLabelsVisible: (v: boolean) => void;
  setSelSpherePolesVisible: (v: boolean) => void;
}

export const useCameraStore = create<CameraState>((set) => ({
  camera: null,
  sceneToriVisible: true,
  scenePolesVisible: true,
  nodePolesVisible: true,
  angleLabelsVisible: true,
  selSpherePolesVisible: true,
  set: (c) => set({ camera: c }),
  setSceneToriVisible: (v) => set({ sceneToriVisible: v }),
  setScenePolesVisible: (v) => set({ scenePolesVisible: v }),
  setNodePolesVisible: (v) => set({ nodePolesVisible: v }),
  setAngleLabelsVisible: (v) => set({ angleLabelsVisible: v }),
  setSelSpherePolesVisible: (v) => set({ selSpherePolesVisible: v }),
}));

export function getCameraState(): CameraState {
  return useCameraStore.getState();
}
