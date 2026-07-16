import { createPortal } from "react-dom";
import { vscode } from "../vscode-api";
import { useRunStatusCtx } from "../state/run-status";
import { useClockHalted, useClockHasRunOnce } from "./clock-state";
import { deriveRunButtonState } from "./run-button-state";

export function RunButton() {
  const status = useRunStatusCtx();
  const clockHalted = useClockHalted();
  const hasRunOnce = useClockHasRunOnce();
  const mount = document.getElementById("run-mount");
  if (!mount) return null;

  const isActive = status.state === "active";
  const btn = deriveRunButtonState(isActive, clockHalted, hasRunOnce);

  const onPlayPause = () => {
    // Literal `postMessage({ type: "..." })` calls, not a dynamic `type: btn.action` —
    // check-message-kind-parity.sh statically greps for literal senders per kind.
    if (btn.action === "pause") {
      vscode.postMessage({ type: "pause" });
      return;
    }
    vscode.postMessage({ type: "run" });
  };

  const onStop = () => {
    vscode.postMessage({ type: "stop" });
  };

  return createPortal(
    <>
      <button
        type="button"
        className="run-btn"
        title={btn.isRunning ? "pause" : "go run . in repo root"}
        onClick={onPlayPause}
        disabled={false}
      >
        {btn.label}
      </button>
      <button
        type="button"
        className="run-btn run-stop-btn"
        title="stop the running process"
        onClick={onStop}
        disabled={status.state !== "active"}
      >
        ■ stop
      </button>
      <span className={statusClass(status, btn.isRunning)}>{statusText(status, btn.isRunning)}</span>
    </>,
    mount,
  );
}

// running is now Go's own truth (clockHalted, via isRunning above), not a field on the
// ext-host RunStatus — so it's taken as an explicit param rather than read off a
// "running"/"paused" state on `s` (that state no longer exists on the wire).
function statusClass(s: ReturnType<typeof useRunStatusCtx>, isRunning: boolean): string {
  if (isRunning) return "run-running";
  if (s.state === "active") return "run-running"; // spawned but clock halted: still "in progress"
  if (s.state === "ok") return "run-ok";
  if (s.state === "cancelled") return "run-idle";
  if (s.state === "error") return "run-error";
  return "run-idle";
}

function statusText(s: ReturnType<typeof useRunStatusCtx>, isRunning: boolean): string {
  if (isRunning) return "running…";
  if (s.state === "active") return "paused"; // spawned but clock halted — accurate either way
  if (s.state === "ok") return "ok";
  if (s.state === "cancelled") return "cancelled";
  if (s.state === "error") return s.message;
  return "";
}
