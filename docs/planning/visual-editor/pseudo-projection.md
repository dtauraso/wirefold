---
branch: task/pseudo-projection-input
---

# Pseudo projection — Input node v0

## Goal
A button on Input nodes opens a side panel showing an algebraic pseudo view of the node's Go logic. The pseudo view is editable; saving round-trips back to `nodes/Input/node.go` via a Go tool that parses and regenerates the file. Other node kinds are deferred to per-kind follow-up branches.

## Level of projection
NOT a syntactic formatter. The pseudo is **algebraic** — one gate-equation per node. Substrate ceremony (ctx loop, TrySend, field declarations, package/imports/types, `init` registration) is hidden in the AST and restored on save. The user sees the primitive and the channel names; the language is invisible.

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
- Everything else (ctx loop, TrySend, Fire, index/wrap arithmetic) is hidden ceremony, restored by `ToGo`.

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

The shell is parameterized by the Init literal values, the output field name (`ToReadGate`), and the `Repeat` bool (surface-signalled by `repeatedly`). Everything else is fixed ceremony restored by `ToGo`.

## Open questions

1. **Multiple output wires (resolved):** `FromGo` asserts the Input struct has exactly one `*Wiring.Out` field. More than one is a parse error. If a real second output is added later, the grammar will be extended deliberately (e.g. comma-separated target list) with a new round-trip test.

## Round-trip mechanism
- `tools/pseudo/` Go package, `go/ast` + `go/printer`.
- `pseudo.FromGo(file) → Equation` — pattern-matches the Input canonical shell in the AST to extract (`InitValues`, `RepeatFlag`, `OutputField`).
- `pseudo.ToGo(Equation, file) → file` — expands the pseudo form back into the canonical shell, preserving hidden AST (struct fields, imports, `init` registration).
- Round-trip test: parse `nodes/Input/node.go` → `FromGo` → `ToGo` → `gofmt` → byte-identical to original `gofmt` output.

## v0 task shape
(a) Codify the Input shell template in `tools/pseudo/input.go` with the Init values, output field name, and Repeat flag as parameters.
(b) Implement `FromGo` / `ToGo` for the Input pseudo form + round-trip test on `nodes/Input/node.go`.
(c) Add button to Input nodes in `GenericNode` + host bridge to read/write `nodes/Input/node.go`; side panel renders the pseudo form; save calls `ToGo` and writes the file.

Steps (a) and (b) are pure Go, no editor surface. Step (c) is the UI branch.

## Non-goals
- Covering ReadGate, InhibitRightGate, or ChainInhibitor in this branch.
- LLM-based translation. The Input pseudo mapping is a deterministic template.
- Arbitrary Go. Only the Input canonical shell pattern.
- Cross-kind refactoring through pseudo.
