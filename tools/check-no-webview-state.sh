#!/usr/bin/env bash
set -euo pipefail

# check-no-webview-state.sh — content-buffer erase guard.
#
# Asserts the webview holds NO domain state of its own: the model lives entirely in Go and
# the TS/React layer is render + forward only (MODEL.md "Editor surface"). After the
# agnostic-content-buffer erase, all node/edge/pulse/geometry/camera state is Go-owned and
# streamed as the binary content buffer; TS decodes and draws it. This guard FAILS if a
# webview file reintroduces a state store:
#
#   1. A Zustand store — `import ... from "zustand"` / `create(...)`. Zustand stores were the
#      home of the old render/camera/spec state; none may return to the webview.
#   2. A stateful domain hook — `useSyncExternalStore` — outside the tiny buffer-reflect
#      resources below. Those three REFLECT Go (decode the latest snapshot / hold the row→id
#      table); they author no domain state, so they are allowed.
#
# Allowed buffer-reflect resources (they mirror Go, they do not author state):
#   - snapshot-buffer.ts   (holds the latest binary snapshot + subscribe)
#   - overlay-flags.ts     (decodes the buffer Overlay columns via useSyncExternalStore)
#   - buffer-nav.ts        (row-keyed id/label table decoded from the buffer)
#
# Exit 0 when clean.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

WEBVIEW_DIR="$REPO_ROOT/tools/topology-vscode/src/webview"

if [[ ! -d "$WEBVIEW_DIR" ]]; then
  echo "no-webview-state: MISCONFIGURED — webview dir not found at $WEBVIEW_DIR" >&2
  exit 1
fi

HITS=0
report() {
  printf '%s\n' "$1"
  HITS=$((HITS + 1))
}

# 1. No Zustand import anywhere in the webview.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "zustand-import: $line  (Zustand store in the webview — domain state must live in Go)"
done < <(grep -arnE 'from[[:space:]]+"zustand"' \
  --include="*.ts" --include="*.tsx" "$WEBVIEW_DIR" 2>/dev/null || true)

# 2. No Zustand-style `create<...>(` / `create((` store constructor (defensive even if the
#    import were aliased). Matches `create<Foo>(` and `create((set` forms.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "zustand-create: $line  (store constructor in the webview — domain state must live in Go)"
done < <(grep -arnE '\bcreate[<(]' \
  --include="*.ts" --include="*.tsx" "$WEBVIEW_DIR" 2>/dev/null || true)

# 3. useSyncExternalStore only in the allowed buffer-reflect resources. Anywhere else it is a
#    stateful domain hook and is forbidden.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  f="${line%%:*}"
  base="$(basename "$f")"
  case "$base" in
    snapshot-buffer.ts|overlay-flags.ts|buffer-nav.ts) continue ;;
  esac
  report "domain-hook: $line  (useSyncExternalStore outside the allowed buffer-reflect resources)"
done < <(grep -arnE '\buseSyncExternalStore\b' \
  --include="*.ts" --include="*.tsx" "$WEBVIEW_DIR" 2>/dev/null || true)

# 4. No useReducer anywhere. A reducer is a domain state MACHINE (dispatched actions
#    mutating held state) — exactly the "author domain state on the TS side" the model
#    forbids. There is no legitimate render-local use, so this is an unconditional ban
#    (a plain tripwire: none exists today, so any first one is a deliberate regression).
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  report "reducer: $line  (useReducer in the webview — a domain state machine must live in Go)"
done < <(grep -arnE '\buseReducer\b' \
  --include="*.ts" --include="*.tsx" "$WEBVIEW_DIR" 2>/dev/null || true)

# 5. createContext only in the allowed render-infra files. A React Context is a common
#    vector for a smuggled domain store (a provider holding node/edge/camera values read
#    across the tree). The ONLY sanctioned use is render-infra plumbing that authors no
#    domain data — today scene-env.tsx's EnvTexContext (a THREE.Texture handle). Anywhere
#    else it is presumed a domain store and forbidden; add a file here only for genuine
#    render-infra context, never for domain values.
while IFS= read -r line; do
  [[ -z "$line" ]] && continue
  f="${line%%:*}"
  base="$(basename "$f")"
  case "$base" in
    scene-env.tsx) continue ;;
  esac
  report "context: $line  (createContext outside the allowed render-infra files — domain state must live in Go)"
done < <(grep -arnE '\bcreateContext\b' \
  --include="*.ts" --include="*.tsx" "$WEBVIEW_DIR" 2>/dev/null || true)

if [[ $HITS -eq 0 ]]; then
  echo "no-webview-state: clean (webview holds no domain state; render + forward only, Go owns the model)"
  exit 0
fi

echo ""
echo "no-webview-state: $HITS hit(s) — the webview must hold no domain state (no Zustand store, no stateful domain hook); the model lives in Go and streams as the binary content buffer"
exit 1
