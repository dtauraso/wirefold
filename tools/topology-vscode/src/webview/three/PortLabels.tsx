// PortLabels.tsx — renders each port's NAME as a billboard label at its streamed buffer
// world position, gated on the typed-equation form's portName autocomplete being open
// (port-autocomplete-context.tsx). Render-only: it decodes the latest snapshot's Port block
// each frame and draws AxisLabel sprites; it authors nothing and holds no domain state — just
// a per-frame render cache (dataRef), the same pattern NavGuides uses for its nav-node cache.

import { useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { portName } from "./buffer-decode";
import { readPortNodeRow, readPortPX, readPortPY, readPortPZ } from "../../schema/buffer-layout";
import { AxisLabel } from "./NavGuides";
import { usePortAutocompleteContext } from "./port-autocomplete-context";

interface PortLabelDatum {
  row: number;
  label: string;
  x: number;
  y: number;
  z: number;
}

export function PortLabels() {
  const { value } = usePortAutocompleteContext();
  const [, setTick] = useState(0);
  const dataRef = useRef<PortLabelDatum[]>([]);

  useFrame(() => {
    if (!value) {
      if (dataRef.current.length > 0) {
        dataRef.current = [];
        setTick((t) => t + 1);
      }
      return;
    }
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (!decoded) return;
    const list: PortLabelDatum[] = [];
    for (let i = 0; i < decoded.portCount; i++) {
      if (readPortNodeRow(decoded.portView, i) !== value.nodeRow) continue;
      list.push({
        row: i,
        label: portName(decoded, i),
        x: readPortPX(decoded.portView, i),
        y: readPortPY(decoded.portView, i),
        z: readPortPZ(decoded.portView, i),
      });
    }
    dataRef.current = list;
    setTick((t) => t + 1);
  });

  if (!value || dataRef.current.length === 0) return null;

  return (
    <>
      {dataRef.current.map((p) => {
        const emphasized = p.row === value.highlightedRow;
        return (
          <AxisLabel
            key={p.row}
            text={p.label || String(p.row)}
            color={emphasized ? "#ffcc00" : "#88ccff"}
            position={[p.x, p.y, p.z]}
            size={emphasized ? 12 : 7}
          />
        );
      })}
    </>
  );
}
