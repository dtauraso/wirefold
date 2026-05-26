// Module-level folds state — mirrors rf-imperative.ts pattern so
// non-React callers can read/write folds without Zustand.

import type { Fold } from "./viewer/types";
export type { Fold };

let _folds: Fold[] = [];

export function getFolds(): Fold[] {
  return _folds;
}

export function setFolds(next: Fold[]) {
  _folds = next;
}

export function toggleFoldCollapse(id: string): boolean {
  const fold = _folds.find((f) => f.id === id);
  if (!fold) return false;
  _folds = _folds.map((f) =>
    f.id === id ? { ...f, collapsed: !f.collapsed } : f
  );
  return true;
}

export function updateFoldPosition(id: string, x: number, y: number) {
  _folds = _folds.map((f) =>
    f.id === id ? { ...f, position: [x, y] as [number, number] } : f
  );
}
