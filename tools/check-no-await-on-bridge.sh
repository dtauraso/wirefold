#!/usr/bin/env bash
set -euo pipefail

# check-no-await-on-bridge.sh — Phase 5 guard.
#
# Asserts the TS→Go send path is FIRE-AND-FORGET: the editor places an edit/control
# message on the bridge and never blocks on Go (MODEL.md "Editor surface"; the spec's
# "no await / no Promises on the TS→Go path"). A blocking TS→Go call reintroduces
# event-loop coordination between nodes that should stay independent — the exact
# inversion this project rejects (CLAUDE.md "Medium vs. substance"; the await/Promise
# regression that hid pacing in the event loop).
#
# Two send hops make up the path:
#   webview → host:  vscode.postMessage(...)        (store.ts, interaction-controls.ts)
#   host    → Go:    runner.writeStdin(...)          (extension/handle-message.ts)
#
# This guard FAILS if either send is awaited, or if writeStdin is declared to return
# a Promise (which would let a caller await it / turn the send into request/response).
# Exit 0 when clean.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_DIR="$REPO_ROOT/tools/topology-vscode/src"
RUNCOMMAND="$SRC_DIR/runCommand.ts"

HITS=0
report() {
  printf '%s\n' "$1"
  HITS=$((HITS + 1))
}

# 1. No `await` directly on a bridge send call. Matches `await ...writeStdin(`
#    and `await ...postMessage(` anywhere in the webview source tree.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "await-on-send: $line"
done < <(grep -rnE 'await[[:space:]]+[A-Za-z0-9_.]*\.(writeStdin|postMessage)\(' \
  --include="*.ts" --include="*.tsx" "$SRC_DIR" 2>/dev/null || true)

# 2. No `.then(`/`.catch(`/`.finally(` chained onto a bridge send (request/response
#    or completion-coupling by another name).
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "promise-chain-on-send: $line"
done < <(grep -rnE '\.(writeStdin|postMessage)\([^)]*\)[[:space:]]*\.(then|catch|finally)\b' \
  --include="*.ts" --include="*.tsx" "$SRC_DIR" 2>/dev/null || true)

# 3. writeStdin must NOT be declared to return a Promise/Thenable — a void return is
#    what keeps the send fire-and-forget at the type level.
if [[ -f "$RUNCOMMAND" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    report "writeStdin-returns-promise: $line"
  done < <(grep -nE 'writeStdin\([^)]*\)[[:space:]]*:[[:space:]]*(Promise|Thenable)' "$RUNCOMMAND" 2>/dev/null || true)

  # Positive assertion: writeStdin must be declared returning void.
  if ! grep -qE 'writeStdin\([^)]*\)[[:space:]]*:[[:space:]]*void' "$RUNCOMMAND"; then
    report "writeStdin-not-void: $RUNCOMMAND does not declare writeStdin(...): void — the TS→Go send must be fire-and-forget"
  fi
fi

if [[ $HITS -eq 0 ]]; then
  echo "no-await-on-bridge: clean (TS→Go send is fire-and-forget; no await/Promise/request-response on the bridge)"
  exit 0
fi

echo ""
echo "no-await-on-bridge: $HITS hit(s) — the TS→Go send path must be fire-and-forget (no await, no Promise chain, no request/response)"
exit 1
