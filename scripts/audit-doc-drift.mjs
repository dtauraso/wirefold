#!/usr/bin/env node
// Audit 11 (mechanical slice): flag broken file/path references in docs.
// Stale narrative claims stay AI-driven — this only catches paths that
// no longer exist.
import { readFileSync, existsSync } from "node:fs";
import { execSync } from "node:child_process";
import { join, dirname } from "node:path";

const root = execSync("git rev-parse --show-toplevel").toString().trim();
// docs/planning/** is skipped whole: CLAUDE.md defines it as historical-by-construction
// (branch-local docs plus the durable session-log, itself a dated log of past sessions).
// A ref to a since-deleted file there is a record of what happened, not a stale current
// claim — check-doc-symbols.sh applies the same exemption for the same reason.
const files = execSync(
  `git ls-files '*.md' ':!:**/node_modules/**' ':!:docs/planning/**'`,
  { cwd: root },
).toString().trim().split("\n");

const linkRe = /\]\(([^)#?]+)(?:[#?][^)]*)?\)/g;
// Only flag backtick refs that look like real paths (contain "/"); bare
// filenames like `topology.json` are documentation shorthand, not paths.
const inlineRe = /`([^`\s]*\/[^`\s]+\.(?:md|ts|tsx|go|json|svg|sh|mjs|yml|yaml))`/g;

// A line marking its own ref as history is exempt — same rationale and keyword set as
// check-doc-symbols.sh's HISTORY_RE: naming a deleted path to say "this is gone" is the
// valuable case, not the bug class this guard exists to catch.
const HISTORY_RE = /(gone|removed|retired|deleted|erased|obsolete|legacy|superseded|replaced|no longer|used to|formerly|was |were |since deleted|do not re-|don.t re-|never existed|no such|there is no)/i;

let fail = 0;
for (const file of files) {
  const abs = join(root, file);
  const text = readFileSync(abs, "utf8");
  const lines = text.split("\n");
  const lineOf = (idx) => {
    let acc = 0;
    for (let i = 0; i < lines.length; i++) {
      acc += lines[i].length + 1;
      if (idx < acc) return lines[i];
    }
    return "";
  };
  const checks = [...text.matchAll(linkRe), ...text.matchAll(inlineRe)];
  for (const m of checks) {
    const ref = m[1];
    if (/^https?:\/\//.test(ref) || ref.startsWith("mailto:")) continue;
    if (/[*<>{}]/.test(ref)) continue; // glob/template, not a literal path
    if (HISTORY_RE.test(lineOf(m.index))) continue; // marked as history, not a live claim
    // This repo's convention is to write cross-file refs root-relative
    // (e.g. `nodes/Wiring/foo.go` from any doc, not just repo-root docs),
    // so try repo-root resolution before falling back to doc-relative.
    const rootCandidate = join(root, ref);
    const relCandidate = join(dirname(abs), ref);
    if (!existsSync(rootCandidate) && !existsSync(relCandidate)) {
      console.log(`doc-drift: ${file}: broken reference '${ref}'`);
      fail = 1;
    }
  }
}
process.exit(fail);
