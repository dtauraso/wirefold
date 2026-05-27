#!/usr/bin/env bash
# PreToolUse(Bash) approval guard. Three tiers, evaluated in order over the
# FULL command string (so a flagged sub-command anywhere in a compound command
# a && b ; c | d triggers the tier):
#   CATASTROPHIC -> deny  (hard block; no approval prompt — edit this file to run)
#   DESTRUCTIVE  -> ask   (manual approval prompt)
#   NETWORK      -> ask   (manual approval prompt)
#   otherwise    -> allow (runs silently)
# Patterns are extended-regex (grep -E). Avoid leading "--" (BSD grep treats it
# as a flag) — use [-][-] instead. The '>' overwrite matcher in DESTRUCTIVE is
# the most aggressive; comment it out to reduce prompts.
set -uo pipefail

input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"

emit() { # $1=allow|ask|deny  $2=reason
  jq -nc --arg d "$1" --arg r "$2" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:$d,permissionDecisionReason:$r}}'
}

if [ -z "$cmd" ]; then emit allow "no command string"; exit 0; fi

CATASTROPHIC_PATTERNS=(
  'no-preserve-root'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+/([[:space:]]|$)'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+/[*]'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+~([[:space:]/]|$)'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+[$]HOME'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+[*]([[:space:]]|$)'
  'rm[[:space:]]+-[a-zA-Z]*[rf][a-zA-Z]*[[:space:]]+[.][[:space:]]*$'
  '(^|[^[:alnum:]_./-])mkfs'
  '(^|[^[:alnum:]_./-])(fdisk|parted|wipefs)([[:space:]]|$)'
  'dd[[:space:]].*[[:space:]]of=/dev/'
  '>[[:space:]]*/dev/(sd|nvme|disk|hd|vd|mmcblk)'
  ':[[:space:]]*\(\)[[:space:]]*\{[[:space:]]*:[[:space:]]*\|'
  '(curl|wget)[[:space:]].*\|[[:space:]]*(sudo[[:space:]]+)?[a-z]*sh([[:space:]]|$)'
  '(chmod|chown)[[:space:]]+-[a-zA-Z]*R[a-zA-Z]*.*[[:space:]]/([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])(shutdown|reboot|halt|poweroff)([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])init[[:space:]]+[06]([[:space:]]|$)'
  'crontab[[:space:]]+-r([[:space:]]|$)'
  'find[[:space:]]+/[[:space:]].*-delete'
)

DESTRUCTIVE_PATTERNS=(
  '(^|[^[:alnum:]_./-])rm([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])rmdir([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])shred([[:space:]]|$)'
  '(^|[^[:alnum:]_./-])dd([[:space:]]|$)'
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

for pat in "${CATASTROPHIC_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then emit deny "catastrophic command blocked"; exit 0; fi
done
# Strip benign /dev/null redirects before destructive check so "> /dev/null" doesn't trigger.
cmd_safe="$(printf '%s' "$cmd" | sed 's|>[[:space:]]*/dev/null||g')"
for pat in "${DESTRUCTIVE_PATTERNS[@]}"; do
  if printf '%s' "$cmd_safe" | grep -Eq "$pat"; then emit ask "destructive pattern matched"; exit 0; fi
done
for pat in "${NETWORK_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then emit ask "network pattern matched"; exit 0; fi
done
emit allow "no flagged pattern matched"
exit 0
