package pseudo

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"text/template"
)

// Source of truth for port-handle names in logic; the emit templates below must mirror these.
const (
	portFromInput          = "FromInput"
	portFromChainInhibitor = "FromChainInhibitor"
	portToChainInhibitor   = "ToChainInhibitor"
)

// ReadGateView is the parsed representation of a ReadGate node instance.
//
// GuardTerms holds the named inputs required before the gate fires. The
// canonical two-term form is ["input value", "signal"]; dropping the signal term
// to ["input value"] produces a value-only gate that fires without a chain-inhibitor.
// The first term is always the data-bearing term ("input value"); the optional second
// is the chain-inhibitor/signal term.
//
// ReadGate is AND-only: both guard terms must be present before the gate fires.
type ReadGateView struct {
	GuardTerms  []string // 1 or 2 named guard terms; index 0 = value term
	OutNeighbor string   // downstream node id, supplied by caller from topology
}

// valueTerm returns the first guard term (always the value term).
func (v ReadGateView) valueTerm() string {
	if len(v.GuardTerms) > 0 {
		return v.GuardTerms[0]
	}
	return "input value"
}

// signalTerm returns the second guard term, or "" if not present.
func (v ReadGateView) signalTerm() string {
	if len(v.GuardTerms) > 1 {
		return v.GuardTerms[1]
	}
	return ""
}

// gateWord returns "and" — ReadGate is AND-only.
func (v ReadGateView) gateWord() string {
	return "and"
}

// FromReadGate parses the Go source of nodes/readgate/node.go to derive the
// guard shape (1 or 2 terms), then returns a ReadGateView.
//
// It accepts either:
//   - HasValue && HasChainInhibitor  → GuardTerms = ["value", "signal"]
//   - HasValue alone                 → GuardTerms = ["value"]
//
// outNeighbor is resolved from topology by the caller and is not derived from Go.
func FromReadGate(goSrc []byte, outNeighbor string) (ReadGateView, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, 0)
	if err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: parse go source: %w", err)
	}

	guardTerms, err := detectReadGateGuard(f)
	if err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: %w", err)
	}

	if err := verifyToChainInhibitorSend(f); err != nil {
		return ReadGateView{}, fmt.Errorf("pseudo.FromReadGate: %w", err)
	}

	return ReadGateView{
		GuardTerms:  guardTerms,
		OutNeighbor: outNeighbor,
	}, nil
}

// RenderReadGate emits the human-readable pseudo text for a ReadGateView.
//
// Two-term form:
//
//	if input value and signal
//	   input value -> <OutNeighbor>
//
// One-term form:
//
//	if input value
//	   input value -> <OutNeighbor>
func RenderReadGate(v ReadGateView) string {
	var b strings.Builder
	b.WriteString("if ")
	b.WriteString(v.valueTerm())
	if sig := v.signalTerm(); sig != "" {
		b.WriteString(" ")
		b.WriteString(v.gateWord())
		b.WriteString(" ")
		b.WriteString(sig)
	}
	b.WriteString("\n")
	b.WriteString("   ")
	b.WriteString(v.valueTerm())
	b.WriteString(" -> ")
	b.WriteString(v.OutNeighbor)
	b.WriteString("\n")
	return b.String()
}

// ParseReadGateError is the error type returned by ParseReadGate on malformed input.
type ParseReadGateError struct {
	msg        string
	cause      error
	suggestion string
}

func (e *ParseReadGateError) Error() string      { return e.msg }
func (e *ParseReadGateError) Unwrap() error      { return e.cause }
func (e *ParseReadGateError) Suggestion() string { return e.suggestion }

// buildReadGateSuggestion builds the canonical suggestion string from a prior view.
func buildReadGateSuggestion(prior ReadGateView) string {
	neighbor := prior.OutNeighbor
	if neighbor == "" {
		neighbor = "<node>"
	}
	if prior.signalTerm() != "" {
		return fmt.Sprintf("Try: if %s %s %s\n   %s -> %s",
			prior.valueTerm(), prior.gateWord(), prior.signalTerm(), prior.valueTerm(), neighbor)
	}
	return fmt.Sprintf("Try: if %s\n   %s -> %s",
		prior.valueTerm(), prior.valueTerm(), neighbor)
}

