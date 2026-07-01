#!/usr/bin/env bash
set -euo pipefail

# check-eslint.sh — lint guard for the webview/extension TypeScript.
#
# Runs ESLint (flat config: typescript-eslint + eslint-plugin-react-hooks) over
# tools/topology-vscode/src. The ruleset is scoped to correctness — the hook
# rules (`react-hooks/rules-of-hooks`, `react-hooks/exhaustive-deps`) are ERROR,
# catching the stale-closure / missing-dep bug class; stylistic rules are off so
# this is a bug guard, not a restyle. Pre-existing deliberate `eslint-disable`
# comments stay honored. FAILS (nonzero) on any lint error; exit 0 when clean.

cd "$(git rev-parse --show-toplevel)/tools/topology-vscode"

npx --no-install eslint src
