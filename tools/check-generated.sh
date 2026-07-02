#!/usr/bin/env bash
# Guard against stale generated TS files.
# Runs the generator and fails if any generated file differs from the committed version.
# Generated files: src/schema/node-defs.ts, src/schema/node-data-types.ts,
#                  src/schema/wire-defs.ts, src/schema/trace-kinds.ts,
#                  src/schema/trace-event-fields.ts,
#                  src/schema/curve-params.ts, src/schema/shading-params.ts,
#                  src/schema/buffer-layout.ts,
#                  nodes/Wiring/node_dims_gen.go, nodes/Wiring/overlay_gen.go,
#                  Buffer/buffer_layout_gen.go
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1

(cd tools/topology-vscode && npm run --silent gen:node-defs 2>&1) || {
  echo "check-generated: generator failed" >&2
  exit 1
}

stale=$(git status --porcelain \
  tools/topology-vscode/src/schema/node-defs.ts \
  tools/topology-vscode/src/schema/node-data-types.ts \
  tools/topology-vscode/src/schema/wire-defs.ts \
  tools/topology-vscode/src/schema/trace-kinds.ts \
  tools/topology-vscode/src/schema/trace-event-fields.ts \
  tools/topology-vscode/src/schema/curve-params.ts \
  tools/topology-vscode/src/schema/shading-params.ts \
  tools/topology-vscode/src/schema/buffer-layout.ts \
  nodes/Wiring/node_dims_gen.go \
  nodes/Wiring/overlay_gen.go \
  Buffer/buffer_layout_gen.go 2>/dev/null)

if [ -n "$stale" ]; then
  echo "check-generated: stale generated file(s) — commit the regenerated output:"
  echo "$stale"
  exit 1
fi
exit 0
