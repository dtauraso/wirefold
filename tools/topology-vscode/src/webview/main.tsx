// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

// Early crash listeners — fire BEFORE React mounts so blank-window crashes
// are captured. These run synchronously here; CrashListeners (mounted inside
// the React tree) duplicates coverage once the tree is up and removes these.
{
  const earlyOnError = (e: ErrorEvent) => {
    postLog("early-window-error", {
      message: e.message,
      filename: e.filename,
      lineno: e.lineno,
      colno: e.colno,
      stack: (e.error as Error | undefined)?.stack ?? "",
    });
  };
  const earlyOnRejection = (e: PromiseRejectionEvent) => {
    const reason = e.reason as { message?: string; stack?: string } | undefined;
    postLog("early-unhandled-rejection", {
      message: reason?.message ?? String(e.reason),
      stack: reason?.stack ?? "",
    });
  };
  window.addEventListener("error", earlyOnError);
  window.addEventListener("unhandledrejection", earlyOnRejection);
}

import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { flushSave, flushViewSave } from "./save";
import { parseHostToWebview, ALL_PSEUDO_ERROR_TYPES, PSEUDO_PREFIX_TO_KIND, PSEUDO_KIND_PREFIX, type PseudoKind } from "../messages";
import { flowToSpec } from "./state/adapter/flow-to-spec";
import { setRunStatusImperative, registerRunStatusSetter, RunStatusCtx } from "./state/run-status";
import type { RunStatusUI } from "./state/run-status";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { RunButton } from "./three/RunButton";
import { SaveLifecycle } from "./SaveLifecycle";
import { useThreeStore } from "./three/store";
import { handleTraceEvent } from "./three/pump";

// Test-only hook for the Playwright e2e harness. The harness stub of
// acquireVsCodeApi populates window.__wirefold_sent with every postMessage
// call from the webview, so a test can assert both the live spec and that a
// save was posted.
(window as unknown as { __wirefold_test: unknown }).__wirefold_test = {
  getSpec: () => { const { nodes, edges } = useThreeStore.getState(); return flowToSpec(nodes, edges, { nodes: [], edges: [] }); },
  getSent: () =>
    (window as unknown as { __wirefold_sent?: unknown[] }).__wirefold_sent ?? [],
};

type PseudoBanner = { message: string; suggestion?: string } | null;
let setBannerImperative: (b: PseudoBanner) => void = () => {};

function Root() {
  const [runStatus, setRunStatus] = useState<RunStatusUI>({ state: "idle" });
  const [banner, setBanner] = useState<PseudoBanner>(null);
  useEffect(() => { registerRunStatusSetter(setRunStatus); }, []);
  useEffect(() => { setBannerImperative = setBanner; return () => { setBannerImperative = () => {}; }; }, []);
  return (
    <RunStatusCtx.Provider value={runStatus}>
      {/* SaveLifecycle and RunButton are mounted once here for all views. */}
      <SaveLifecycle />
      <RunButton />
      <ThreeView />
      {banner && (
        <div
          style={{
            position: "fixed",
            bottom: 12,
            left: "50%",
            transform: "translateX(-50%)",
            padding: "8px 12px",
            background: "#b00020",
            color: "white",
            borderRadius: 4,
            zIndex: 1000,
            maxWidth: 480,
            display: "flex",
            alignItems: "flex-start",
            gap: 8,
            fontFamily: "var(--vscode-font-family, sans-serif)",
            fontSize: 13,
          }}
        >
          <div style={{ flex: 1 }}>
            <div>{banner.message}</div>
            {banner.suggestion && (
              <div style={{ fontStyle: "italic", fontSize: 11, marginTop: 4, opacity: 0.9 }}>
                {banner.suggestion}
              </div>
            )}
          </div>
          <button
            onClick={() => setBanner(null)}
            style={{
              background: "transparent",
              border: "none",
              color: "white",
              cursor: "pointer",
              fontSize: 16,
              lineHeight: 1,
              padding: "0 2px",
            }}
            aria-label="Dismiss"
          >×</button>
        </div>
      )}
    </RunStatusCtx.Provider>
  );
}

postLog("lifecycle", { phase: "before-render" });
const app = document.getElementById("app")!;
createRoot(app).render(
  <ErrorBoundary>
    <CrashListeners />
    <Root />
  </ErrorBoundary>,
);

window.addEventListener("message", (e) => {
  const msg = parseHostToWebview(e.data);
  if (!msg) return;
  postLog("lifecycle", { phase: `msg:${msg.type}` });
  if (msg.type === "run-status") {
    setRunStatusImperative(msg.state === "error"
      ? { state: "error", message: msg.message ?? "" }
      : { state: msg.state as "running" | "paused" | "ok" | "cancelled" });
  } else if (msg.type === "flush") {
    // Host requests immediate flush of any pending debounced saves (panel
    // becoming hidden / about to dispose).
    flushSave();
    flushViewSave();
  } else if (msg.type === "load") {
    // Feed the R3F store; full document text includes spec + view key.
    useThreeStore.getState().load(msg.text);
  } else if (msg.type === "trace-event") {
    handleTraceEvent(msg.event);
  } else if (ALL_PSEUDO_ERROR_TYPES.has(msg.type)) {
    // Pseudo save/parse failed — show banner, then re-fire render so the
    // billboard text snaps back to the unchanged pseudo immediately.
    const { nodeId, message, suggestion } = msg as { nodeId: string; message: string; suggestion?: string };
    setBannerImperative({ message, suggestion });
    setTimeout(() => setBannerImperative(null), 6000);
    const prefix = msg.type.slice(0, -"-error".length);
    if (prefix in PSEUDO_PREFIX_TO_KIND) {
      const kind = PSEUDO_PREFIX_TO_KIND[prefix as keyof typeof PSEUDO_PREFIX_TO_KIND];
      const renderPrefix = PSEUDO_KIND_PREFIX[kind as PseudoKind];
      vscode.postMessage({ type: `${renderPrefix}-render`, nodeId });
    }
  } else if (msg.type.endsWith("-render-result")) {
    // Generated pseudocode for a hasPseudo node — patch into node data so the
    // billboard sublabel ternary can render it. Saved sublabel overrides win.
    const { nodeId, pseudo } = msg as { nodeId: string; pseudo: string };
    console.debug("[pseudo]", nodeId, pseudo?.slice(0, 40));
    useThreeStore.getState().setNodes((ns) =>
      ns.map((n) => (n.id === nodeId ? { ...n, data: { ...n.data, pseudo } } : n)),
    );
  }
  // load for 2D is fully handled inside App's message effect.
});

// Signal readiness after the message listener is registered so the host's
// load response is guaranteed not missed.
vscode.postMessage({ type: "ready" });
postLog("lifecycle", { phase: "ready-sent" });
