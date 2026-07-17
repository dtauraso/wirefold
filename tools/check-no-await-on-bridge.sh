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
# The REAL send surface. Every editor→Go send goes through one of these; there is
# no direct webview call to writeStdin/postMessage (checked by grepping the tree —
# see tools/check-no-await-on-bridge.sh history for the miss this replaced):
#   webview (call site)  -> postGoRecord()   (webview/vscode-api.ts)
#   webview (call site)  -> sendRawInput()   (webview/three/raw-input.ts, wraps postGoRecord)
#   host    -> Go        -> writeStdin()     (extension/runCommand.ts)
# postGoRecord itself is a thin wrapper over vscode.postMessage; sendRawInput wraps
# postGoRecord. Grepping for all three names (plus the raw postMessage/writeStdin
# primitives) catches a caller no matter which layer of wrapper they used.
#
# This guard FAILS if any of these is awaited, or `.then`/`.catch`/`.finally`-chained
# onto a call to any of these, or if writeStdin is declared to return a Promise.
# Exit 0 when clean.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_DIR="$REPO_ROOT/tools/topology-vscode/src"
RUNCOMMAND="$SRC_DIR/runCommand.ts"

# The forbidden-call set: every function (at any wrapper layer) that performs a
# TS->Go bridge send, plus the underlying primitives for defense in depth.
SEND_FNS='postGoRecord|sendRawInput|writeStdin|postMessage'

HITS=0
report() {
  printf '%s\n' "$1"
  HITS=$((HITS + 1))
}

# 1. No `await` directly on a bridge send call (bare call or via a receiver, e.g.
#    `await postGoRecord(...)`, `await sendRawInput(...)`, `await runner.writeStdin(...)`,
#    `await vscode.postMessage(...)`).
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "await-on-send: $line"
done < <(grep -arnE "await[[:space:]]+([A-Za-z0-9_]+\.)*($SEND_FNS)\(" \
  --include="*.ts" --include="*.tsx" "$SRC_DIR" 2>/dev/null || true)

# 2. No `.then(`/`.catch(`/`.finally(` chained onto a bridge send (request/response
#    or completion-coupling by another name). The send call's own ARGUMENTS may
#    contain nested parens (e.g. `postGoRecord(encodeRawInput(e)).then(...)`), so this
#    must NOT rely on a `[^)]*`-bounded arg match (that hides nested-paren chains —
#    verified: it let `postGoRecord(encodeRawInput(e)).then(() => {})` through
#    silently). `.*` is paren-agnostic and does not have that blind spot.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "promise-chain-on-send: $line"
done < <(grep -arnE "($SEND_FNS)\(.*\)[[:space:]]*\.(then|catch|finally)\b" \
  --include="*.ts" --include="*.tsx" "$SRC_DIR" 2>/dev/null || true)

# 3. writeStdin must NOT be declared to return a Promise/Thenable — a void return is
#    what keeps the send fire-and-forget at the type level.
[[ -f "$RUNCOMMAND" ]] || { echo "no-await-on-bridge: MISCONFIGURED — $RUNCOMMAND not found" >&2; exit 1; }

while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "writeStdin-returns-promise: $line"
done < <(grep -nE 'writeStdin\([^)]*\)[[:space:]]*:[[:space:]]*(Promise|Thenable)' "$RUNCOMMAND" 2>/dev/null || true)

# Positive assertion: writeStdin must be declared returning void, AND at least one
# actual send call must exist in the scanned set (otherwise the "clean" result could
# be vacuous — the send surface renamed away and the guard scanning nothing).
if ! grep -qE 'writeStdin\([^)]*\)[[:space:]]*:[[:space:]]*void' "$RUNCOMMAND"; then
  report "writeStdin-not-void: $RUNCOMMAND does not declare writeStdin(...): void — the TS→Go send must be fire-and-forget"
fi

SEND_CALL_COUNT=$(grep -arlE "($SEND_FNS)\(" --include="*.ts" --include="*.tsx" "$SRC_DIR" 2>/dev/null | wc -l | tr -d '[:space:]')
if [[ "$SEND_CALL_COUNT" -eq 0 ]]; then
  report "no-send-calls-found: none of ($SEND_FNS) appear anywhere under $SRC_DIR — the send surface names likely changed; update SEND_FNS or this guard is scanning nothing"
fi

if [[ $HITS -eq 0 ]]; then
  echo "no-await-on-bridge: clean (TS→Go send is fire-and-forget; no await/Promise/request-response on the bridge)"
  exit 0
fi

echo ""
echo "no-await-on-bridge: $HITS hit(s) — the TS→Go send path must be fire-and-forget (no await, no Promise chain, no request/response)"
exit 1
