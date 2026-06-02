// node-dims.ts — single source of truth for default node dimensions.
// Used wherever node width/height may be absent from NodeData.
export const NODE_DIM_FALLBACK = { width: 110, height: 60 } as const;
