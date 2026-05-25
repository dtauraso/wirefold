package pseudo

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
)

// ChainInhibitorView is the parsed representation of a ChainInhibitor node instance.
//
// Semantics: on receiving input v, emit the previously-held value to all ToNext
// outputs (broadcast), then held = v (one-step delay / shift).
//
// Grammar:
//
//	send held -> <OutNeighbor>[, <OutNeighbor> ...]
//	keep input
type ChainInhibitorView struct {
	OutNeighbors []string // downstream node ids, supplied by caller from topology
}

// FromChainInhibitor parses the Go source of nodes/chaininhibitor/node.go to verify
// its expected structure, then returns a ChainInhibitorView.
//
// outNeighbors is resolved from topology by the caller and is not derived from Go.
func FromChainInhibitor(goSrc []byte, outNeighbors []string) (ChainInhibitorView, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, 0)
	if err != nil {
		return ChainInhibitorView{}, fmt.Errorf("pseudo.FromChainInhibitor: parse go source: %w", err)
	}

	if err := verifyChainInhibitorStructure(f); err != nil {
		return ChainInhibitorView{}, fmt.Errorf("pseudo.FromChainInhibitor: %w", err)
	}

	return ChainInhibitorView{OutNeighbors: outNeighbors}, nil
}

// RenderChainInhibitor emits the human-readable pseudo text for a ChainInhibitorView.
//
//	send held -> <OutNeighbor>[, <OutNeighbor> ...]
//	keep input
func RenderChainInhibitor(v ChainInhibitorView) string {
	var b strings.Builder
	b.WriteString("send held -> ")
	b.WriteString(strings.Join(v.OutNeighbors, ", "))
	b.WriteString("\n")
	b.WriteString("keep input")
	b.WriteString("\n")
	return b.String()
}

// ParseChainInhibitorError is the error type returned by ParseChainInhibitor on malformed input.
type ParseChainInhibitorError struct {
	msg        string
	cause      error
	suggestion string
}

func (e *ParseChainInhibitorError) Error() string      { return e.msg }
func (e *ParseChainInhibitorError) Unwrap() error      { return e.cause }
func (e *ParseChainInhibitorError) Suggestion() string { return e.suggestion }

// buildChainInhibitorSuggestion builds the canonical suggestion string from a prior view.
func buildChainInhibitorSuggestion(prior ChainInhibitorView) string {
	neighbors := strings.Join(prior.OutNeighbors, ", ")
	if neighbors == "" {
		neighbors = "<node>"
	}
	return fmt.Sprintf("Try: send held -> %s\n   keep input", neighbors)
}

// ParseChainInhibitor parses edited pseudo text back into a ChainInhibitorView.
//
// Grammar (whitespace-insensitive across lines):
//
//	"send" "held" "->" ident NEWLINE "keep" "input"
//
// On malformed input returns *ParseChainInhibitorError with a human message and Suggestion().
func ParseChainInhibitor(text string, prior ChainInhibitorView) (ChainInhibitorView, error) {
	p := &pseudoParser{input: strings.TrimSpace(text)}
	v, err := p.parseChainInhibitorPseudo()
	if err != nil {
		var pe *parseError
		if isParseError(err, &pe) {
			return ChainInhibitorView{}, &ParseChainInhibitorError{
				msg:        pe.humanMessage(),
				cause:      err,
				suggestion: buildChainInhibitorSuggestion(prior),
			}
		}
		return ChainInhibitorView{}, &ParseChainInhibitorError{
			msg:        err.Error(),
			cause:      err,
			suggestion: buildChainInhibitorSuggestion(prior),
		}
	}
	return v, nil
}

// ToChainInhibitor regenerates nodes/chaininhibitor/node.go to match v.
//
// Returns the new Go source and the new output-neighbor names (broadcast set).
// removedPorts is always empty for ChainInhibitor (no optional ports).
func ToChainInhibitor(v ChainInhibitorView) (newGoSrc []byte, newOutNeighbors []string, removedPorts []string, err error) {
	const fileTemplate = `package chaininhibitor

import (
	"context"
	"sync"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire                       func()
	Held                       int ` + "`wire:\"data.state\"`" + `
	FromPrevChainInhibitorNode *Wiring.In
	ToNext                     Wiring.OutMulti
}

func (in *Node) Update(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevChainInhibitorNode.Done()
			var wg sync.WaitGroup
			for _, out := range in.ToNext {
				wg.Add(1)
				go func(o *Wiring.Out) {
					defer wg.Done()
					o.TrySend(in.Held)
				}(out)
			}
			wg.Wait()
			in.Held = value
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &Node{} })
}
`
	formatted, fmtErr := format.Source([]byte(fileTemplate))
	if fmtErr != nil {
		return nil, nil, nil, fmt.Errorf("pseudo.ToChainInhibitor: format source: %w", fmtErr)
	}

	return formatted, v.OutNeighbors, nil, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// verifyChainInhibitorStructure checks that the Update method has the expected
// TryRecv + TrySend pattern for a ChainInhibitor node.
func verifyChainInhibitorStructure(f *ast.File) error {
	foundRecv := false
	foundSend := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// TrySend may be on a loop variable (not a selector); detect by method name.
		if sel.Sel.Name == "TrySend" {
			foundSend = true
		}
		// TryRecv must be on FromPrevChainInhibitorNode.
		xSel, ok := sel.X.(*ast.SelectorExpr)
		if ok && xSel.Sel.Name == "FromPrevChainInhibitorNode" && sel.Sel.Name == "TryRecv" {
			foundRecv = true
		}
		return true
	})
	if !foundRecv {
		return fmt.Errorf("Update method missing expected FromPrevChainInhibitorNode.TryRecv call")
	}
	if !foundSend {
		return fmt.Errorf("Update method missing expected TrySend call")
	}
	return nil
}

// parseChainInhibitorPseudo parses the ChainInhibitor pseudo grammar:
//
//	"send" "held" "->" ident NEWLINE "keep" "input"
func (p *pseudoParser) parseChainInhibitorPseudo() (ChainInhibitorView, error) {
	// Line 1: "send" "held" "->" ident
	if rawErr := p.consumeWord("send"); rawErr != nil {
		tok := p.peekWord()
		if tok == "" {
			tok = excerpt(p.input, p.pos)
		}
		return ChainInhibitorView{}, &parseError{kind: parseErrBadStart, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeWord("held"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeToken("->"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
	}
	// Parse one or more comma-separated idents: n0[, n1[, n2 ...]]
	first, rawErr := p.consumeIdent()
	if rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}
	outNeighbors := []string{first}
	for {
		p.skipWS()
		if p.pos >= len(p.input) || p.input[p.pos] != ',' {
			break
		}
		p.pos++ // consume comma
		p.skipWS()
		next, rawErr2 := p.consumeIdent()
		if rawErr2 != nil {
			tok := excerpt(p.input, p.pos)
			return ChainInhibitorView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr2}
		}
		outNeighbors = append(outNeighbors, next)
	}

	// Line 2: "keep" "input"
	if rawErr := p.consumeWord("keep"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
	}
	if rawErr := p.consumeWord("input"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
	}

	p.skipWS()
	if p.pos != len(p.input) {
		tok := excerpt(p.input, p.pos)
		return ChainInhibitorView{}, &parseError{kind: parseErrTrailing, token: tok,
			wrapped: fmt.Errorf("unexpected trailing content at position %d: %q", p.pos, tok)}
	}

	return ChainInhibitorView{OutNeighbors: outNeighbors}, nil
}

