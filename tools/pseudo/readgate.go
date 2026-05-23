package pseudo

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// ReadGateView is the parsed representation of a ReadGate node instance.
// It holds only the tokens needed to render pseudo text; no spec data is
// required for render-only (parse/save is a later step).
type ReadGateView struct {
	ValueInput  string // fixed "value"  — the value input port name
	SignalInput string // fixed "signal" — the signal/chain-inhibitor input port name
	OutNeighbor string // downstream node id, supplied by caller from topology
}

// FromReadGate parses the Go source of nodes/readgate/node.go to confirm the
// gate has the expected guard (HasValue && HasChainInhibitor) and the
// ToChainInhibitor send, then returns a ReadGateView.
//
// outNeighbor is resolved from topology by the caller and is not derived from Go.
func FromReadGate(goSrc []byte, outNeighbor string) (ReadGateView, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, 0)
	if err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: parse go source: %w", err)
	}

	if err := verifyReadGateGuard(f); err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: %w", err)
	}

	if err := verifyToChainInhibitorSend(f); err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: %w", err)
	}

	return ReadGateView{
		ValueInput:  "value",
		SignalInput: "signal",
		OutNeighbor: outNeighbor,
	}, nil
}

// RenderReadGate emits the human-readable pseudo text for a ReadGateView.
// Format:
//
//	if value and signal
//	   send value -> edge to <OutNeighbor>
func RenderReadGate(v ReadGateView) string {
	var b strings.Builder
	b.WriteString("if value and signal\n")
	b.WriteString("   send value -> edge to ")
	b.WriteString(v.OutNeighbor)
	b.WriteString("\n")
	return b.String()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// verifyReadGateGuard checks that the Update method body contains an
// if-statement guarded by HasValue && HasChainInhibitor.
func verifyReadGateGuard(f *ast.File) error {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if isBinaryAnd(ifStmt.Cond, "HasValue", "HasChainInhibitor") {
			found = true
		}
		return true
	})
	if !found {
		return fmt.Errorf("Update method missing expected guard: HasValue && HasChainInhibitor")
	}
	return nil
}

// verifyToChainInhibitorSend checks that the Update method calls
// ToChainInhibitor.TrySend somewhere.
func verifyToChainInhibitorSend(f *ast.File) error {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		xSel, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if xSel.Sel.Name == "ToChainInhibitor" && sel.Sel.Name == "TrySend" {
			found = true
		}
		return true
	})
	if !found {
		return fmt.Errorf("Update method missing expected ToChainInhibitor.TrySend call")
	}
	return nil
}

// isBinaryAnd reports whether expr is (X.leftField && X.rightField) for any
// receiver X, or bare (leftIdent && rightIdent).
func isBinaryAnd(expr ast.Expr, left, right string) bool {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op.String() != "&&" {
		return false
	}
	return selectorOrIdent(bin.X, left) && selectorOrIdent(bin.Y, right)
}

// selectorOrIdent reports whether expr is either a bare identifier with name,
// or a selector expression whose field name matches name (e.g. g.HasValue).
func selectorOrIdent(expr ast.Expr, name string) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name == name
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == name
	}
	return false
}
