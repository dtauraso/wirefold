#!/usr/bin/env python3
"""UserPromptSubmit hook: nudge main session to delegate executor-style work.

Fires only when the prompt contains keywords that suggest multi-step
lookup / mechanical edit work.

DO NOT re-add a CLAUDE.md citation here. This hook cited CLAUDE.md
"Model routing" from 24de543c (2026-05-13) until c123b83e (2026-06-16)
removed that Delegation doctrine and raised force-delegate's threshold
1->8 -- a deliberate softening. The hook kept citing the deleted section
for a month, so it was asserting retired doctrine as live rule, including
"Main session never writes", which no longer holds. A hook that cites a
doc is only as true as the doc; this one states its own advice instead,
so deleting a section cannot silently turn it into a liar.
tools/check-doc-citations.sh now fails the build on such a citation.

Model tiers (haiku/sonnet) are deliberately NOT named: that table went
with the removed doctrine. Choose per task -- judgment work wants the
default model.
"""
import json
import re
import sys

PATTERN = re.compile(
    r"\b(audit|sweep|refactor|rename|grep|scan)\b|find all|search the|check all|go through",
    re.IGNORECASE,
)

MESSAGE = (
    "Heads up: this prompt looks like executor-style work. Consider delegating: "
    "read-only sweeps to an Explore subagent, scoped mechanical edits to the "
    "implementer subagent (NOT general-purpose — implementer has no Agent tool, "
    "so it cannot spawn nested agents). Judge it on the merits: a single targeted "
    "lookup with a known path is cheaper inline, and judgment-heavy work wants the "
    "default model. This is a nudge, not a rule — CLAUDE.md has no delegation "
    "doctrine, and the main session writing code is fine."
)

try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

prompt = data.get("prompt", "")
if PATTERN.search(prompt):
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "UserPromptSubmit",
            "additionalContext": MESSAGE,
        }
    }))
