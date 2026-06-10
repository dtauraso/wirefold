// Scenario: dragging a node does not disconnect its edges.
// Observable: the React Flow edge DOM element for each incident wire
// is still present in the DOM after the drag completes.
// Fixture: go-2node (Input → ReadGate, one edge).

import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const HERE = __dirname;
const HARNESS_URL = pathToFileURL(resolve(HERE, "harness.html")).toString();

declare global {
  interface Window {
    __wirefold_fixture: string;
  }
}

// React Flow renders edges as <g class="react-flow__edge"> elements. The
// edge SVG path has class react-flow__edge-path. Use aria-label to find
// the specific edge — RF sets aria-label="Edge from <source> to <target>".
const EDGE_ARIA = "Edge from in08 to readGate1";

test("wire-survives-drag: dragging a node keeps its edge in the DOM", async ({ page }) => {
  const fixture = readFileSync(resolve(HERE, "fixtures/go-2node.json"), "utf-8");
  await page.addInitScript((text: string) => { window.__wirefold_fixture = text; }, fixture);
  await page.goto(HARNESS_URL);

  await page.waitForSelector('.react-flow__node[data-id="in08"]');

  // Edge must be present before drag (RF renders it as an aria-labeled button).
  const edgeBefore = await page.getByRole("button", { name: EDGE_ARIA }).count();
  expect(edgeBefore, "edge missing before drag").toBeGreaterThan(0);

  // Drag the source node (Input).
  const node = page.locator('.react-flow__node[data-id="in08"]');
  const box = await node.boundingBox();
  if (!box) throw new Error("node bbox missing");
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.move(cx, cy);
  await page.mouse.down();
  await page.mouse.move(cx + 80, cy + 40, { steps: 8 });
  await page.mouse.up();

  // Edge must still be present after drag (wire was not severed by position change).
  await expect.poll(
    () => page.getByRole("button", { name: EDGE_ARIA }).count(),
    { timeout: 2000, message: "edge disappeared after drag" },
  ).toBeGreaterThan(0);
});
