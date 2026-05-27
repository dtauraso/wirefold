#!/usr/bin/env bash
# PreToolUse(Bash) approval guard.
# Auto-approves a Bash command UNLESS it contains a destructive operation,
# in which case it falls through to normal manual approval ("ask").
# The scan is over the FULL command string, so a destructive sub-command
# anywhere in a compound command (a && b ; c | d) triggers "ask".
#
# Tune by editing DESTRUCTIVE_PATTERNS (extended regex, grep -E). The final
# '>' overwrite matcher is the most aggressive — comment it out to reduce prompts.
set -uo pipefail

input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"

emit() { # $1=allow|ask  $2=reason
  jq -nc --arg d "$1" --arg r "$2" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:$d,permissionDecisionReason:$r}}'
}

if [ -z "$cmd" ]; then emit allow "no command string"; exit 0; fi

DESTRUCTIVE_PATTERNS=(
  '(^|[^[:alnum:]_./-])rm([[:space:]]|$)'                   # rm
  '(^|[^[:alnum:]_./-])rmdir([[:space:]]|$)'                # rmdir
  '(^|[^[:alnum:]_./-])shred([[:space:]]|$)'                # shred
  '(^|[^[:alnum:]_./-])dd([[:space:]]|$)'                   # dd
  '(^|[^[:alnum:]_./-])mkfs'                                 # mkfs*
  '(^|[^[:alnum:]_./-])truncate([[:space:]]|$)'             # truncate
  '[[:space:]]-delete([[:space:]]|$)'                       # find ... -delete
  'git[[:space:]]+push'                                      # git push (incl. force)
  'git[[:space:]]+clean'                                     # git clean
  '[-][-]hard([[:space:]]|$)'                               # git reset --hard
  'git[[:space:]]+branch[[:space:]].*-[Dd]([[:space:]]|$)'   # git branch -d/-D
  'git[[:space:]]+tag[[:space:]].*-d([[:space:]]|$)'         # git tag -d
  '[-][-]force(-with-lease)?([[:space:]]|=|$)'               # --force / --force-with-lease
  '(^|[^>&0-9])>([^>]|$)'                                    # single > overwrite redirect (aggressive)
)

for pat in "${DESTRUCTIVE_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then
    emit ask "destructive pattern matched"
    exit 0
  fi
done
emit allow "no destructive pattern matched"
exit 0
