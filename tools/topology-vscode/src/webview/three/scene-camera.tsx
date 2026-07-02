// scene-camera.tsx — CameraSettleDetector.
import { useEffect, useRef } from "react";
import { useThree, useFrame } from "@react-three/fiber";

// ---------------------------------------------------------------------------
// CameraSettleDetector: fires onSettle ~250ms after the camera stops moving.
// Compares camera matrix each frame; on change resets a debounce timer.
// ---------------------------------------------------------------------------

export function CameraSettleDetector({
  onSettle,
}: {
  onSettle: () => void;
}) {
  const { camera } = useThree();
  const lastElements = useRef<Float32Array>(new Float32Array(16));
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useFrame(() => {
    // Compare matrix elements with epsilon matching toFixed(2) rounding (~5e-3).
    camera.updateMatrixWorld();
    const els = camera.matrixWorld.elements;
    const EPSILON = 5e-3;
    let changed = false;
    for (let i = 0; i < 16; i++) {
      // i < 16 and both arrays hold 16 elements — indices are in range.
      if (Math.abs(els[i]! - lastElements.current[i]!) > EPSILON) { changed = true; break; }
    }
    if (changed) {
      lastElements.current.set(els);
      if (timerRef.current !== null) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        onSettle();
      }, 250);
    }
  });

  // Clear any pending settle timer on unmount so it can't fire onSettle after the
  // component is gone.
  useEffect(() => () => {
    if (timerRef.current !== null) clearTimeout(timerRef.current);
  }, []);

  return null;
}
