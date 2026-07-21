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
const mdFiles = execSync(
  `git ls-files '*.md' ':!:**/node_modules/**' ':!:docs/planning/**'`,
  { cwd: root },
).toString().trim().split("\n");

// Non-doc source files: comments in these can also cite a doc path (e.g. "see
// the visual-editor planning doc for why") with no backticks/markdown-link
// syntax at all, so they need a different, source-shaped regex (below) rather
// than the markdown link/inline-code regexes. docs/planning/** itself is
// exempt for the same historical-by-construction reason as above; generated
// files are exempt because their path mentions are about themselves (e.g. a
// header comment naming the file it was generated from/into), not citations.
//
// File set: `.go`/`.ts`/`.tsx` (the original class) plus `.gitignore`, `*.sh`,
// `*.mjs`, `*.yml`/`*.yaml` — all comment-bearing non-markdown files where a
// stray doc citation can rot the same way (this guard's own
// gap: a `.gitignore` comment cited a stripped branch-local doc and stayed
// green because `.gitignore` wasn't scanned at all). `*.json` is deliberately
// excluded: JSON has no comment syntax, so a doc-path-shaped string there is
// data, not documentation prose, and would be a false-positive source, not a
// citation to keep in sync. `Makefile` is included by name for the same
// reason as `.gitignore` (no extension to glob on) even though none exists in
// this repo today.
const srcFiles = execSync(
  `git ls-files '*.go' '*.ts' '*.tsx' '*.sh' '*.mjs' '*.yml' '*.yaml' '.gitignore' 'Makefile' '**/.gitignore' '**/Makefile' ':!:**/node_modules/**' ':!:docs/planning/**' ':!:**/*_generated.go' ':!:**/out/**'`,
  { cwd: root },
).toString().trim().split("\n");

const linkRe = /\]\(([^)#?]+)(?:[#?][^)]*)?\)/g;
// Only flag backtick refs that look like real paths (contain "/"); bare
// filenames like `topology.json` are documentation shorthand, not paths.
const inlineRe = /`([^`\s]*\/[^`\s]+\.(?:md|ts|tsx|go|json|svg|sh|mjs|yml|yaml))`/g;

// Source-file (non-markdown) ref regex: deliberately narrow, and deliberately
// documentation-only (.md/.html), NOT general code-path references. Go/TS
// comments have no backtick/link convention, so a broad "any repo-relative
// path" regex was tried first and it lit up dozens of false positives: tests
// build throwaway on-disk fixtures under paths like `nodes/y/meta.json` or
// `nodes/self/inputs/In.json` (single-letter/short fixture node ids, not real
// package dirs) purely as prose describing what the test writes, and those
// aren't citations to anything — there is nothing to keep in sync. Anchoring
// on `docs/`, `MODEL.md`, or `CLAUDE.md` plus a `.md`/`.html` extension
// targets exactly the bug class this guard exists for (a comment citing a
// planning/architecture doc that no longer exists) without also policing
// arbitrary code-path prose.
const srcRefRe =
  /\b(docs\/[A-Za-z0-9_./-]+\.(?:md|html)|MODEL\.md|CLAUDE\.md)\b/g;

// A line marking its own ref as history is exempt — same rationale and keyword set as
// check-doc-symbols.sh's HISTORY_RE: naming a deleted path to say "this is gone" is the
// valuable case, not the bug class this guard exists to catch.
const HISTORY_RE = /(gone|removed|retired|deleted|erased|obsolete|legacy|superseded|replaced|no longer|used to|formerly|was |were |since deleted|do not re-|don.t re-|never existed|no such|there is no)/i;

let fail = 0;

function lineOfFactory(text) {
  const lines = text.split("\n");
  return (idx) => {
    let acc = 0;
    for (let i = 0; i < lines.length; i++) {
      acc += lines[i].length + 1;
      if (idx < acc) return lines[i];
    }
    return "";
  };
}

function checkRef(file, abs, ref, idx, lineOf) {
  if (/^https?:\/\//.test(ref) || ref.startsWith("mailto:")) return;
  if (/[*<>{}]/.test(ref)) return; // glob/template, not a literal path
  if (HISTORY_RE.test(lineOf(idx))) return; // marked as history, not a live claim
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

for (const file of mdFiles) {
  const abs = join(root, file);
  if (!existsSync(abs)) continue; // unstaged deletion — see the srcFiles loop below
  const text = readFileSync(abs, "utf8");
  const lineOf = lineOfFactory(text);
  const checks = [...text.matchAll(linkRe), ...text.matchAll(inlineRe)];
  for (const m of checks) checkRef(file, abs, m[1], m.index, lineOf);
}

for (const file of srcFiles) {
  if (!file) continue;
  const abs = join(root, file);
  // git ls-files reads the INDEX, so a file deleted in the working tree but not yet
  // staged is still listed. Skip it rather than dying on ENOENT: an audit that crashes
  // on an in-progress deletion reports a guard failure for the wrong reason.
  if (!existsSync(abs)) continue;
  const text = readFileSync(abs, "utf8");
  const lineOf = lineOfFactory(text);
  for (const m of text.matchAll(srcRefRe)) {
    checkRef(file, abs, m[1], m.index, lineOf);
  }
}

process.exit(fail);
