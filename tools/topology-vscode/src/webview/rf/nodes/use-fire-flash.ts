// useFireFlash — returns true for 400ms after lastFire changes.
// Nodes use this to briefly highlight when the Go runtime fires them.
// No substrate logic — pure visual affordance driven by data.

import { useEffect, useRef, useState } from "react";
import { ANIMATION_FIELDS } from "../animation-fields";

/** The field name for the lastFire animation field (from registry). */
export const LAST_FIRE_FIELD = ANIMATION_FIELDS.lastFire.name;

const FLASH_MS = 400;

export function useFireFlash(lastFire: number | undefined): boolean {
  const [flashing, setFlashing] = useState(false);
  const prev = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (lastFire === undefined || lastFire === prev.current) return;
    prev.current = lastFire;
    setFlashing(true);
    const t = setTimeout(() => setFlashing(false), FLASH_MS);
    return () => clearTimeout(t);
  }, [lastFire]);

  return flashing;
}
