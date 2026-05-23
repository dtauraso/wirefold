#!/usr/bin/env bash
# Fast deterministic checks for the Stop hook. Skips slow test suites.
# Returns nonzero (and prints why) if anything fails.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 0

changed=$(git status --porcelain 2>/dev/null | awk '{print $NF}')
[ -z "$changed" ] && exit 0

go_changed=$(echo "$changed" | grep -E '\.go$' || true)
ts_changed=$(echo "$changed" | grep -E 'tools/topology-vscode/.*\.(ts|tsx)$' || true)

fail=0
out=""

if [ -n "$go_changed" ]; then
  if ! go_out=$(go build ./... 2>&1); then
    out+="go build failed:\n$go_out\n\n"
    fail=1
  fi
fi

if [ -n "$ts_changed" ]; then
  if ! tsc_out=$(cd tools/topology-vscode && npx --no-install tsc --noEmit 2>&1); then
    out+="tsc --noEmit failed:\n$tsc_out\n\n"
    fail=1
  fi
  # Rebuild the webview/extension bundle so Cmd-R in the host picks up
  # the latest TS changes without a manual `npm run build`. Skip when
  # out/webview.js is already newer than every changed TS file — avoids
  # paying full esbuild cost on no-op or pure-test edits.
  webview_out="tools/topology-vscode/out/webview.js"
  # Test files don't enter the bundle; skip build when only test/ changed.
  bundle_ts_changed=$(echo "$ts_changed" | grep -v 'tools/topology-vscode/test/' || true)
  need_build=0
  if [ -n "$bundle_ts_changed" ]; then
    need_build=1
    if [ -f "$webview_out" ]; then
      out_mtime=$(stat -f %m "$webview_out" 2>/dev/null || stat -c %Y "$webview_out" 2>/dev/null || echo 0)
      newer=0
      while IFS= read -r f; do
        [ -z "$f" ] && continue
        [ ! -f "$f" ] && continue
        f_mtime=$(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo 0)
        if [ "$f_mtime" -gt "$out_mtime" ]; then newer=1; break; fi
      done <<< "$bundle_ts_changed"
      [ "$newer" -eq 0 ] && need_build=0
    fi
  fi
  if [ "$need_build" -eq 1 ]; then
    if ! build_out=$(cd tools/topology-vscode && npm run --silent build 2>&1); then
      out+="webview build failed:\n$build_out\n\n"
      fail=1
    fi
  fi
fi

if [ $fail -ne 0 ]; then
  python3 -c "
import json, sys
reason = 'Pre-stop checks failed. Fix before stopping:\n\n' + sys.stdin.read()
print(json.dumps({'decision': 'block', 'reason': reason}))
" <<< "$(printf '%b' "$out")"
  exit 0
fi
exit 0
