---
name: feedback-subagent-discovery-mandate
description: Give subagents a grep-first discovery mandate, not a curated reading list, or codebase-wide integration points get missed
metadata:
  type: feedback
---

When dispatching subagents for a multi-part feature, prompts that say "READ FIRST: X, Y, Z then edit" make agents fast but blinkered — they execute assumptions instead of verifying them against the codebase. Read/edit executes the known; grep/find discovers the unknown. A heavy read+edit / light grep+find ratio is the signal.

**Why:** On the 3D editor build, six narrow-view agents each edited ThreeView.tsx from a curated file list. Result: intra-file tangle (caught by a read-the-whole-file audit) AND a codebase-wide integration gap that only a grep fan-out found — adding `z` to the schema plumbed load+render but no agent added a save path, because none was pointed at the save side. Reading named files can never find a call that doesn't exist; grep can.

**How to apply:** For new features with many parts, FRONT-LOAD a grep-first discovery sweep across the integration surface (find all readers/writers/call-sites of the model being changed) BEFORE writing. In edit prompts, give a discovery mandate ("grep every site that reads/writes X; confirm round-trip") rather than only a reading list. Relates to [[feedback_verify_subagent_commits]] and [[feedback_schema_parser_parity]].
