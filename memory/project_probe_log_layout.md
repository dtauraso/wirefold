---
name: project-probe-log-layout
description: Runtime logs land in five .probe/ JSONL files (go/go-errors/go-debug/ts/ts-errors) with a shared ts_ms+src+step envelope; probe-merge.sh derives unified views
metadata:
  type: project
---

Editor/runtime diagnostics are written to five files under `.probe/` — `go.jsonl` (buffer-decoded trace events, src:"buf"), `go-errors.jsonl` (Go failures from stderr, src:"go"), `go-debug.jsonl` (Go DEBUG BREADCRUMB channel, src:"go-debug"), `ts.jsonl` (webview+ext logs, src:"ts-webview"/"ts-ext"), `ts-errors.jsonl` (window/unhandled/render errors).

**The breadcrumb RULE — where to call `tr.Breadcrumb`, why not `fmt.Fprintf(os.Stderr, ...)`, and keep-it-SPARSE — lives in CLAUDE.md's "Debugging the Go layer" section**, which loads every session. It is deliberately not restated here: this file previously carried a second copy of the routing mechanics, so a change to `tryParseBreadcrumb` or the sink wiring needed two edits and would silently half-rot. This file covers only what CLAUDE.md does not: the file layout, the envelope, and the freshness trap.

**Envelope.** Every line carries `{ts_ms, src, step?}` — `ts_ms` is `Date.now()` wall-clock (cross-process comparable on one machine), `step` is the Go event ordinal, present only on Go-derived lines. Go's `marshalEvent`/canonical form is untouched (contract fixture `trace-events.jsonl` pins it); the envelope is added extension-side at the disk-write boundary.

**Reading across files.** `tools/probe-merge.sh` (no-arg = all by `ts_ms`; `--errors`, `--step N`, `--go`, `--ts`, `--debug`). Retired filenames: `phase4-pump.jsonl`→`go.jsonl`, `webview-log.jsonl`→split.

**Freshness caveat (the trap).** These files are written by the LIVE editor run and can be minutes stale — check the last `ts_ms` against now before concluding anything. Several diagnoses were derailed by reading a stale log that did not contain the live failure.
