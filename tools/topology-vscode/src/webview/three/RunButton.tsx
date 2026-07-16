import { createPortal } from "react-dom";
import { vscode } from "../vscode-api";
import { useRunStatusCtx } from "../state/run-status";
import { useClockHalted } from "./clock-state";

export function RunButton() {
  const status = useRunStatusCtx();
  const clockHalted = useClockHalted();
  const mount = document.getElementById("run-mount");
  if (!mount) return null;

  // ONE fact drives the button: is Go's clock actually ticking. isActive ("a Go process is
  // spawned") is the ext host's: a dead process cannot report that it is dead. clockHalted
  // is Go's, streamed in the buffer's Clock block — NOT predicted from the stdin play/pause
  // write. The buffer describes a live process, so it means nothing without isActive.
  //
  // Go's clock has ONE gate and ONE Resume() (see stdin_reader.go handlePlayMsg) — it
  // cannot distinguish "never started" from "user paused", and should not have to. So there
  // are only TWO button states, not three: running (clock ticking) vs. not (halted, whether
  // that's pre-start, post-pause, or post-stop). There is no separate "resume" label/action;
  // the halted case always posts {type:"run"}, which handle-message.ts's "run" case
  // resolves correctly for both first start and resume-after-pause (idempotent runner.run()
  // then runner.play()).
  const isRunning = isActiveAndTicking(status, clockHalted);

  const onPlayPause = () => {
    if (isRunning) {
      vscode.postMessage({ type: "pause" });
      return;
    }
    // Not running: process never started, is paused, or was stopped and needs a restart.
    // handle-message's "run" case calls runner.run() (idempotent spawn) then runner.play().
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
        title={isRunning ? "pause" : "go run . in repo root"}
        onClick={onPlayPause}
        disabled={false}
      >
        {isRunning ? "⏸ pause" : "▶ run"}
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
      <span className={statusClass(status, isRunning)}>{statusText(status, isRunning)}</span>
    </>,
    mount,
  );
}

function isActiveAndTicking(
  status: ReturnType<typeof useRunStatusCtx>,
  clockHalted: boolean | null,
): boolean {
  return status.state === "active" && clockHalted === false;
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
