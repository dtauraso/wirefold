---
name: feedback-no-nested-agents
description: Delegate edit/implement work to the project 'implementer' agent (no Agent tool), not 'general-purpose' (Tools:*), so subagents can't recursively spawn nested agents
metadata:
  type: feedback
---

Delegated edit/implementation work must go to the project **`implementer`** subagent
(`.claude/agents/implementer.md`), NOT `general-purpose`.

**Why:** `general-purpose` has `Tools: *`, which includes the `Agent` tool, so each
delegated agent can recursively spawn its own subagents — observed 2026-06-27 as a
`general-purpose → general-purpose → general-purpose` nested tree (~20 agent files for
~5 explicit spawns). `implementer`'s toolset omits `Agent`, so recursion is structurally
impossible (the bug class is unrepresentable, cf. [[feedback-make-bug-class-unrepresentable]]),
rather than relying on a prompt plea.

**How to apply:** for write/refactor/fix tasks use `subagent_type: "implementer"`. For
read-only research use `Explore` (already excludes `Agent`). Reserve `general-purpose`
only when a task genuinely needs to fan out to further subagents (rare; decide consciously).
Still spot-check delegated commits ([[feedback-verify-subagent-commits]]).
