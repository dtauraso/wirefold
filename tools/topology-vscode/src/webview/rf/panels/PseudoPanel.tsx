// PseudoPanel — floating panel for editing the algebraic pseudo view of an
// Input node. Opened from the "ƒ" button on the node header; closed via
// Save or Cancel.
//
// Lifecycle:
//   mount → post pseudo-render → wait for pseudo-render-result
//   Save  → post pseudo-save  → wait for pseudo-save-result → onClose
//   Cancel → onClose immediately
//   Error (either phase) → show inline message above textarea

import React, { useEffect, useRef, useState } from "react";
import { vscode } from "../../vscode-api";

type Props = {
  nodeId: string;
  onClose: () => void;
};

type Phase =
  | { kind: "loading" }
  | { kind: "ready"; pseudo: string }
  | { kind: "saving"; pseudo: string }
  | { kind: "error"; pseudo: string; message: string };

export function PseudoPanel({ nodeId, onClose }: Props) {
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });
  const textRef = useRef<HTMLTextAreaElement | null>(null);

  // Subscribe to host messages relevant to this nodeId.
  useEffect(() => {
    const handler = (e: MessageEvent<unknown>) => {
      const data = e.data as { type?: string; nodeId?: string; pseudo?: string; message?: string } | null;
      if (!data || typeof data !== "object") return;
      if (data.nodeId !== nodeId) return;
      if (data.type === "pseudo-render-result" && typeof data.pseudo === "string") {
        setPhase({ kind: "ready", pseudo: data.pseudo });
      } else if (data.type === "pseudo-save-result") {
        onClose();
      } else if (data.type === "pseudo-error" && typeof data.message === "string") {
        setPhase((prev) => {
          const pseudo = prev.kind === "loading" ? "" : (prev as { pseudo?: string }).pseudo ?? "";
          return { kind: "error", pseudo, message: data.message! };
        });
      }
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [nodeId, onClose]);

  // Post pseudo-render on mount.
  useEffect(() => {
    vscode.postMessage({ type: "pseudo-render", nodeId });
  }, [nodeId]);

  const currentPseudo =
    phase.kind === "loading" ? "" : (phase as { pseudo?: string }).pseudo ?? "";

  const handleSave = () => {
    const text = textRef.current?.value ?? currentPseudo;
    setPhase({ kind: "saving", pseudo: text });
    vscode.postMessage({ type: "pseudo-save", nodeId, pseudo: text });
  };

  const handleCancel = () => onClose();

  const panel: React.CSSProperties = {
    background: "#1e1e1e",
    border: "1px solid #444",
    borderRadius: 4,
    padding: "8px",
    marginTop: 6,
    width: "100%",
    boxSizing: "border-box",
    display: "flex",
    flexDirection: "column",
    gap: 6,
    fontFamily: "monospace",
    fontSize: 11,
    color: "#ccc",
  };

  const titleRow: React.CSSProperties = {
    display: "flex",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 4,
  };

  const title: React.CSSProperties = { fontWeight: 600, fontSize: 11, color: "#ddd" };

  const errorBox: React.CSSProperties = {
    background: "#3b1212",
    border: "1px solid #a33",
    borderRadius: 3,
    padding: "4px 8px",
    color: "#f66",
    fontSize: 11,
  };

  const textareaStyle: React.CSSProperties = {
    width: "100%",
    height: 90,
    resize: "none",
    background: "#252526",
    color: "#ccc",
    border: "1px solid #555",
    borderRadius: 3,
    padding: "4px 6px",
    fontFamily: "monospace",
    fontSize: 11,
    boxSizing: "border-box",
  };

  const btnRow: React.CSSProperties = {
    display: "flex",
    justifyContent: "flex-end",
    gap: 8,
    marginTop: 4,
  };

  const btnBase: React.CSSProperties = {
    padding: "3px 12px",
    borderRadius: 3,
    border: "1px solid #666",
    cursor: "pointer",
    fontSize: 12,
    fontFamily: "sans-serif",
  };

  const btnSave: React.CSSProperties = {
    ...btnBase,
    background: "#0e639c",
    color: "#fff",
    borderColor: "#0e639c",
  };

  const btnCancel: React.CSSProperties = { ...btnBase, background: "transparent", color: "#ccc" };

  const isBusy = phase.kind === "loading" || phase.kind === "saving";

  return (
    <div style={panel}>
      <div style={titleRow}>
        <span style={title}>pseudo — Input</span>
        <span style={{ fontSize: 10, color: "#888" }}>{nodeId}</span>
      </div>
      {phase.kind === "error" && (
        <div style={errorBox}>{phase.message}</div>
      )}
      {phase.kind === "loading" ? (
        <div style={{ color: "#888", padding: "4px 0" }}>loading…</div>
      ) : (
        <textarea
          ref={textRef}
          style={textareaStyle}
          defaultValue={currentPseudo}
          disabled={isBusy}
          spellCheck={false}
        />
      )}
      <div style={btnRow}>
        <button style={btnCancel} onClick={handleCancel} disabled={phase.kind === "saving"}>
          Cancel
        </button>
        <button style={btnSave} onClick={handleSave} disabled={isBusy}>
          {phase.kind === "saving" ? "Saving…" : "Save"}
        </button>
      </div>
    </div>
  );
}
