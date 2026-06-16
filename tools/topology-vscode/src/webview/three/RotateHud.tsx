// RotateHud.tsx — screen-space HUD that visualizes the rotate gesture state machine.
// Polls rotHudRef each frame via rAF; draws deadzone strips (turntable) and
// concentric roll rings (roll), highlighted based on active mode.
// Visualization only — no gesture math, no side effects.

import { useEffect, useRef } from "react";
import type React from "react";
import { ROT_HUD_WIN_PX } from "./interaction-controls";

// ---------------------------------------------------------------------------
// Color constants — matched to the label overlay palette
// ---------------------------------------------------------------------------

const COLOR_DIM = "rgba(120,180,255,0.18)";
const COLOR_BRIGHT = "rgba(120,200,255,0.55)";

// ---------------------------------------------------------------------------
// RotateHud component
// ---------------------------------------------------------------------------

interface RotateHudProps {
  rotHudRef: React.MutableRefObject<{
    active: boolean;
    x: number;  // anchor (deadzone-window center, client coords) — strips
    y: number;
    mx: number; // live pointer (client coords) — roll rings follow it
    my: number;
    mode: "turntable" | "roll";
  }>;
  containerRef: React.MutableRefObject<HTMLDivElement | null>;
}

export function RotateHud({ rotHudRef, containerRef }: RotateHudProps) {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Stable element refs for the SVG children we mutate each frame.
  const hStripRef = useRef<SVGRectElement | null>(null);
  const vStripRef = useRef<SVGRectElement | null>(null);
  const ringsRef = useRef<SVGCircleElement[]>([]);

  useEffect(() => {
    const svg = svgRef.current;
    const wrap = wrapRef.current;
    if (!svg || !wrap) return;

    // Build SVG children once.
    const ns = "http://www.w3.org/2000/svg";

    const hStrip = document.createElementNS(ns, "rect");
    hStrip.setAttribute("fill", COLOR_DIM);
    svg.appendChild(hStrip);
    hStripRef.current = hStrip;

    const vStrip = document.createElementNS(ns, "rect");
    vStrip.setAttribute("fill", COLOR_DIM);
    svg.appendChild(vStrip);
    vStripRef.current = vStrip;

    // Pool of concentric-ring elements; how many are shown grows with the pointer's
    // distance from the anchor (a ring is "added outside" as the mouse moves out).
    const MAX_RINGS = 16;
    ringsRef.current = [];
    for (let i = 0; i < MAX_RINGS; i++) {
      const c = document.createElementNS(ns, "circle");
      c.setAttribute("fill", "none");
      c.setAttribute("stroke-width", "1.5");
      svg.appendChild(c);
      ringsRef.current.push(c);
    }

    let rafId: number;

    const tick = () => {
      rafId = requestAnimationFrame(tick);
      const hud = rotHudRef.current;

      if (!hud.active) {
        wrap.style.opacity = "0";
        wrap.style.visibility = "hidden";
        return;
      }

      wrap.style.opacity = "1";
      wrap.style.visibility = "visible";

      const container = containerRef.current;
      if (!container) return;
      const rect = container.getBoundingClientRect();

      // Convert client coords to container-relative.
      const cx = hud.x - rect.left;
      const cy = hud.y - rect.top;
      const W = rect.width;
      const H = rect.height;

      // Update SVG viewport to match container.
      svg.setAttribute("width", String(W));
      svg.setAttribute("height", String(H));
      svg.setAttribute("viewBox", `0 0 ${W} ${H}`);

      const isTurntable = hud.mode === "turntable";
      const HIDDEN = "rgba(0,0,0,0)";
      const halfW = ROT_HUD_WIN_PX;
      // Live pointer (container-relative) — only used to size how many rings to show.
      const mx = hud.mx - rect.left;
      const my = hud.my - rect.top;

      // Strips: shown ONLY in turntable, centered on the anchor. They VANISH in roll.
      const stripColor = isTurntable ? COLOR_BRIGHT : HIDDEN;
      const hStrip = hStripRef.current!;
      hStrip.setAttribute("x", "0");
      hStrip.setAttribute("y", String(cy - halfW));
      hStrip.setAttribute("width", String(W));
      hStrip.setAttribute("height", String(halfW * 2));
      hStrip.setAttribute("fill", stripColor);

      const vStrip = vStripRef.current!;
      vStrip.setAttribute("x", String(cx - halfW));
      vStrip.setAttribute("y", "0");
      vStrip.setAttribute("width", String(halfW * 2));
      vStrip.setAttribute("height", String(H));
      vStrip.setAttribute("fill", stripColor);

      // Concentric rings: shown ONLY in roll, centered on the ANCHOR (fixed — they do
      // not move while rolling). One more ring than the pointer's radius is shown, so a
      // ring is "added outside" as the mouse moves out. The mouse rides around them;
      // smaller ring = less travel per angle = faster roll.
      const rings = ringsRef.current;
      const pointerR = Math.hypot(mx - cx, my - cy);
      const shown = isTurntable ? 0 : Math.min(rings.length, Math.max(3, Math.ceil(pointerR / halfW) + 1));
      for (let i = 0; i < rings.length; i++) {
        const el = rings[i];
        if (i < shown) {
          el.setAttribute("cx", String(cx));
          el.setAttribute("cy", String(cy));
          el.setAttribute("r", String((i + 1) * halfW));
          el.setAttribute("stroke", COLOR_BRIGHT);
        } else {
          el.setAttribute("stroke", HIDDEN);
        }
      }
    };

    tick();
    return () => {
      cancelAnimationFrame(rafId);
      // Clean up children so React can remount cleanly.
      while (svg.firstChild) svg.removeChild(svg.firstChild);
    };
  }, [rotHudRef, containerRef]);

  return (
    <div
      ref={wrapRef}
      style={{
        position: "absolute",
        inset: 0,
        pointerEvents: "none",
        zIndex: 20,
        opacity: 0,
        visibility: "hidden",
      }}
    >
      <svg
        ref={svgRef}
        style={{ position: "absolute", inset: 0 }}
        aria-hidden="true"
      />
    </div>
  );
}
