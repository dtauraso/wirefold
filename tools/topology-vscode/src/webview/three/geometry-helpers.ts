// geometry-helpers.ts — pure NDC ↔ pixel conversion helpers for the 3D view.
// No React, no scene state, no node/edge geometry (all geometry is Go-owned and arrives
// via the binary content buffer).

export function ndcToPixel(ndcX: number, ndcY: number, size: { width: number; height: number }): { px: number; py: number } {
  const px = (ndcX + 1) / 2 * size.width;
  const py = (1 - (ndcY + 1) / 2) * size.height;
  return { px, py };
}

export function pixelToNDC(clientX: number, clientY: number, rect: DOMRect): { ndcX: number; ndcY: number } {
  const ndcX = ((clientX - rect.left) / rect.width) * 2 - 1;
  const ndcY = -((clientY - rect.top) / rect.height) * 2 + 1;
  return { ndcX, ndcY };
}
