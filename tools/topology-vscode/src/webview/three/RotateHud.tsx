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
  const ring1Ref = useRef<SVGCircleElement | null>(null);
  const ring2Ref = useRef<SVGCircleElement | null>(null);
  const ring3Ref = useRef<SVGCircleElement | null>(null);

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

    const makeCircle = (r: number) => {
      const c = document.createElementNS(ns, "circle");
      c.setAttribute("fill", "none");
      c.setAttribute("stroke-width", "1.5");
      svg.appendChild(c);
      return c;
    };
    ring1Ref.current = makeCircle(ROT_HUD_WIN_PX);
    ring2Ref.current = makeCircle(ROT_HUD_WIN_PX * 2);
    ring3Ref.current = makeCircle(ROT_HUD_WIN_PX * 3);

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
      // Live pointer (container-relative): the roll rings follow it.
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

      // Concentric rings: shown ONLY in roll, centered on the LIVE pointer so they
      // re-center as the mouse drifts (a new circle is "made" wherever it goes —
      // smaller circle = tighter curl = faster roll, from the turning-integration math).
      const ringColor = isTurntable ? HIDDEN : COLOR_BRIGHT;
      const setRing = (el: SVGCircleElement, r: number) => {
        el.setAttribute("cx", String(mx));
        el.setAttribute("cy", String(my));
        el.setAttribute("r", String(r));
        el.setAttribute("stroke", ringColor);
      };
      setRing(ring1Ref.current!, ROT_HUD_WIN_PX);
      setRing(ring2Ref.current!, ROT_HUD_WIN_PX * 2);
      setRing(ring3Ref.current!, ROT_HUD_WIN_PX * 3);
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
