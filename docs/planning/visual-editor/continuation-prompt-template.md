# Continuation prompt template

The current handoff lives at
[handoff.md](handoff.md) in this directory. Any AI starting a fresh
session reads that file — not chat history — to pick up state. This
template is the *schema* for handoff.md: when an invariant changes,
edit the template; the next session re-renders handoff.md from it.

Stable invariants (branch hygiene, cwd note, friction-logging rule)
live in the template so they don't have to be rediscovered each
session.

---

Continuing on wirefold, branch {{branch-name}}.

State at handoff:
  Local + origin/{{branch-name}} in sync at {{short-sha}}.
  npm test → {{pass}}/{{total}} pass ({{breakdown}}).
  npm run check:loc → {{clean | offenders: ...}}.
  Working tree: {{clean | files modified — note pre-existing vs new}}.

{{Optional: per-branch decision summary — what was chosen on this
branch and why, e.g. substrate choice, pattern adopted. Omit if the
branch is a straightforward continuation.}}

Open branches (pushed, unmerged):
  {{branch}} — {{one-line state}}.
  {{branch}} — {{one-line state}}.

Next options (each justified against "what did the rest of the world converge on"):
1. {{option — scope, blockers, sign-off needs}}
2. {{option}}
3. {{option}}
4. {{option}}

Branch hygiene: no merge to main without explicit sign-off. Delete merged branches without re-asking. Force-push needs sign-off.
Cwd for tsc/tests/check:loc/build: tools/topology-vscode/ (Bash resets cwd — chain cd or use absolute paths).
If user surfaces unrelated friction, log to docs/planning/visual-editor/session-log.md and open a fresh task/<short-kebab>.

ALWAYS — at end of session, overwrite docs/planning/visual-editor/handoff.md with a freshly-rendered prompt tailored to the state you're leaving the branch in, and commit it on the task branch. Do not rely on chat history; the next AI may be a fresh model with no transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop is self-perpetuating across sessions. Use this template (continuation-prompt-template.md) as the structural source of truth; update the template when an invariant changes.
