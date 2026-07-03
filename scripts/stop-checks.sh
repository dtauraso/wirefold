#!/usr/bin/env bash
# Fast deterministic checks for the Stop hook. Skips slow test suites.
# Returns nonzero (and prints why) if anything fails.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 0

# Files to consider for the EXPENSIVE language builds. This is the union of:
#   - the working tree (uncommitted changes), and
#   - committed-but-unmerged work (branch ahead of origin/main, or main if no origin).
# The guard LOOP below runs unconditionally regardless of this set — it is fast
# (sub-second greps) and is the real enforcement; gating it on a dirty tree made
# the whole suite decorative for the normal commit-then-stop workflow.
worktree_changed=$(git status --porcelain 2>/dev/null | awk '{print $NF}')

base=""
if git rev-parse --verify -q origin/main >/dev/null 2>&1; then
  base="origin/main"
elif git rev-parse --verify -q main >/dev/null 2>&1; then
  base="main"
fi
committed_changed=""
if [ -n "$base" ]; then
  committed_changed=$(git diff --name-only "$base"...HEAD 2>/dev/null || true)
fi

changed=$(printf '%s\n%s\n' "$worktree_changed" "$committed_changed")

go_changed=$(echo "$changed" | grep -E '\.go$' || true)
ts_changed=$(echo "$changed" | grep -E 'tools/topology-vscode/.*\.(ts|tsx)$' || true)

fail=0
out=""

if [ -n "$go_changed" ]; then
  if ! go_out=$(go build ./... 2>&1); then
    out+="go build failed:\n$go_out\n\n"
    fail=1
  fi
  # go test — fast/cached here (~0.2s), so run it on the same gate as go build
  # (Go changed / branch ahead of origin/main). This was the one verify step
  # living outside the suite.
  if ! gotest_out=$(go test ./... 2>&1); then
    out+="go test failed:\n$gotest_out\n\n"
    fail=1
  fi
  # go vet + staticcheck. staticcheck COMPILES the whole module, so it is
  # expensive — it lives here in the go-gated block (Go changed / branch ahead of
  # origin/main), never in the fast unconditional guard loop below.
  if ! sc_out=$(bash tools/check-staticcheck.sh 2>&1); then
    out+="check-staticcheck failed:\n$sc_out\n\n"
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
  # ESLint (react-hooks correctness guard) — TS-only, needs node_modules, so it
  # lives in the ts_changed block alongside tsc rather than the generic loop.
  if ! eslint_out=$(bash tools/check-eslint.sh 2>&1); then
    out+="check-eslint failed:\n$eslint_out\n\n"
    fail=1
  fi
  # Vitest unit suite (trace-event field contracts, parseSpec, round-trips) —
  # compiles + runs the tests, so EXPENSIVE; lives in the ts_changed block, not
  # the fast unconditional guard loop.
  if ! vitest_out=$(bash tools/check-vitest.sh 2>&1); then
    out+="check-vitest failed:\n$vitest_out\n\n"
    fail=1
  fi
fi

for chk in check-message-kind-parity check-edit-op-parity check-bridge-literal-parity check-input-layout-parity check-generated check-no-camera-roundtrip check-polar-only-nav check-no-await-on-bridge check-ts-computes-no-geometry check-ts-shading-from-go check-send-rule-parity check-gofmt check-buffer-layout-parity check-no-webview-state; do
  if ! chk_out=$(bash "tools/$chk.sh" 2>&1); then
    out+="$chk failed:\n$chk_out\n\n"
    fail=1
  fi
done

if ! chk_out=$(bash "scripts/check-dead-doc-tokens.sh" 2>&1); then
  out+="check-dead-doc-tokens failed:\n$chk_out\n\n"
  fail=1
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
