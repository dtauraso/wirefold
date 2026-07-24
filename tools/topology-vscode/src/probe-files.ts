// Canonical names of the .probe/ diagnosis files. These names are duplicated across the
// Go/TS boundary (Go writes via its own paths; the shell reader tools/probe-merge.sh
// hardcodes them since shell can't import this) — but every TypeScript reference must
// route through here so the two TS writers (runCommand.ts, extension/webview-log.ts)
// cannot drift from each other.
//
// If you rename one, update tools/probe-merge.sh (and any Go-side path) too.
export const PROBE_DIR = ".probe";

export const PROBE_FILES = {
  // go: the VIEW stream's own .probe log (camera/overlay/scene events). Every trace kind
  // is now decentralized to its own owner fd — this bucket carries none of the
  // node/edge/interior kinds. goNode/goEdge/goInterior are
  // SEPARATE per-owner-KIND logs (memory/feedback_no_single_writer_bridge.md — N owners,
  // N logs, no merge into one file): each is the decode of every node/edge/interior
  // stream's OWN trailing EVENTS section (NodeGeometry, Geometry/Position/Arrive,
  // NodeBead respectively). Use tools/probe-merge.sh to view them together sorted by
  // ts_ms when that's what you want; they are never merged on write.
  go: "go.jsonl",
  goNode: "go-node.jsonl",
  goEdge: "go-edge.jsonl",
  goInterior: "go-interior.jsonl",
  goErrors: "go-errors.jsonl",
  ts: "ts.jsonl",
  tsErrors: "ts-errors.jsonl",
  handlerErrorLast: "handler-error-last.json",
} as const;
