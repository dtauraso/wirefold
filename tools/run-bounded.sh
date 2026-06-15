#!/usr/bin/env bash
# run-bounded.sh — run a command with a hard wall-clock limit.
#
# macOS has no coreutils `timeout`, and the wirefold sim (and anything that
# parks goroutines on a halted clock / paced wire) can fail to exit on its own.
# A foreground call to such a command blocks until the agent/harness limit —
# this is exactly what hung a subagent for 13 minutes. ALWAYS wrap a
# potentially-blocking run in this helper instead of calling it directly.
#
# Usage:
#   tools/run-bounded.sh <seconds> <command> [args...]
#
# Example (capture the sim's startup trace without risking a hang):
#   tools/run-bounded.sh 5 ./wirefold -topology ./topology -trace /tmp/t.jsonl -duration 1s </dev/null
#
# Exits with the command's own status if it finishes in time, or 124 if it was
# killed at the deadline (matching GNU `timeout`'s convention).

set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: run-bounded.sh <seconds> <command> [args...]" >&2
  exit 2
fi

limit="$1"; shift

# perl's alarm delivers SIGALRM after <limit> seconds; exec replaces the shell so
# signals hit the real command. If alarm fires, perl exits 124.
perl -e '
  my $limit = shift;
  my $pid = fork();
  if ($pid == 0) { exec @ARGV or exit 127; }
  $SIG{ALRM} = sub { kill "TERM", $pid; sleep 1; kill "KILL", $pid; exit 124; };
  alarm $limit;
  waitpid($pid, 0);
  exit($? >> 8);
' "$limit" "$@"
