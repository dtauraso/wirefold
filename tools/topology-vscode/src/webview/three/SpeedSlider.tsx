import { useState } from "react";
import { createPortal } from "react-dom";
import { postGoRecord } from "../vscode-api";
import { encodeClockSpeed } from "../../schema/input-layout";

// SpeedSlider — a playback-speed control. Speed is Go-owned state (the clock); this
// component holds only the EPHEMERAL slider position (local UI control value, not domain
// state) and fire-and-forgets each change to Go via the edit-update(clock, speed) wire
// record. No await, no Promise chain (check-no-await-on-bridge).
export function SpeedSlider() {
  const [speed, setSpeed] = useState(1);
  const mount = document.getElementById("run-mount");
  if (!mount) return null;

  const onChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = Number(e.target.value);
    setSpeed(value);
    postGoRecord(encodeClockSpeed(value));
  };

  return createPortal(
    <span className="speed-slider">
      <input
        type="range"
        min={0}
        max={2}
        step={1}
        value={speed}
        onChange={onChange}
        aria-label="playback speed"
      />
      <span className="speed-slider-label">{speed}</span>
    </span>,
    mount,
  );
}
