// camera-store.ts — Go-owned polar camera state, written by pump on "camera" trace events.
// The store holds the last camera snapshot reported by Go; consumers read it for rendering.

import { create } from "zustand";

export type PolarCamera = {
  pivot: [number, number, number];
  r: number;
  pos: [number, number];
  up: [number, number];
};

interface CameraState {
  camera: PolarCamera | null;
  set: (c: PolarCamera) => void;
}

export const useCameraStore = create<CameraState>((set) => ({
  camera: null,
  set: (c) => set({ camera: c }),
}));

export function getCameraState(): CameraState {
  return useCameraStore.getState();
}
