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
  set: (c: PolarCamera) => void;
  setSceneToriVisible: (v: boolean) => void;
}

export const useCameraStore = create<CameraState>((set) => ({
  camera: null,
  sceneToriVisible: true,
  set: (c) => set({ camera: c }),
  setSceneToriVisible: (v) => set({ sceneToriVisible: v }),
}));

export function getCameraState(): CameraState {
  return useCameraStore.getState();
}
