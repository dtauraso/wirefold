#!/usr/bin/env bash
# check-no-foreground-sim.sh — PreToolUse(Bash) guard.
#
# Enforces the rule in memory/feedback_no_foreground_sim_runs.md: the wirefold sim
# (and anything parked on a halted clock / paced wire) can fail to exit on its own,
# and macOS has no `timeout`, so a FOREGROUND run blocks the Bash call until the
# harness limit — the exact failure that once hung a subagent for 13 minutes.
#
# A sim run is allowed ONLY when it is either:
#   1. backgrounded by the harness  (tool_input.run_in_background == true), or
#   2. wrapped in tools/run-bounded.sh  (hard wall-clock cap), or
#   3. backgrounded in-shell with a trailing `&`.
# Otherwise the run is foreground-unbounded and is DENIED with a fix hint.
#
# Scope is deliberately narrow: it fires only on invocations of the sim binary
# (./wirefold) or `go run` of the repo's main package. Everything else is allowed
# silently. Exit 0 always; the decision is carried in the emitted JSON.
set -uo pipefail

input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"
bg="$(printf '%s' "$input" | jq -r '.tool_input.run_in_background // false')"

emit() { # $1=allow|deny  $2=reason
  jq -nc --arg d "$1" --arg r "$2" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:$d,permissionDecisionReason:$r}}'
}

if [ -z "$cmd" ]; then emit allow "no command string"; exit 0; fi

# Does the command invoke the sim? (./wirefold, bare wirefold binary, or `go run` of
# the repo MAIN package specifically). Narrow on purpose — `go test`, `go build`,
# editing, and `go run` of a subpackage tool (e.g. `go run ./tools/gen-node-defs`)
# are exempt. The main package is the module root, so only `go run .`, `go run ./`,
# or `go run github.com/dtauraso/wirefold` count as sim runs.
SIM_RE='(^|[^[:alnum:]_./-])(\./)?wirefold([[:space:]]|$)|go[[:space:]]+run[[:space:]]+(\./?|github\.com/dtauraso/wirefold)([[:space:]]|$)'
if ! printf '%s' "$cmd" | grep -Eq "$SIM_RE"; then
  emit allow "not a sim run"; exit 0
fi

# Allowed escape hatches.
if [ "$bg" = "true" ]; then emit allow "sim run is harness-backgrounded"; exit 0; fi
if printf '%s' "$cmd" | grep -Eq 'run-bounded\.sh'; then emit allow "sim run is bounded"; exit 0; fi
if printf '%s' "$cmd" | grep -Eq '&[[:space:]]*$'; then emit allow "sim run is shell-backgrounded"; exit 0; fi

emit deny "Foreground sim run blocked (memory/feedback_no_foreground_sim_runs.md): the sim can fail to exit and will hang the Bash call. Re-run backgrounded (run_in_background=true), or wrap it: tools/run-bounded.sh <seconds> <cmd…>."
exit 0
