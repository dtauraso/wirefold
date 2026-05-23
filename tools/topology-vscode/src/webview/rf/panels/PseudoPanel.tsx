// PseudoPanel — inline label with double-click-to-edit for the algebraic
// pseudo view of an Input node. Toggled open/closed by the "ƒ" button.
//
// Lifecycle:
//   mount → post pseudo-render → receive pseudo-render-result → show label
//   double-click label → edit mode (contentEditable div, auto-focused)
//   each input (debounced ~250 ms) → post pseudo-save
//     pseudo-save-result → accept; update last-known-good
//     pseudo-error       → keep edit mode; overlay (PseudoErrorOverlay) shows error
//   blur → exit edit mode; if last attempt errored, revert to last-known-good

import React, { useEffect, useRef, useState, useCallback } from "react";
import { vscode } from "../../vscode-api";

type Props = {
  nodeId: string;
  onClose?: () => void;
};

type EditState =
  | { mode: "label" }
  | { mode: "editing" };

export function PseudoPanel({ nodeId }: Props) {
  // Last-known-good text (from pseudo-render-result or last pseudo-save-result)
  const [lkg, setLkg] = useState<string | null>(null);
  // Current buffer while editing
  const bufRef = useRef<string>("");
  const [editState, setEditState] = useState<EditState>({ mode: "label" });
  const editDivRef = useRef<HTMLDivElement | null>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Track whether the last auto-save attempt errored
  const lastSaveErrored = useRef(false);

  // Subscribe to host messages relevant to this nodeId.
  useEffect(() => {
    const handler = (e: MessageEvent<unknown>) => {
      const data = e.data as {
        type?: string;
        nodeId?: string;
        pseudo?: string;
        message?: string;
      } | null;
      if (!data || typeof data !== "object") return;
      if (data.nodeId !== nodeId) return;

      if (
        data.type === "pseudo-render-result" &&
        typeof data.pseudo === "string"
      ) {
        setLkg(data.pseudo);
      } else if (
        data.type === "pseudo-save-result" &&
        typeof data.pseudo === "string"
      ) {
        // Accept: update last-known-good to whatever was just saved.
        setLkg(data.pseudo);
        lastSaveErrored.current = false;
      } else if (
        data.type === "pseudo-save-result"
      ) {
        // save-result without pseudo field — treat as accepted, keep buffer
        lastSaveErrored.current = false;
      } else if (
        data.type === "pseudo-error" &&
        typeof data.message === "string"
      ) {
        lastSaveErrored.current = true;
      }
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [nodeId]);

  // Post pseudo-render on mount.
  useEffect(() => {
    vscode.postMessage({ type: "pseudo-render", nodeId });
  }, [nodeId]);

  // Focus the editable div whenever edit mode activates.
  // Set textContent imperatively so React never touches the DOM during editing.
  useEffect(() => {
    if (editState.mode === "editing" && editDivRef.current) {
      const el = editDivRef.current;
      el.textContent = lkg ?? "";
      el.focus();
      // Select all
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = window.getSelection();
      sel?.removeAllRanges();
      sel?.addRange(range);
    }
  }, [editState.mode]); // intentionally omit lkg — only runs on mode transition

  const scheduleSave = useCallback(
    (text: string) => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        vscode.postMessage({ type: "pseudo-save", nodeId, pseudo: text });
      }, 250);
    },
    [nodeId]
  );

  const handleDoubleClick = () => {
    bufRef.current = lkg ?? "";
    lastSaveErrored.current = false;
    setEditState({ mode: "editing" });
  };

  const handleInput = (e: React.FormEvent<HTMLDivElement>) => {
    const text = (e.currentTarget.textContent ?? "").trim();
    bufRef.current = text;
    scheduleSave(text);
  };

  const handleBlur = () => {
    // Cancel any pending debounced save.
    if (debounceRef.current) clearTimeout(debounceRef.current);

    if (lastSaveErrored.current) {
      // Fire one final save so the host surfaces the error in the status bar.
      vscode.postMessage({ type: "pseudo-save", nodeId, pseudo: bufRef.current });
      // Revert visible text to last-known-good.
      if (editDivRef.current && lkg !== null) {
        editDivRef.current.textContent = lkg;
      }
      bufRef.current = lkg ?? "";
    }
    setEditState({ mode: "label" });
  };

  // ── Styles ─────────────────────────────────────────────────────────────────

  // Matches the node container's base text style (fontSize 11, inherited color)
  // plus a top separator so it doesn't crowd the label above.
  const panel: React.CSSProperties = {
    marginTop: 4,
    width: "100%",
    boxSizing: "border-box",
    fontFamily: "monospace",
    fontSize: 11,
  };

  // Inherits font/color from node; italic+dim while loading.
  const labelStyle: React.CSSProperties = {
    display: "block",
    padding: "2px 0",
    cursor: "text",
    whiteSpace: "pre-wrap",
    wordBreak: "break-all",
    color: lkg === null ? "#666" : "inherit",
    fontStyle: lkg === null ? "italic" : "normal",
    userSelect: "text",
    textAlign: "center",
  };

  // Edit mode: same look as label, only a subtle dashed outline to signal focus.
  // textAlign is intentionally left (not center) so click-to-place-caret works
  // reliably in Chromium webviews — centered contentEditable breaks hit-testing.
  const editDivStyle: React.CSSProperties = {
    display: "inline-block",
    width: "100%",
    boxSizing: "border-box",
    padding: "2px 0",
    whiteSpace: "pre-wrap",
    wordBreak: "break-all",
    outline: "1px dashed #888",
    outlineOffset: 2,
    color: "inherit",
    minHeight: "1.4em",
    cursor: "text",
    textAlign: "left",
  };

  // ── Render ─────────────────────────────────────────────────────────────────

  const displayText =
    lkg === null ? "loading…" : lkg === "" ? "(empty)" : lkg;

  return (
    <div style={panel}>
      {editState.mode === "label" ? (
        <span
          style={labelStyle}
          onDoubleClick={lkg !== null ? handleDoubleClick : undefined}
          title={lkg !== null ? "Double-click to edit" : undefined}
        >
          {displayText}
        </span>
      ) : (
        <div
          ref={editDivRef}
          style={editDivStyle}
          contentEditable="plaintext-only"
          suppressContentEditableWarning
          spellCheck={false}
          onInput={handleInput}
          onBlur={handleBlur}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              lastSaveErrored.current = true; // force revert on blur
              editDivRef.current?.blur();
            }
          }}
        />
      )}
    </div>
  );
}
