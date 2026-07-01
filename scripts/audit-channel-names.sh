#!/usr/bin/env bash
# Audit 14: channel naming convention.
# Flags channel names that don't encode endpoints (per CLAUDE.md).
# Generic single-word names are the failure mode — ch1, done, signal, out, ack alone.
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1

if [ ! -d nodes ]; then
  echo "channel-naming: MISCONFIGURED — scan dir not found: nodes/ (run from repo root)" >&2
  exit 1
fi

bad_pattern='^(ch[0-9]*|done|signal|out|in|ack|sig|tmp|x|c|ch)$'
fail=0

while IFS= read -r line; do
  file=$(echo "$line" | cut -d: -f1)
  match=$(echo "$line" | cut -d: -f2-)
  # The convention is about node-connecting WIRE channels. Internal control/sync
  # channels (ack signals) and local accessor vars are legitimately not endpoint-
  # named; mark such a line with a trailing `// chan-name-ok: <reason>` to exempt it.
  if echo "$match" | command grep -q 'chan-name-ok'; then continue; fi
  name=$(echo "$match" | sed -E 's/.*[[:space:]]([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*:?=[[:space:]]*make\(chan.*/\1/')
  if echo "$name" | grep -qE "$bad_pattern"; then
    echo "channel-naming: $file: generic name '$name' (encode endpoints per CLAUDE.md)"
    fail=1
  fi
done < <(grep -arnE --include='*.go' --exclude='*_test.go' '[A-Za-z_][A-Za-z0-9_]*[[:space:]]*:?=[[:space:]]*make\(chan' nodes/ 2>/dev/null)

exit $fail