// ParseReadGate parses edited pseudo text back into a ReadGateView.
//
// Grammar (whitespace-insensitive across lines):
//
//	pseudo   := "if" "input" "value" ["and" ident] NEWLINE "input" "value" "->" ident
//
// "or" is rejected — ReadGate is AND-only.
//
// On malformed input returns *ParseReadGateError with a human message and Suggestion().
func ParseReadGate(text string, prior ReadGateView) (ReadGateView, error) {
	p := &pseudoParser{input: strings.TrimSpace(text)}
	v, err := p.parseReadGatePseudo()
	if err != nil {
		var pe *parseError
		if isParseError(err, &pe) {
			return ReadGateView{}, &ParseReadGateError{
				msg:        pe.humanMessage(),
				cause:      err,
				suggestion: buildReadGateSuggestion(prior),
			}
		}
		return ReadGateView{}, &ParseReadGateError{
			msg:        err.Error(),
			cause:      err,
			suggestion: buildReadGateSuggestion(prior),
		}
	}
	return v, nil
}

// ToReadGate regenerates nodes/readgate/node.go to match the guard described by v.
// The struct shape follows the guard: 2-term guard keeps HasChainInhibitor and
// FromChainInhibitor; 1-term guard omits them entirely (no dead ports).
//
// Returns the new Go source, the new output-neighbor name, and removedPorts: the
// struct field names dropped vs. the full 2-term shape (["FromChainInhibitor"] for
// a 1-term guard, empty for 2-term).
func ToReadGate(v ReadGateView) (newGoSrc []byte, newOutNeighbor string, removedPorts []string, err error) {
	hasSignal := v.signalTerm() != ""

	// Compute removedPorts: fields present in the full 2-term shape but absent here.
	if !hasSignal {
		removedPorts = []string{portFromChainInhibitor}
	}

	type templateData struct {
		HasSignal bool
	}

	const updateTemplate = `
func (g *Node) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !g.HasValue {
			if v, ok := g.FromInput.TryRecv(); ok {
				g.Value = v
				g.HasValue = true
			}
		}
{{- if .HasSignal}}

		if !g.HasChainInhibitor {
			if _, ok := g.FromChainInhibitor.TryRecv(); ok {
				g.HasChainInhibitor = true
			}
		}

		if g.HasValue && g.HasChainInhibitor {
			g.Fire()
			g.FromInput.Done()
			g.FromChainInhibitor.Done()
			g.HasValue = false
			g.HasChainInhibitor = false
			g.ToChainInhibitor.TrySend(g.Value)
		}
{{- else}}

		if g.HasValue {
			g.Fire()
			g.FromInput.Done()
			g.HasValue = false
			g.ToChainInhibitor.TrySend(g.Value)
		}
{{- end}}
	}
}
`
	tmpl, tmplErr := template.New("update").Parse(updateTemplate)
	if tmplErr != nil {
		return nil, "", nil, fmt.Errorf("pseudo.ToReadGate: template parse: %w", tmplErr)
	}

	var methodBuf bytes.Buffer
	if tmplErr = tmpl.Execute(&methodBuf, templateData{HasSignal: hasSignal}); tmplErr != nil {
		return nil, "", nil, fmt.Errorf("pseudo.ToReadGate: template execute: %w", tmplErr)
	}

	// Regenerate the full file from scratch; struct shape follows guard terms.
	const fileTemplateWithSignal = `package readgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire               func()
	Value              int
	HasValue           bool
	HasChainInhibitor  bool
	FromInput          *Wiring.In
	FromChainInhibitor *Wiring.In
	ToChainInhibitor   *Wiring.Out
}

{{.UpdateMethod}}
func init() {
	Wiring.Register("ReadGate", func() any { return &Node{} })
}
`
	const fileTemplateNoSignal = `package readgate

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire             func()
	Value            int
	HasValue         bool
	FromInput        *Wiring.In
	ToChainInhibitor *Wiring.Out
}

{{.UpdateMethod}}
func init() {
	Wiring.Register("ReadGate", func() any { return &Node{} })
}
`
	type fileData struct {
		UpdateMethod string
	}

	fileTemplateStr := fileTemplateWithSignal
	if !hasSignal {
		fileTemplateStr = fileTemplateNoSignal
	}

	fileTmpl, tmplErr := template.New("file").Parse(fileTemplateStr)
	if tmplErr != nil {
		return nil, "", nil, fmt.Errorf("pseudo.ToReadGate: file template parse: %w", tmplErr)
	}

	var fileBuf bytes.Buffer
	if tmplErr = fileTmpl.Execute(&fileBuf, fileData{UpdateMethod: methodBuf.String()}); tmplErr != nil {
		return nil, "", nil, fmt.Errorf("pseudo.ToReadGate: file template execute: %w", tmplErr)
	}

	formatted, fmtErr := format.Source(fileBuf.Bytes())
	if fmtErr != nil {
		return nil, "", nil, fmt.Errorf("pseudo.ToReadGate: format source: %w\nsource:\n%s", fmtErr, fileBuf.String())
	}

	return formatted, v.OutNeighbor, removedPorts, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// detectReadGateGuard inspects the AST for the firing-guard if-statement and
// returns the guard terms. Accepts either:
//
//	g.HasValue && g.HasChainInhibitor  → ["value", "signal"]
//	g.HasValue                         → ["value"]
//
// ReadGate is AND-only; a || guard is not matched (falls through to error).
func detectReadGateGuard(f *ast.File) ([]string, error) {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		if andBoolOp(ifStmt.Cond, "HasValue", "HasChainInhibitor") {
			found = []string{"input value", "signal"}
			return false
		}
		if selectorOrIdent(ifStmt.Cond, "HasValue") {
			found = []string{"input value"}
			return false
		}
		return true
	})
	if len(found) == 0 {
		return nil, fmt.Errorf("Update method missing expected AND guard: HasValue (with or without HasChainInhibitor)")
	}
	return found, nil
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
		if xSel.Sel.Name == portToChainInhibitor && sel.Sel.Name == "TrySend" {
			found = true
		}
		return true
	})
	if !found {
		return fmt.Errorf("Update method missing expected ToChainInhibitor.TrySend call")
	}
	return nil
}

