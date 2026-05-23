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

## Input node primitive: EMIT

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

**Proposed pseudo equation:**

```
EMIT(Init, repeat=Repeat) → ToReadGate
```

- `EMIT` names the primitive: drain the `Init` sequence by sending each value to the output wire.
- `repeat=Repeat` is a boolean modifier — when true the index wraps and the loop runs forever; when false the node exits after the last element.
- `ToReadGate` is the Go field name of the output wire; trivially round-trippable.

**Canonical Go shell that EMIT expands into:**

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

The shell is parameterized by two names (`Init`, `ToReadGate`) and one boolean flag name (`Repeat`). Everything else is fixed ceremony restored by `ToGo`.

## Open questions

1. **How are `Init` values exposed in pseudo?** The `Init` field is a `[]int` from spec (`wire:"data.init"`). The pseudo equation currently refers to the field by name, not its literal values. Should the panel also show/edit the actual array contents (`EMIT([1,2,3], repeat=false) → ToReadGate`), or is `Init` an opaque reference and array editing stays in the node properties panel?

2. **Multiple output wires.** The current Input node has exactly one output (`ToReadGate`). If a future topology wires a second output, the EMIT primitive would need to name it. v0 assumes exactly one output; the question is whether to assert-error or silently ignore extras during `FromGo`.

3. **`repeat=false` verbosity.** When `Repeat` is false (the non-repeating case) the equation could be abbreviated to `EMIT(Init) → ToReadGate` with repeat omitted as a default. Explicit vs. implicit default is a display preference — pick one for v0 and make it consistent.

4. **`Fire()` call.** `n.Fire()` is called before every TrySend. It is substrate ceremony (triggers the visual pulse indicator) and should stay invisible in pseudo. Confirm it is always present in the EMIT shell and never varies; if so, hide it unconditionally.

## Round-trip mechanism
- `tools/pseudo/` Go package, `go/ast` + `go/printer`.
- `pseudo.FromGo(file) → Equation` — pattern-matches the EMIT shell in the AST to extract (`InitField`, `RepeatField`, `OutputField`).
- `pseudo.ToGo(Equation, file) → file` — expands equation back into the canonical shell, preserving hidden AST (struct fields, imports, `init` registration).
- Round-trip test: parse `nodes/Input/node.go` → `FromGo` → `ToGo` → `gofmt` → byte-identical to original `gofmt` output.

## v0 task shape
(a) Codify the EMIT shell template in `tools/pseudo/emit.go` with the three variable names as parameters.
(b) Implement `FromGo` / `ToGo` for EMIT + round-trip test on `nodes/Input/node.go`.
(c) Add button to Input nodes in `GenericNode` + host bridge to read/write `nodes/Input/node.go`; side panel renders the equation; save calls `ToGo` and writes the file.

Steps (a) and (b) are pure Go, no editor surface. Step (c) is the UI branch.

## Non-goals
- Covering ReadGate, InhibitRightGate, or ChainInhibitor in this branch.
- LLM-based translation. The EMIT mapping is a deterministic template.
- Arbitrary Go. Only the EMIT shell pattern.
- Cross-kind refactoring through pseudo.
