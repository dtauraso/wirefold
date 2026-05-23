import { useCallback } from "react";
import { NODE_TYPES } from "../../../schema";
import { NODE_DEFS } from "../nodes/registry";
import { IDENT_RE } from "../../state/ops/rename";
import { scheduleSave, scheduleViewSave } from "../../save";
import { rfGetNodes, rfSetNodes } from "../rf-imperative";
import { pushSnapshot } from "../history";
import { PALETTE_DATA_TYPE } from "../panels/NodePalette";
import type { AppCtx } from "./_ctx";
import { specKindToRfType } from "../adapter/spec-to-flow";

export function useDragDrop(ctx: AppCtx) {
  const onDragOver = useCallback((ev: React.DragEvent) => {
    if (!Array.from(ev.dataTransfer.types).includes(PALETTE_DATA_TYPE)) return;
    ev.preventDefault();
    ev.dataTransfer.dropEffect = "copy";
  }, []);

  const onDrop = useCallback((ev: React.DragEvent) => {
    const type = ev.dataTransfer.getData(PALETTE_DATA_TYPE);
    if (!type || !NODE_TYPES[type]) return;
    ev.preventDefault();
    if (!ctx.lastSpec.current) return;
    const pos = ctx.rf.screenToFlowPosition({ x: ev.clientX, y: ev.clientY });
    // Mint a unique id from the type. Lowercase first char so the id is
    // a valid Go identifier the first time the runtime loader consumes it.
    const base = type.charAt(0).toLowerCase() + type.slice(1);
    let n = 0;
    let id = `${base}${n}`;
    const rfNodes = rfGetNodes();
    while (rfNodes.some((nd) => nd.id === id)) {
      n += 1;
      id = `${base}${n}`;
    }
    if (!IDENT_RE.test(id)) return;
    pushSnapshot();
    const def = NODE_TYPES[type];
    const width = def?.width ?? 110;
    const height = def?.height ?? 60;
    const rfType = specKindToRfType(type);
    const nodeDef = NODE_DEFS[rfType];
    const nodeData = nodeDef?.defaultData ?? undefined;
    rfSetNodes((ns) => [
      ...ns,
      {
        id,
        type: specKindToRfType(type),
        position: { x: pos.x, y: pos.y },
        data: {
          label: id,
          type,
          fill: def?.fill ?? "#ffffff",
          stroke: def?.stroke ?? "#888",
          shape: def?.shape ?? "rect",
          width,
          height,
          inputs: def?.inputs ?? [],
          outputs: def?.outputs ?? [],
          nodeData,
        },
      },
    ]);
    scheduleSave();
    scheduleViewSave();
  }, [ctx]);

  return { onDragOver, onDrop };
}
