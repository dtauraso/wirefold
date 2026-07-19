#!/usr/bin/env bash
set -euo pipefail

# check-no-nul-bytes.sh — guard against literal NUL (0x00) bytes in tracked
# source files.
#
# History: overlay-flags.ts was authored with the two-character escape
# sequence `\0` intended (e.g. inside a template/string like
# `names.join("\0")`) but a literal 0x00 byte landed in the file instead.
# Consequences: git treated the file as BINARY (diffs showed `Bin X -> Y`
# instead of a real line diff), `grep` went silent on it, and every guard
# that greps source was blinded to its contents. It still compiled and every
# check passed — the whole verify suite was green on a binary source file.
# The NULs shipped to main in commit 338f05da before anyone noticed.
#
# This guard makes that class impossible to ship again: it scans every
# TRACKED source file (driven by `git ls-files`, so untracked/gitignored
# noise like node_modules/out/.git never enters the scan) for a literal
# 0x00 byte and fails, naming the file and the byte offset, if it finds one.
#
# Exit 0 when clean, exit 1 (with a report) when any tracked source file
# contains a NUL byte.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Source-file extensions we care about. Genuinely binary tracked files (images,
# fonts, etc.) are excluded by extension, not by an ad hoc path list, so a new
# binary asset doesn't need this script edited.
INCLUDE_EXT_RE='\.(go|ts|tsx|js|jsx|json|md|sh|css)$'

HITS=0
report() {
  printf '%s\n' "$1"
  HITS=$((HITS + 1))
}

while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  [[ -f "$f" ]] || continue
  # Byte-scan via python3 rather than grep: grep implementations differ wildly
  # across platforms in how -P/-U/-a/-o interact with a literal NUL byte
  # (verified: ugrep on macOS silently matched the whole line instead of just
  # the NUL when asked for $'\x00'). A direct byte scan has no such ambiguity.
  hit=$(python3 -c "
import sys
data = open(sys.argv[1], 'rb').read()
idx = data.find(b'\\x00')
if idx == -1:
    sys.exit(0)
line_no = data.count(b'\\n', 0, idx) + 1
print(f'{idx}:{line_no}')
" "$f" 2>/dev/null || true)
  [[ -z "$hit" ]] && continue
  byte_off="${hit%%:*}"
  line_no="${hit##*:}"
  report "nul-byte: $f: byte offset $byte_off (line $line_no)"
done < <(git ls-files | grep -E "$INCLUDE_EXT_RE" || true)

if [[ $HITS -eq 0 ]]; then
  echo "no-nul-bytes: clean (no literal NUL bytes in tracked source files)"
  exit 0
fi

echo ""
echo "no-nul-bytes: $HITS hit(s) — literal 0x00 byte(s) found in tracked source; this silently turns the file BINARY to git (diffs show Bin X -> Y, grep goes silent, all grep-based guards are blinded). Almost always a stray '\\0' escape that landed as a real byte instead of two characters. Fix by replacing the literal NUL with the intended escape sequence."
exit 1
