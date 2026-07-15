import { createPortal } from "react-dom";
import { vscode } from "../vscode-api";
import { useRunStatusCtx } from "../state/run-status";
import { useClockHalted } from "./clock-state";

export function RunButton() {
  const status = useRunStatusCtx();
  const clockHalted = useClockHalted();
  const mount = document.getElementById("run-mount");
  if (!mount) return null;

  // Two facts, each owned by the only side that can know it. isActive ("a Go process is
  // spawned") is the ext host's: a dead process cannot report that it is dead. clockHalted
  // is Go's, streamed in the buffer's Clock block — NOT predicted from the stdin play/pause
  // write. The buffer describes a live process, so it means nothing without isActive.
  const isActive = status.state === "active"; // process is alive
  const isRunning = isActive && clockHalted === false;
  const isPaused = isActive && clockHalted !== false; // includes clockHalted===true and
  // the brief instant right after spawn before the first snapshot has arrived (null) —
  // Go starts halted (main.go), so treating "not yet known" as paused matches the true
  // initial state and avoids a false "running" flash.

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
      <span className={statusClass(status, isRunning, isPaused)}>
        {statusText(status, isRunning, isPaused)}
      </span>
    </>,
    mount,
  );
}

// running/paused are now Go's own truth (clockHalted, via isRunning/isPaused above), not a
// field on the ext-host RunStatus — so these take them as explicit params rather than
// reading a "running"/"paused" state off `s` (that state no longer exists on the wire).
function statusClass(s: ReturnType<typeof useRunStatusCtx>, isRunning: boolean, isPaused: boolean): string {
  if (isRunning || isPaused) return "run-running";
  if (s.state === "ok") return "run-ok";
  if (s.state === "cancelled") return "run-idle";
  if (s.state === "error") return "run-error";
  return "run-idle";
}

function statusText(s: ReturnType<typeof useRunStatusCtx>, isRunning: boolean, isPaused: boolean): string {
  if (isRunning) return "running…";
  if (isPaused) return "paused";
  if (s.state === "ok") return "ok";
  if (s.state === "cancelled") return "cancelled";
  if (s.state === "error") return s.message;
  return "";
}
