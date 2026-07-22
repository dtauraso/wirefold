#!/usr/bin/env bash
# verify.sh — the terminal/agent front door to the repo's verify checks.
#
# It runs the SAME checks as the Stop hook (scripts/stop-checks.sh) but in --cli mode, so it
# prints the failure reason and EXITS NONZERO on failure. This is the one to run by hand:
#
#   bash scripts/verify.sh   →  exit 0 = clean, nonzero = something failed (reason on stderr)
#
# It is a thin wrapper on purpose — there is exactly ONE copy of the checks (in
# stop-checks.sh), so the terminal path and the hook path can never drift. Do NOT copy
# checks here. The hook keeps its JSON-on-stdout/exit-0 contract; this flips only the
# failure SIGNAL to a conventional nonzero exit.
#
# Why this exists: stop-checks.sh must exit 0 for the Stop-hook protocol, which makes
# `bash scripts/stop-checks.sh && echo ok` (or a `$?` check) report green on a red tree.
# Running verify.sh makes the reflexive habit correct.
set -u
exec bash "$(dirname "$0")/stop-checks.sh" --cli "$@"
