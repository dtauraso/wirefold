// @vitest-environment jsdom
//
// ErrorBoundary.tsx — catches render-time errors in its subtree, posts a structured
// log entry (postLog "render-error"), and renders a minimal fallback instead of going
// blank silently. Plain DOM class component (no react-three-fiber), in scope per brief.

import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";

// post.ts -> vscode-api.ts calls acquireVsCodeApi() at module load; stub before import.
const postMessage = vi.fn();
(globalThis as unknown as { acquireVsCodeApi: () => unknown }).acquireVsCodeApi = () => ({
  postMessage,
  setState: () => {},
  getState: () => ({}),
});

const { ErrorBoundary } = await import("../src/webview/log/ErrorBoundary");

function Boom(): never {
  throw new Error("kaboom");
}

// Suppress React's expected console.error noise for the intentionally-thrown error.
const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});

describe("ErrorBoundary", () => {
  it("renders children normally when nothing throws", () => {
    const { getByText } = render(
      <ErrorBoundary>
        <div>fine</div>
      </ErrorBoundary>,
    );
    expect(getByText("fine")).toBeTruthy();
  });

  it("catches a render-time error, shows the fallback, and posts a render-error log", () => {
    const { getByText } = render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(getByText(/webview render error: kaboom/)).toBeTruthy();
    expect(postMessage).toHaveBeenCalledWith(
      expect.objectContaining({
        type: "webview-log",
        entry: expect.stringContaining("kaboom"),
      }),
    );
  });
});

void consoleErrorSpy;
