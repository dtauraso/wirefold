// Module-level folds state — mirrors rf-imperative.ts pattern so
// non-React callers can read/write folds without Zustand.

import type { Fold } from "./viewer/types";
export type { Fold };

let _folds: Fold[] = [];

export function getFolds(): Fold[] {
  return _folds;
}

