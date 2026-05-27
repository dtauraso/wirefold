#!/usr/bin/env bash
# PreToolUse(Bash) approval guard.
# Auto-approves a Bash command UNLESS it contains a destructive OR network
# operation, in which case it falls through to normal manual approval ("ask").
# The scan is over the FULL command string, so a flagged sub-command anywhere
# in a compound command (a && b ; c | d) triggers "ask".
#
# Tune by editing the arrays below (extended regex, grep -E). The '>' overwrite
# matcher is the most aggressive destructive pattern — comment it out to reduce prompts.
set -uo pipefail

input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"

emit() { # $1=allow|ask  $2=reason
  jq -nc --arg d "$1" --arg r "$2" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:$d,permissionDecisionReason:$r}}'
}

if [ -z "$cmd" ]; then emit allow "no command string"; exit 0; fi

DESTRUCTIVE_PATTERNS=(
  '(^|[^[:alnum:]_./-])rm([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])rmdir([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])shred([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])dd([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])mkfs'
  '(^|[^[:alnum:]_./-])truncate([[:space:]]|$)'
  '[[:space:]]-delete([[:space:]]|$)'
  'git[[:space:]]+clean'
  '[-][-]hard'
  'git[[:space:]]+branch[[:space:]].*-[Dd]([[:space:]]|$)'
  'git[[:space:]]+tag[[:space:]].*-d([[:space:]]|$)'
  '[-][-]force(-with-lease)?([[:space:]]|=|$)'
  '(^|[^>&0-9])>([^>]|$)'
)

NETWORK_PATTERNS=(
  '(^|[^[:alnum:]_./-])curl([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])wget([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])nc([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])netcat([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])telnet([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])s?ftp([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])scp([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])rsync([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])ssh([[:space:]]|$)'
  'https?://'
  'ftp://'
  'git[[:space:]]+(push|pull|fetch|clone|remote|ls-remote)'
  '(npm|pnpm|yarn)[[:space:]]+(install|ci|add|publish|i)([[:space:]]|$)'
  'pip[0-9]?[[:space:]]+install'
  '(^|[^[:alnum:]_./-])(brew|apt|apt-get|gh|aws|gcloud)([[:space:]]|$)'
  'go[[:space:]]+(get|install|mod[[:space:]]+(download|tidy))'
  'docker[[:space:]]+(pull|push)'
  'cargo[[:space:]]+(install|add|update|fetch|publish)'
  'gem[[:space:]]+install'
)

for pat in "${DESTRUCTIVE_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then emit ask "destructive pattern matched"; exit 0; fi
done
for pat in "${NETWORK_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then emit ask "network pattern matched"; exit 0; fi
done
emit allow "no destructive or network pattern matched"
exit 0
