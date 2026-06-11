---
name: discriminated-union-grep
description: When adding a new variant to a discriminated-union pipeline (trace events, message kinds, etc.), grep every existing variant name to find all allowlists/gates before declaring done
metadata:
  type: feedback
---

When adding a new variant (e.g. a new event kind) to a discriminated-union pipeline, grep every existing variant's name across the codebase before declaring the change done. There are usually hidden allowlists or gates that hardcode the known variants.

**Why:** 2026-05-21 slot-trace landing. Added a `slot` event to the Go→webview trace pipeline. Touched the 7 files the design sketch named (Trace.go, two node kinds, messages.ts, pump.ts, GenericNode.tsx). Missed `runCommand.ts:18` — a hardcoded `kind === "recv" || "fire" || "send"` gate in the extension host that silently dropped slot events into the VS Code output channel instead of forwarding them to the webview. Cost a full diagnostic pass to find.

**How to apply:** Before declaring done on a new-variant change, grep each existing variant name (e.g. `grep -rn '"recv"' src/`) and inspect every hit. Any allowlist, switch, type guard, or string-equality check that enumerates the known set must learn the new variant too. Design sketches naming "the files to edit" are not exhaustive — derive the allowlist set from a grep, not from the sketch.

[[feedback_audit_invariant_call_sites_first]] is the related rule for the Go side.
