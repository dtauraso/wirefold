// PanPolarOverlay.tsx — screen-space "mouse as polar" overlay shown during a wheel-pan burst.
// Plot-only: reads usePanPolarStore (driven by onWheelNative). The mouse's polar sphere is
// centered on the panned-to location: pole = up, refX = horizontal, and the 3rd line (refY) is
// the view — the ⊙ pointing into the screen. The pan slide runs center → start anchor in the
// refX–pole plane; θ is measured from refX. Cartesian wheel delta in (the input edge), polar
// picture out — nothing computes here.

import React from "react";
import { usePanPolarStore } from "./pan-polar-store";

const SPHERE_R = 70; // px — the planted mouse-sphere reference circle (fixed size)
const REF_LEN = 40; // px — refX (horizontal) / pole (up) reference ticks

export function PanPolarOverlay() {
  const active = usePanPolarStore((s) => s.active);
  const cx = usePanPolarStore((s) => s.cx);
  const cy = usePanPolarStore((s) => s.cy);
  const dx = usePanPolarStore((s) => s.dx);
  const dy = usePanPolarStore((s) => s.dy);
  if (!active) return null;

  const r = Math.hypot(dx, dy);
  // The sphere is centered on the PANNED-TO location (anchor + accumulated delta); the start
  // anchor stays put as a trailing reference, drifting out of the sphere as the pan grows.
  const Cx = cx + dx;
  const Cy = cy + dy;

  // θ = angle from refX (horizontal, +x) to the pan slide (center → start anchor).
  const a3 = Math.atan2(cy - Cy, cx - Cx);
  let dTheta = a3;
  while (dTheta > Math.PI) dTheta -= 2 * Math.PI;
  while (dTheta <= -Math.PI) dTheta += 2 * Math.PI;
  const thetaDeg = Math.round(Math.abs(dTheta) * (180 / Math.PI));
  const ARC_R = 30;
  const arcPts: string[] = [];
  const M = 18;
  for (let i = 0; i <= M; i++) {
    const a = dTheta * (i / M);
    arcPts.push(`${(Cx + Math.cos(a) * ARC_R).toFixed(1)},${(Cy + Math.sin(a) * ARC_R).toFixed(1)}`);
  }
  const aMid = dTheta / 2;
  const tlx = Cx + Math.cos(aMid) * (ARC_R + 18);
  const tly = Cy + Math.sin(aMid) * (ARC_R + 18);

  return (
    <svg
      style={{
        position: "absolute",
        inset: 0,
        width: "100%",
        height: "100%",
        pointerEvents: "none",
        overflow: "visible",
        zIndex: 20,
      }}
    >
      {/* mouse sphere — centered on the panned-to location */}
      <circle cx={Cx} cy={Cy} r={SPHERE_R} fill="none" stroke="#2f3a5c" strokeWidth={1.5} />
      {/* refX = horizontal reference */}
      <line x1={Cx} y1={Cy} x2={Cx + REF_LEN} y2={Cy} stroke="#57b6ff" strokeWidth={2} />
      <text x={Cx + REF_LEN + 4} y={Cy + 4} fill="#57b6ff" fontSize={12} fontFamily="monospace">
        refX
      </text>
      {/* pole = up reference */}
      <line x1={Cx} y1={Cy} x2={Cx} y2={Cy - REF_LEN} stroke="#c79cff" strokeWidth={2} />
      <text x={Cx + 6} y={Cy - REF_LEN - 2} fill="#c79cff" fontSize={12} fontFamily="monospace">
        pole
      </text>
      {/* θ — angle from refX to the pan slide */}
      {r > 0.5 && (
        <>
          <polyline points={arcPts.join(" ")} fill="none" stroke="#57b6ff" strokeWidth={2} />
          <text x={tlx - 6} y={tly + 4} fill="#57b6ff" fontSize={13} fontFamily="monospace">
            θ={thetaDeg}°
          </text>
        </>
      )}
      {/* pan slide: panned-to center → start anchor, in the refX–pole plane */}
      <line x1={Cx} y1={Cy} x2={cx} y2={cy} stroke="#ffcc44" strokeWidth={3} />
      <text x={(Cx + cx) / 2 + 8} y={(Cy + cy) / 2 + 4} fill="#ffcc44" fontSize={12} fontFamily="monospace">
        pan · r={Math.round(r)}
      </text>
      {/* start anchor — trailing reference (dim) */}
      <circle cx={cx} cy={cy} r={4} fill="#9aa6c0" />
      {/* 3rd line = view (⊙ into the screen) at center */}
      <circle cx={Cx} cy={Cy} r={9} fill="none" stroke="#66ff99" strokeWidth={2} />
      <circle cx={Cx} cy={Cy} r={4} fill="#e7ecf5" />
    </svg>
  );
}
