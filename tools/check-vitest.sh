#!/usr/bin/env bash
set -euo pipefail

# check-vitest.sh — unit-test guard for the webview/extension TypeScript.
#
# Runs the vitest suite (`npm test` → `vitest run`, non-interactive) over
# tools/topology-vscode. This suite includes the cross-language trace-event
# field contracts, parseSpec/round-trip tests, and topology data-path checks —
# a green suite is the only thing that catches a Go json-tag rename or a
# fixture drifting away from TRACE_EVENT_KINDS. It compiles + runs the tests,
# so it is EXPENSIVE and belongs in the TS-gated block of stop-checks, never
# the fast unconditional guard loop. FAILS (nonzero) on any test failure;
# exit 0 when the whole suite passes.

cd "$(git rev-parse --show-toplevel)/tools/topology-vscode"

npm test
