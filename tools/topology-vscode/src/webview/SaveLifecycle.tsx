import { useEffect } from "react";
import { flushViewSave } from "./save";

// Flushes any pending debounced scene save on page hide / visibility change.
export function SaveLifecycle() {
  useEffect(() => {
    const flush = () => flushViewSave();
    const onVis = () => { if (document.visibilityState === "hidden") flush(); };
    window.addEventListener("pagehide", flush);
    window.addEventListener("visibilitychange", onVis);
    return () => {
      window.removeEventListener("pagehide", flush);
      window.removeEventListener("visibilitychange", onVis);
    };
  }, []);
  return null;
}