// andBoolOp reports whether expr is (X.left && X.right).
// Returns true only for &&; || is not matched (ReadGate is AND-only).
func andBoolOp(expr ast.Expr, left, right string) bool {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if !selectorOrIdent(bin.X, left) || !selectorOrIdent(bin.Y, right) {
		return false
	}
	return bin.Op.String() == "&&"
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

// isParseError attempts to unwrap err into a *parseError; returns true and
// sets pe if successful.
func isParseError(err error, pe **parseError) bool {
	if p, ok := err.(*parseError); ok {
		*pe = p
		return true
	}
	return false
}

// parseReadGatePseudo parses the ReadGate pseudo grammar:
//
//	"if" "input" "value" ["and" ident] NEWLINE "input" "value" "->" ident
func (p *pseudoParser) parseReadGatePseudo() (ReadGateView, error) {
	if rawErr := p.consumeWord("if"); rawErr != nil {
		tok := p.peekWord()
		if tok == "" {
			tok = excerpt(p.input, p.pos)
		}
		return ReadGateView{}, &parseError{kind: parseErrBadStart, token: tok, wrapped: rawErr}
	}

	// First guard term is the two-word phrase "input value" (required)
	if rawErr := p.consumeWord("input"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeWord("value"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}
	term1 := "input value"

	// Optional "and" <ident>; "or" is rejected — ReadGate is AND-only.
	var guardTerms []string
	guardTerms = append(guardTerms, term1)

	if pw := p.peekWord(); pw == "or" {
		return ReadGateView{}, &parseError{kind: parseErrGeneric, token: pw,
			wrapped: fmt.Errorf("ReadGate is AND-only; \"or\" gate is not permitted")}
	} else if pw == "and" {
		_ = p.consumeWord("and")
		term2, rawErr := p.consumeIdent()
		if rawErr != nil {
			tok := excerpt(p.input, p.pos)
			return ReadGateView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
		}
		guardTerms = append(guardTerms, term2)
	} else if pw != "" && pw != "input" {
		// Some other word where gate keyword expected — reject.
		return ReadGateView{}, &parseError{kind: parseErrGeneric, token: pw,
			wrapped: fmt.Errorf("expected gate keyword \"and\", got %q", pw)}
	}

	// "input" "value" "->" ident (send line)
	if rawErr := p.consumeWord("input"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeWord("value"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeToken("->"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
	}
	outNeighbor, rawErr := p.consumeIdent()
	if rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}

	p.skipWS()
	if p.pos != len(p.input) {
		tok := excerpt(p.input, p.pos)
		return ReadGateView{}, &parseError{kind: parseErrTrailing, token: tok,
			wrapped: fmt.Errorf("unexpected trailing content at position %d: %q", p.pos, tok)}
	}

	return ReadGateView{GuardTerms: guardTerms, OutNeighbor: outNeighbor}, nil
}
