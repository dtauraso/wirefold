#!/usr/bin/env python3
"""PreToolUse(Bash) hook: block `open` of an .html file.

Rationale: HTML artifacts should render inside VS Code (Live Preview), not the
system browser. The terminal cannot trigger Live Preview's pane directly, but it
CAN reliably keep HTML out of the browser by forcing `code <file>` instead of
`open <file>`. Memory/preference is discipline; this hook is enforcement.

Denies only when the command invokes the `open` binary on a target ending in
.html (optionally quoted, with #fragment/?query). Everything else passes through.
"""
import json
import re
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)  # not our concern; don't block on malformed input

cmd = (data.get("tool_input") or {}).get("command", "")

# `open` as a command word (start of line or after a shell separator), then an
# argument that resolves to a .html file. Keep it narrow: macOS `open` opening a
# page, not e.g. `openssl` or a flag like `--open`.
OPEN_HTML = re.compile(
    r"""(?:^|[;&|]|&&|\|\|)\s*open\b      # the open command word
        (?:\s+-[^\s]+)*                   # any leading flags (-a, -g, ...)
        \s+['"]?[^'"]*\.html(?:[?#][^'"\s]*)?['"]?""",
    re.VERBOSE,
)

if OPEN_HTML.search(cmd):
    msg = (
        "Blocked: don't `open` HTML in the browser. Use `code \"<file>\"` so it "
        "opens in VS Code, then render it with the Live Preview icon (or cmd+k v). "
        "Re-run with `code` instead of `open`."
    )
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": "deny",
            "permissionDecisionReason": msg,
        }
    }))

sys.exit(0)
