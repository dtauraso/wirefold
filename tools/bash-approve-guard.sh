#!/usr/bin/env bash
# PreToolUse(Bash) approval guard. Three tiers, evaluated in order over the
# FULL command string (so a flagged sub-command anywhere in a compound command
# a && b ; c | d triggers the tier):
#   CATASTROPHIC -> deny        (hard block; no prompt — edit this file to ever run it)
#   DESTRUCTIVE  -> pass-through (no output; Claude Code's native prompt handles it,
#                                 so a "yes, and don't ask again" choice persists)
#   NETWORK      -> pass-through (same — native prompt; remembered allows work)
#   otherwise    -> allow       (silent)
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
  # git merge: CLAUDE.md's Workflow section names "merging a task branch into main"
  # FIRST in its list of actions that still require sign-off. Until this line, that rule
  # was prose the code contradicted: merge matched no tier, fell through to
  # `otherwise -> allow`, and was SILENTLY AUTO-APPROVED — the guard actively answered
  # the question the doc says to ask. --force and branch -D were gated; the one action
  # named first was not.
  # Trailing ([[:space:]]|$) is load-bearing: it keeps read-only `git merge-base` out
  # (after "merge" comes "-", not a space), while still catching `git merge --no-ff x`.
  'git[[:space:]]+merge([[:space:]]|$)'
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
  'git[[:space:]]+(clone|ls-remote)'
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
for pat in "${DESTRUCTIVE_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then exit 0; fi
done
for pat in "${NETWORK_PATTERNS[@]}"; do
  if printf '%s' "$cmd" | grep -Eq "$pat"; then exit 0; fi
done
emit allow "no flagged pattern matched"
exit 0
