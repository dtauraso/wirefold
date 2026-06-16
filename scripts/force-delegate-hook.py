#!/usr/bin/env python3
"""PreToolUse hook: hard-block executor-style work after a threshold.

Counts Read / Grep / Glob calls plus Bash calls whose command starts
with a search verb (grep, rg, find, ls, cat, head, tail, awk, sed).
Threshold is 1 -- blocks on the 2nd qualifying call.
The Task/Agent tool resets the counter.
"""
import json
import os
import re
import sys

THRESHOLD = 8  # block on the (THRESHOLD+1)th call
SEARCH_VERBS = re.compile(r"^\s*(grep|rg|find|ls|cat|head|tail|awk|sed)\b")

def counter_path(session_id: str) -> str:
    safe = re.sub(r"[^A-Za-z0-9_-]", "_", session_id or "default")
    return f"/tmp/claude-delegate-{safe}.count"

def read_count(path: str) -> int:
    try:
        with open(path) as f:
            return int(f.read().strip() or "0")
    except Exception:
        return 0

def write_count(path: str, n: int) -> None:
    try:
        with open(path, "w") as f:
            f.write(str(n))
    except Exception:
        pass

def main() -> int:
    try:
        data = json.load(sys.stdin)
    except Exception:
        return 0

    # Exempt subagent sessions. The hook exists to push the main
    # (Opus) session to delegate; once inside a subagent, the
    # subagent IS the delegation and needs to do executor work
    # freely. Claude Code sets agent_id / agent_type on tool calls
    # made inside a subagent invocation (session_id is shared with
    # the parent, so it can't be used as the exemption signal).
    if data.get("agent_id") or data.get("agent_type"):
        return 0

    tool = data.get("tool_name", "")
    session_id = data.get("session_id", "default")
    path = counter_path(session_id)

    # Reset on subagent spawn.
    if tool in ("Task", "Agent"):
        write_count(path, 0)
        return 0

    is_search = tool in ("Read", "Grep", "Glob")
    if tool == "Bash":
        cmd = (data.get("tool_input") or {}).get("command", "")
        if SEARCH_VERBS.match(cmd):
            is_search = True

    if not is_search:
        return 0

    n = read_count(path) + 1
    write_count(path, n)

    if n > THRESHOLD:
        msg = (
            f"Delegate this. You've made {n} executor-style lookups inline. "
            "Spawn an Agent subagent: model='haiku' with subagent_type='Explore' "
            "for research, or model='sonnet' general-purpose for mechanical edits. "
            "Counter resets when you spawn the Agent. If you genuinely need one "
            "more inline lookup (e.g. reading a file the user just named), say "
            "so in chat -- the user can clear /tmp/claude-delegate-*.count."
        )
        print(json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "deny",
                "permissionDecisionReason": msg,
            }
        }))
    return 0

if __name__ == "__main__":
    sys.exit(main())
