// useFireFlash — returns true for 400ms after lastFire changes.
// Nodes use this to briefly highlight when the Go runtime fires them.
// No substrate logic — pure visual affordance driven by fire-flash-state.ts.

import { useEffect, useRef, useState } from "react";

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
