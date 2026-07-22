#!/usr/bin/env bash
set -euo pipefail

# Guards the CLAUDE.md "Wire props" claim: the edge `label` prop rides the Edge block's
# EdgeLabelOff/EdgeLabelLen columns SOLELY for the `.probe` buffer-decoded log, and the
# edge RENDERER (EdgeTube.tsx) reads ONLY SX..EZ/Selected from the Edge block — never the
# label columns. If a future change wired label into the edge shader, that documented
# contract would be silently violated (a wire prop reaching the screen without being packed
# and consumed deliberately). Nothing but prose enforced this before; this makes it a
# grep-detectable fact.
#
# The label columns' ONE legitimate reader is buffer-decode.ts (the .probe decoder). The
# generated readers live in buffer-layout.ts. Everything else under the three/ render tree —
# EdgeTube.tsx above all — must not reference them. Exit 0 clean, exit 1 with a report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
THREE_DIR="$REPO_ROOT/tools/topology-vscode/src/webview/three"

if [ ! -d "$THREE_DIR" ]; then
  # Render tree absent (partial checkout) — nothing to guard, not a failure.
  exit 0
fi

# The forbidden tokens: the generated label-column readers and the raw column names. A
# renderer that draws with the label has to name one of these to reach the bytes.
PATTERN='readEdgeEdgeLabelOff|readEdgeEdgeLabelLen|EdgeLabelOff|EdgeLabelLen'

# Search the whole render tree, then drop the ONE allowed reader (buffer-decode.ts) and the
# generated layout file (buffer-layout.ts, which DEFINES the readers). Any surviving hit is
# a renderer reaching for the label — the drift this guards against.
hits=$(grep -rnE "$PATTERN" "$THREE_DIR" --include="*.ts" --include="*.tsx" \
  | grep -v '/buffer-decode.ts:' \
  | grep -v '/buffer-layout.ts:' \
  || true)

if [ -n "$hits" ]; then
  echo "check-edge-label-usage: the edge label columns must be read ONLY by buffer-decode.ts"
  echo "(the .probe decoder), never by the render tree (CLAUDE.md 'Wire props': label never"
  echo "feeds the render path). Offending references:"
  echo "$hits"
  exit 1
fi

exit 0
