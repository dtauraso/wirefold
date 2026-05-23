import { useEffect, useRef, useState } from "react";
import { parseHostToWebview } from "../../../messages";

type ErrorState = { message: string; suggestion: string } | null;

const AUTO_DISMISS_MS = 10_000;

export function PseudoErrorOverlay() {
  const [error, setError] = useState<ErrorState>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    function handler(e: MessageEvent) {
      const msg = parseHostToWebview(e.data);
      if (!msg) return;
      if (msg.type === "pseudo-error") {
        // Reset auto-dismiss timer on each new error.
        if (timerRef.current !== null) clearTimeout(timerRef.current);
        setError({ message: msg.message, suggestion: msg.suggestion ?? "" });
        timerRef.current = setTimeout(() => setError(null), AUTO_DISMISS_MS);
      } else if (msg.type === "pseudo-save-result") {
        if (timerRef.current !== null) clearTimeout(timerRef.current);
        setError(null);
      }
    }
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, []);

  if (!error) return null;

  return (
    <div style={{
      position: "fixed",
      bottom: 12,
      left: 12,
      zIndex: 9999,
      pointerEvents: "none",
    }}>
      <div style={{
        pointerEvents: "auto",
        background: "rgba(30, 10, 10, 0.92)",
        color: "#f8c8c8",
        fontFamily: "monospace",
        fontSize: 12,
        lineHeight: 1.5,
        padding: "8px 12px",
        borderRadius: 4,
        border: "1px solid rgba(220, 80, 80, 0.5)",
        maxWidth: 480,
        boxShadow: "0 2px 8px rgba(0,0,0,0.5)",
      }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", gap: 8 }}>
          <span style={{ flex: 1, wordBreak: "break-word" }}>{error.message}</span>
          <button
            onClick={() => setError(null)}
            title="Dismiss"
            style={{
              background: "none",
              border: "none",
              color: "#f8c8c8",
              cursor: "pointer",
              fontSize: 14,
              lineHeight: 1,
              padding: 0,
              flexShrink: 0,
            }}
          >✕</button>
        </div>
        {error.suggestion && (
          <div style={{ marginTop: 4, color: "#c8e8c8", fontSize: 11 }}>
            {error.suggestion}
          </div>
        )}
      </div>
    </div>
  );
}
