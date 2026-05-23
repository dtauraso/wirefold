---
branch: task/pseudo-projection-input
---

# Pseudo projection — Input node v0

## Goal
A button on Input nodes opens a side panel showing an algebraic pseudo view of the node's Go logic. The pseudo view is editable. The pseudo is a **per-instance** view — it is opened from a specific Input node on the canvas and reflects that node's individual configuration. Saving routes each edit to whichever file holds the original token: values and the `repeatedly` flag write to `topology.json` (per-instance spec data exposed via `wire:"..."` struct tags); channel names and the logic shape write to `nodes/Input/node.go` (per-kind source). Other node kinds are deferred to per-kind follow-up branches.

## Level of projection
NOT a syntactic formatter. The pseudo is **algebraic** — one gate-equation per node. The pseudo view is **per-instance**: it is opened from a specific node on the canvas, reads that node's spec entry from `topology.json` plus the kind's Go source from `nodes/Input/node.go`. Substrate ceremony (ctx loop, TrySend, field declarations, package/imports/types, `init` registration) is hidden in the AST and restored on save. The user sees the primitive and the channel names; the language is invisible.

## v0 scope: Input only
The original v0 plan covered all 4 node kinds. That scope is reduced: this branch handles **Input only**. ReadGate, InhibitRightGate, and ChainInhibitor each get their own follow-up task branches once Input is proven end-to-end. Per-kind branches are cheaper to review, scope-creep less, and keep the round-trip test surface small.

## Input node pseudo form

Current `nodes/Input/node.go` in full:

```go
package input

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire       func()
	Init       []int `wire:"data.init"`
	Repeat     bool  `wire:"data.repeat"`
	ToReadGate *Wiring.Out
}

func (n *Node) Update(ctx context.Context) {
	for i := 0; n.Repeat || i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		if len(n.Init) == 0 {
			return
		}
		n.Fire()
		if n.ToReadGate.TrySend(n.Init[i%len(n.Init)]) {
			i++
			if !n.Repeat && i >= len(n.Init) {
				return
			}
		}
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
```

**Pseudo form:**

```
// Repeat=false
send each of [0, 1] to ToReadGate

// Repeat=true
repeatedly send each of [0, 1] to ToReadGate
```

Rules:

- The `Init` array is rendered as a literal in the pseudo (e.g. `[0, 1]`), not behind the field name `Init`.
- The output channel uses the Go field name (e.g. `ToReadGate`) verbatim.
- The leading word `repeatedly` is the only surface signal for the `Repeat` bool. Absent → `Repeat=false`; present → `Repeat=true`.
- Everything else (ctx loop, TrySend, Fire, index/wrap arithmetic) is hidden ceremony, restored by `ToInput`.

**Canonical Go shell that the pseudo form expands into:**

```go
func (n *Node) Update(ctx context.Context) {
	for i := 0; n.Repeat || i < len(n.Init); {
		if ctx.Err() != nil {
			return
		}
		if len(n.Init) == 0 {
			return
		}
		n.Fire()
		if n.ToReadGate.TrySend(n.Init[i%len(n.Init)]) {
			i++
			if !n.Repeat && i >= len(n.Init) {
				return
			}
		}
	}
}
```

The shell is parameterized by the Init literal values, the output field name (`ToReadGate`), and the `Repeat` bool (surface-signalled by `repeatedly`). Everything else is fixed ceremony restored by `ToInput`.

## Round-trip mechanism
- `tools/pseudo/` Go package, `go/ast` + `go/printer` + `encoding/json`.
- `pseudo.FromInput(goSrc []byte, specEntry map[string]any) → InputView` reads both sources. `InputView` carries each pseudo token with an origin tag (`OriginGo` or `OriginSpec`).
- `pseudo.RenderInput(view) → string` produces the pseudo text.
- `pseudo.ParseInput(text, view) → InputView` parses edits, preserving origin tags by position against the prior view.
- `pseudo.ToInput(view) → (newGoSrc, newSpecEntry)` writes back to each source per origin tag; unchanged sources are byte-identical (post-gofmt for Go).
- Round-trip tests:
  - (a) Go file + spec entry → `FromInput` → `RenderInput` → `ParseInput` → `ToInput` → byte-identical Go and byte-identical spec JSON.
  - (b) Edit only spec-origin tokens; ensure Go output is byte-identical to original.
  - (c) Edit only Go-origin tokens; ensure spec JSON is byte-identical.

## v0 task shape
(a) Codify the Input shell template in `tools/pseudo/input.go` parameterized by `OutputField` (Go-origin) and `InitValues`, `Repeat` (spec-origin).
(b) Implement `FromInput` / `ParseInput` / `RenderInput` / `ToInput` with the three round-trip tests above.
(c) Editor button on Input nodes opens a side panel; bridge reads `nodes/Input/node.go` + the node's spec entry from `topology.json`; save dispatches per-origin to the two files.

Steps (a) and (b) are pure Go, no editor surface. Step (c) is the UI branch.

## Non-goals
- Covering ReadGate, InhibitRightGate, or ChainInhibitor in this branch.
- LLM-based translation. The Input pseudo mapping is a deterministic template.
- Arbitrary Go. Only the Input canonical shell pattern.
- Cross-kind refactoring through pseudo.
- Single-file round-trip. Save always routes tokens to the correct source (spec vs. kind Go file) per origin tag.

## Open questions

1. **Multiple output wires (resolved):** `FromInput` asserts the Input struct has exactly one `*Wiring.Out` field. More than one is a parse error. If a real second output is added later, the grammar will be extended deliberately (e.g. comma-separated target list) with a new round-trip test.
2. **Unknown Go field reference:** if the pseudo text references a field name that does not exist in the kind's Go struct, `ParseInput` returns a parse error — no partial save.
