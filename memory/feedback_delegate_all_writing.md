---
name: feedback-delegate-all-writing
description: Strict — main session never writes code/docs/scripts directly; all writing is delegated. Main session only outputs prose, decisions, and dispatch prompts.
metadata:
  type: feedback
---

**Rule (strict):** Main session never calls `Write`, `Edit`, or `Bash` for editing/scripting. Every code change, doc edit, script run, file restore, memory write, or handoff update goes through a subagent.

Allowed in main session:
- Short prose responses to the user.
- Decisions and recommendations.
- Dispatch prompts (the Agent tool call).
- Reading the user's named file/path when they explicitly request it.

Not allowed in main session:
- `Edit` / `Write` of any file (code, docs, memory, handoff, topology.json).
- `Bash` for anything beyond a single read-only check the user named.
- Running scripts, even one-liners.
- Hand-editing memory or handoff.md.

**Why:** Even small inline edits and "just one script" calls add up. Earlier sessions paid for restoration scripts, memory writes, and CLAUDE.md edits in main when each could have been a subagent. User said "you stopped delegating" — this rule removes the judgment call.

**How to apply:** Before any non-read tool call, ask "is this writing?" If yes, dispatch. The inline-executor counter is no longer the trigger — *any* write is the trigger.

Supersedes / tightens: [[feedback-delegate-executor-work]], [[feedback-delegate-by-default]].
