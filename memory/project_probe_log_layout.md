---
name: project-probe-log-layout
description: Runtime logs land in four .probe/ JSONL files (go/go-errors/ts/ts-errors) with a shared ts_ms+src+step envelope; probe-merge.sh derives unified views
metadata:
  type: project
---

Editor/runtime diagnostics are written to four files under `.probe/` — `go.jsonl` (Go trace relayed from Go stdout, src:"go"), `go-errors.jsonl` (Go failures), `ts.jsonl` (webview+ext logs, src:"ts-webview"/"ts-ext"), `ts-errors.jsonl` (window/unhandled/render errors). Every line carries an envelope `{ts_ms, src, step?}` — `ts_ms` is Date.now() wall-clock (cross-process comparable on one machine), `step` is the Go event ordinal present only on Go-derived lines. Go's `marshalEvent`/canonical form is untouched (contract fixture `trace-events.jsonl` pins it); the envelope is added extension-side at the disk-write boundary. To read across files use `tools/probe-merge.sh` (no-arg=all by ts_ms; `--errors`, `--step N`, `--go`, `--ts`). Retired filenames: `phase4-pump.jsonl`→`go.jsonl`, `webview-log.jsonl`→split. Landed on branch task/logs-ai-readable.
