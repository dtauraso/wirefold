// Canonical names of the .probe/ diagnosis files (five rolling JSONL logs plus
// the append-only handler-error post-mortem). These names are
// duplicated across the Go/TS boundary (Go writes via its own paths; the shell
// reader tools/probe-merge.sh hardcodes them since shell can't import this) —
// but every TypeScript reference must route through here so the two TS writers
// (runCommand.ts, extension/webview-log.ts) cannot drift from each other.
//
// If you rename one, update tools/probe-merge.sh (and any Go-side path) too.
export const PROBE_DIR = ".probe";

export const PROBE_FILES = {
  go: "go.jsonl",
  goErrors: "go-errors.jsonl",
  goDebug: "go-debug.jsonl",
  ts: "ts.jsonl",
  tsErrors: "ts-errors.jsonl",
  handlerErrorLast: "handler-error-last.json",
} as const;
