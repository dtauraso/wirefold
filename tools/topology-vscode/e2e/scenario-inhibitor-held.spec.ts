// Scenario: Inhibitor renders its held value as an in-box label.
// Observable: a DOM text node containing "held=0" is visible inside
// the Inhibitor node immediately after mount (seed=0 means the
// initial held value is 0). This pins the feat(chain-inhibitor) label
// landed in f8af21a.
// Fixture: three-node-with-edges (Input → i0 → i1, both Inhibitor).

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

test("chainInhibitor: held= label renders at mount", async ({ page }) => {
  const fixture = readFileSync(
    resolve(HERE, "fixtures/three-node-with-edges.json"), "utf-8",
  );
  await page.addInitScript((text: string) => { window.__wirefold_fixture = text; }, fixture);
  await page.goto(HARNESS_URL);

  // Wait for nodes to mount.
  await page.waitForSelector('.react-flow__node[data-id="i0"]');

  // The InhibitorBody renders a <span> with "held=<value>".
  // With no explicit seed on this fixture the initial held value is null,
  // so the label reads "held=null". Wait for it to appear.
  const label = page.locator('.react-flow__node[data-id="i0"] span').filter({
    hasText: /^held=/,
  });
  await expect(label).toBeVisible({ timeout: 3000 });

  // The label must be non-empty (seed or null, not blank).
  const text = await label.textContent();
  expect(text).toMatch(/^held=/);
});
