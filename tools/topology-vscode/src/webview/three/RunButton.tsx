import { createPortal } from "react-dom";
import { vscode } from "../vscode-api";
import { useRunStatusCtx } from "../state/run-status";

export function RunButton() {
  const status = useRunStatusCtx();
  const mount = document.getElementById("run-mount");
  if (!mount) return null;

  const isRunning = status.state === "running";
  const isPaused = status.state === "paused";
  const isActive = isRunning || isPaused; // process is alive

  const onPlayPause = () => {
    if (isPaused) {
      // Resume the clock — handle-message.ts's "resume" case calls runner.play() directly.
      vscode.postMessage({ type: "resume" });
      return;
    }
    if (isRunning) {
      vscode.postMessage({ type: "pause" });
      return;
    }
    // idle: Go is spawned but clock is halted, or process was stopped and needs
    // a restart. handle-message calls runner.run() (idempotent spawn) then runner.play().
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
        title={isPaused ? "resume" : isRunning ? "pause" : "go run . in repo root"}
        onClick={onPlayPause}
        disabled={false}
      >
        {isPaused ? "▶ resume" : isRunning ? "⏸ pause" : "▶ run"}
      </button>
      <button
        type="button"
        className="run-btn run-stop-btn"
        title="stop the running process"
        onClick={onStop}
        disabled={!isActive}
      >
        ■ stop
      </button>
      <span className={statusClass(status)}>{statusText(status)}</span>
    </>,
    mount,
  );
}

function statusClass(s: ReturnType<typeof useRunStatusCtx>): string {
  if (s.state === "running") return "run-running";
  if (s.state === "paused") return "run-running";
  if (s.state === "ok") return "run-ok";
  if (s.state === "cancelled") return "run-idle";
  if (s.state === "error") return "run-error";
  return "run-idle";
}

function statusText(s: ReturnType<typeof useRunStatusCtx>): string {
  if (s.state === "running") return "running…";
  if (s.state === "paused") return "paused";
  if (s.state === "ok") return "ok";
  if (s.state === "cancelled") return "cancelled";
  if (s.state === "error") return s.message;
  return "";
}
