import { createContext, useContext } from "react";

export interface EdgeActions {
  setEdgeLane: (edgeId: string, lane: number) => void;
  setPortPosition: (nodeId: string, portName: string, side: "left" | "right" | "top" | "bottom", slot: 0 | 1 | 2) => void;
}

export const EdgeActionsCtx = createContext<EdgeActions | null>(null);

export function useEdgeActions(): EdgeActions | null {
  return useContext(EdgeActionsCtx);
}
