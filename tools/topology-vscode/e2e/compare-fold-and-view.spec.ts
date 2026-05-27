// Tier 3 system-shape (Phase 5):
//   fold + diff — when a folded region is expanded, member nodes that
//   differ between live and comparison still carry their diff classes.
//   Documents the rule: fold state and diff state compose; neither
//   swallows the other. (Downgraded scope: the *collapsed* placeholder
//   badge with category counts is a future enhancement; the expanded
//   case alone proves composition.)

import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const HERE = __dirname;
const HARNESS_URL = pathToFileURL(resolve(HERE, "harness.html")).toString();

declare global {
  interface Window {
    __wirefold_fixture: string;
    __wirefold_view_fixture?: string;
  }
}

const live = () => readFileSync(resolve(HERE, "fixtures", "compare-live.json"), "utf-8");
const other = () => readFileSync(resolve(HERE, "fixtures", "compare-other.json"), "utf-8");

async function loadCompareLive(page: import("@playwright/test").Page, view?: object) {
  const liveText = live();
  const viewText = view ? JSON.stringify(view) : undefined;
  await page.addInitScript(
    ({ s, v }: { s: string; v?: string }) => {
      window.__wirefold_fixture = s;
      if (v) window.__wirefold_view_fixture = v;
    },
    { s: liveText, v: viewText },
  );
  await page.goto(HARNESS_URL);
  await page.waitForSelector('.react-flow__node[data-id="a"]');
  await page.waitForSelector('.react-flow__node[data-id="b"]');
  await page.evaluate((text: string) => {
    window.postMessage(
      { type: "compare-load", source: "file", text, label: "fixtures/compare-other.json" },
      "*",
    );
  }, other());
}

test.describe("Phase 5 — system-shape composition", () => {
  test("fold + diff: expanded fold members keep their diff classes", async ({ page }) => {
    // Pre-set viewerState with an *expanded* fold containing the moved
    // member "b". Expanded folds render members; the diff decoration must
    // still attach .diff-moved to b regardless of the fold wrapper.
    await loadCompareLive(page, {
      folds: [
        {
          id: "fold-test",
          label: "test fold",
          memberIds: ["b"],
          position: [300, 100],
          collapsed: false,
        },
      ],
    });

    // Wait for diff decoration to settle (fold render + decorate pass).
    await expect.poll(async () =>
      page.locator('.react-flow__node[data-id="b"].diff-moved').count(),
    ).toBeGreaterThan(0);

    // Sanity: the fold frame is also rendered (so we know we're in the
    // expanded-with-members state, not a fallback).
    await expect(page.locator(".fold-frame")).toHaveCount(1);
  });

});
