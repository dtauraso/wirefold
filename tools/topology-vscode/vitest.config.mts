import { defineConfig } from "vitest/config";
import * as path from "path";

export default defineConfig({
  test: {
    include: ["test/**/*.test.ts", "test/**/*.test.tsx"],
    environment: "node",
    // `vscode` is injected by the VS Code runtime and is unresolvable under vitest.
    // Alias it to a minimal stub so extension-host modules import cleanly in unit tests.
    alias: {
      vscode: path.resolve(__dirname, "test/stubs/vscode.ts"),
    },
  },
});
