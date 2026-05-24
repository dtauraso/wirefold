// Package pseudo implements algebraic pseudo projections for wirefold node kinds.
// The pseudo view hides substrate ceremony and surfaces only the logic tokens
// visible to a node author: values, channel names, and control flags.
package pseudo

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"strconv"
	"strings"
	"unicode"
)

// Origin tags which source owns a token in InputView.
type Origin int

const (
	OriginGo   Origin = iota // token lives in the Go source file
	OriginSpec                // token lives in the topology.json spec entry
)

// InputView is the parsed representation of one Input node instance.
// It carries each logical token with its origin so ToInput can route
// writes back to the correct source.
type InputView struct {
	OutputField string // OriginGo  — *Wiring.Out struct field name
	OutNeighbor string // topology  — downstream node id, supplied by caller
	InitValues  []int  // OriginSpec — data.init
	Repeat      bool   // OriginSpec — data.repeat

	// Hidden ceremony preserved for byte-identical round-trip.
	origGoSrc []byte
	origSpec  map[string]any
}

// FromInput parses the Go source of nodes/Input/node.go and the per-instance
// spec entry from topology.json into an InputView.
//
// goSrc must contain a package-level struct named Node in package input with
// exactly one *Wiring.Out field. specEntry must contain "init" ([]numbers) and
// "repeat" (bool). outNeighbor is resolved from topology by the caller and is
// not derived from Go source.
func FromInput(goSrc []byte, specEntry map[string]any, outNeighbor string) (InputView, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, 0)
	if err != nil {
		return InputView{}, fmt.Errorf("pseudo.FromInput: parse go source: %w", err)
	}

	outputField, err := findSingleOutField(f)
	if err != nil {
		return InputView{}, fmt.Errorf("pseudo.FromInput: %w", err)
	}

	initValues, err := specInitSlice(specEntry)
	if err != nil {
		return InputView{}, fmt.Errorf("pseudo.FromInput: %w", err)
	}

	repeat, err := specRepeatBool(specEntry)
	if err != nil {
		return InputView{}, fmt.Errorf("pseudo.FromInput: %w", err)
	}

	// Deep-clone origSpec so mutations to the caller's map don't alias ours.
	cloned := cloneSpec(specEntry)

	return InputView{
		OutputField: outputField,
		OutNeighbor: outNeighbor,
		InitValues:  initValues,
		Repeat:      repeat,
		origGoSrc:   goSrc,
		origSpec:    cloned,
	}, nil
}

// RenderInput produces the human-readable pseudo text for an InputView.
// Format examples:
//
//	[0, 1] -> readGate1
//	↺ [0, 1] -> readGate1
func RenderInput(v InputView) string {
	var b strings.Builder
	if v.Repeat {
		b.WriteString("↺ ")
	}
	b.WriteString("[")
	for i, n := range v.InitValues {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(n))
	}
	b.WriteString("] -> ")
	b.WriteString(v.OutNeighbor)
	return b.String()
}

// ParseInputError is the error type returned by ParseInput when the pseudo
// text fails to parse. It carries a human-readable message, the underlying
// parse error, and a canonical-form suggestion.
type ParseInputError struct {
	msg        string
	cause      error
	suggestion string
}

func (e *ParseInputError) Error() string      { return e.msg }
func (e *ParseInputError) Unwrap() error      { return e.cause }
func (e *ParseInputError) Suggestion() string { return e.suggestion }

// buildSuggestion produces the canonical suggestion string for a failed
// ParseInput call. It uses prior.OutNeighbor when available; otherwise it
// falls back to a placeholder.
func buildSuggestion(prior InputView) string {
	neighbor := prior.OutNeighbor
	if neighbor == "" {
		neighbor = "<node>"
	}
	vals := prior.InitValues
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.Itoa(v)
	}
	list := strings.Join(parts, ", ")
	return fmt.Sprintf("Try: [↺] [%s] -> %s", list, neighbor)
}

// ParseInput parses a pseudo text produced (or edited) by the user back into
// an InputView. It is strict: the entire input must match the grammar; extra
// trailing tokens are an error.
//
// Grammar (whitespace-insensitive):
//
//	pseudo   := ["↺"] "[" intList "]" "->" ident
//	intList  := int ("," int)* | ε
//
// The returned view inherits prior.origGoSrc, prior.origSpec, and
// prior.OutputField so that unchanged ceremony is preserved for byte-identical
// round-trips. The parsed ident after "->" is stored in OutNeighbor.
//
// On parse failure the returned error is *ParseInputError, which exposes a
// Suggestion() string with the canonical form.
func ParseInput(text string, prior InputView) (InputView, error) {
	p := &pseudoParser{input: text}
	repeat, vals, outNeighbor, err := p.parseInputPseudo()
	if err != nil {
		var pe *parseError
		if errors.As(err, &pe) {
			return InputView{}, &ParseInputError{
				msg:        pe.humanMessage(),
				cause:      err,
				suggestion: buildSuggestion(prior),
			}
		}
		return InputView{}, &ParseInputError{
			msg:        err.Error(),
			cause:      err,
			suggestion: buildSuggestion(prior),
		}
	}
	return InputView{
		OutputField: prior.OutputField,
		OutNeighbor: outNeighbor,
		InitValues:  vals,
		Repeat:      repeat,
		origGoSrc:   prior.origGoSrc,
		origSpec:    prior.origSpec,
	}, nil
}

// ToInput writes an InputView back to its two sources.
//
// Go source: if OutputField equals the original field name the original goSrc
// bytes are returned unchanged (byte-identical). If OutputField differs, the
// struct field declaration and all references in the Update method body are
// renamed, then the file is re-emitted via go/format.
//
// Spec: a clone of origSpec is returned with "init" and "repeat" set from
// the view; all other keys are preserved unchanged.
func ToInput(v InputView) (newGoSrc []byte, newSpec map[string]any, err error) {
	// --- Go source ---
	origOutputField, err2 := goOutputField(v.origGoSrc)
	if err2 != nil {
		return nil, nil, fmt.Errorf("pseudo.ToInput: re-parse original go src: %w", err2)
	}

	if v.OutputField == origOutputField {
		newGoSrc = v.origGoSrc
	} else {
		newGoSrc, err = renameOutputField(v.origGoSrc, origOutputField, v.OutputField)
		if err != nil {
			return nil, nil, fmt.Errorf("pseudo.ToInput: rename field: %w", err)
		}
	}

	// --- Spec ---
	newSpec = cloneSpec(v.origSpec)
	initAny := make([]any, len(v.InitValues))
	for i, n := range v.InitValues {
		initAny[i] = n
	}
	newSpec["init"] = initAny
	newSpec["repeat"] = v.Repeat

	return newGoSrc, newSpec, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// findSingleOutField returns the name of the single *Wiring.Out field in
// the Node struct. Returns an error if there are zero or more than one.
func findSingleOutField(f *ast.File) (string, error) {
	var names []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Node" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if isWiringOutType(field.Type) {
					for _, n := range field.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	switch len(names) {
	case 0:
		return "", fmt.Errorf("Node struct has no *Wiring.Out field")
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("Node struct has %d *Wiring.Out fields; expected exactly 1", len(names))
	}
}

// isWiringOutType reports whether expr represents *Wiring.Out.
func isWiringOutType(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "Wiring" && sel.Sel.Name == "Out"
}

func specInitSlice(spec map[string]any) ([]int, error) {
	raw, ok := spec["init"]
	if !ok {
		return nil, fmt.Errorf(`spec entry missing "init" key`)
	}
	slice, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`spec "init" must be []any (got %T)`, raw)
	}
	result := make([]int, len(slice))
	for i, v := range slice {
		switch n := v.(type) {
		case float64:
			result[i] = int(n)
		case int:
			result[i] = n
		default:
			return nil, fmt.Errorf(`spec "init"[%d] has unexpected type %T`, i, v)
		}
	}
	return result, nil
}

func specRepeatBool(spec map[string]any) (bool, error) {
	raw, ok := spec["repeat"]
	if !ok {
		return false, fmt.Errorf(`spec entry missing "repeat" key`)
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf(`spec "repeat" must be bool (got %T)`, raw)
	}
	return b, nil
}

func cloneSpec(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

func goOutputField(goSrc []byte) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, 0)
	if err != nil {
		return "", err
	}
	return findSingleOutField(f)
}

// renameOutputField renames oldName → newName in both the Node struct
// declaration and all selector expressions in the Update method body.
func renameOutputField(goSrc []byte, oldName, newName string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", goSrc, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			// Struct field declaration.
			if isWiringOutType(node.Type) {
				for _, ident := range node.Names {
					if ident.Name == oldName {
						ident.Name = newName
					}
				}
			}
		case *ast.SelectorExpr:
			// n.OldName.Method(...) — check parent is n (receiver ident).
			if node.Sel.Name == oldName {
				if recv, ok := node.X.(*ast.SelectorExpr); ok {
					_ = recv // deep selector; unlikely but handled below
				}
				// We need to distinguish n.<field> selectors from other selectors.
				// The receiver in Update is *Node so the pattern is n.<fieldName>.
				// Check that X is an identifier (the method receiver variable).
				if _, ok := node.X.(*ast.Ident); ok {
					node.Sel.Name = newName
				}
			}
		}
		return true
	})

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, err
	}
	// gofmt the output for canonical form.
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ─── pseudo parser ────────────────────────────────────────────────────────────

// parseErrorKind describes which structural problem the parser encountered.
type parseErrorKind int

const (
	parseErrBadStart      parseErrorKind = iota // first token is not "["
	parseErrBadInt                              // non-integer where int expected
	parseErrUnclosedBrace                       // "[" without closing "]"
	parseErrMissingIdent                        // "to" with no ident following
	parseErrTrailing                            // extra tokens after valid parse
	parseErrGeneric                             // fallback
)

// parseError is the structured error produced by the pseudo parser.
// It carries the offending token (raw text) and a kind for message generation.
type parseError struct {
	kind    parseErrorKind
	token   string // the offending excerpt
	wrapped error  // original low-level error (optional)
}

func (e *parseError) Error() string       { return e.humanMessage() }
func (e *parseError) Unwrap() error       { return e.wrapped }
func (e *parseError) Is(target error) bool { _, ok := target.(*parseError); return ok }

func (e *parseError) humanMessage() string {
	switch e.kind {
	case parseErrBadStart:
		return fmt.Sprintf("Couldn't parse %q. Lines must start with \"[\" or \"↺\".", e.token)
	case parseErrBadInt:
		return fmt.Sprintf("Couldn't parse %q. Init values must be whole numbers.", e.token)
	case parseErrUnclosedBrace:
		return fmt.Sprintf("Couldn't parse %q. Init values must be enclosed in brackets like [0, 1].", e.token)
	case parseErrMissingIdent:
		return fmt.Sprintf("Couldn't parse %q. Missing node id after \"->\".", e.token)
	case parseErrTrailing:
		return fmt.Sprintf("Couldn't parse %q. Unexpected text after the output field name.", e.token)
	default:
		return fmt.Sprintf("Couldn't parse %q.", e.token)
	}
}

type pseudoParser struct {
	input string
	pos   int
}

func (p *pseudoParser) skipWS() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *pseudoParser) peekWord() string {
	p.skipWS()
	start := p.pos
	for p.pos < len(p.input) && (unicode.IsLetter(rune(p.input[p.pos])) || rune(p.input[p.pos]) == '_') {
		p.pos++
	}
	word := p.input[start:p.pos]
	p.pos = start // don't consume yet
	return word
}

func (p *pseudoParser) consumeWord(expected string) error {
	p.skipWS()
	if !strings.HasPrefix(p.input[p.pos:], expected) {
		return fmt.Errorf("expected %q at position %d, got %q", expected, p.pos, excerpt(p.input, p.pos))
	}
	// Make sure it's a whole word (not a prefix of a longer identifier).
	end := p.pos + len(expected)
	if end < len(p.input) && (unicode.IsLetter(rune(p.input[end])) || rune(p.input[end]) == '_') {
		return fmt.Errorf("expected %q at position %d, got %q", expected, p.pos, excerpt(p.input, p.pos))
	}
	p.pos += len(expected)
	return nil
}

func (p *pseudoParser) consumeIdent() (string, error) {
	p.skipWS()
	start := p.pos
	if p.pos >= len(p.input) {
		return "", fmt.Errorf("expected identifier at position %d, got EOF", p.pos)
	}
	ch := rune(p.input[p.pos])
	if !unicode.IsLetter(ch) && ch != '_' {
		return "", fmt.Errorf("expected identifier at position %d, got %q", p.pos, excerpt(p.input, p.pos))
	}
	for p.pos < len(p.input) {
		ch = rune(p.input[p.pos])
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '_' {
			break
		}
		p.pos++
	}
	return p.input[start:p.pos], nil
}

// consumeToken consumes an exact literal string (no word-boundary check).
// Use for punctuation tokens like "->".
func (p *pseudoParser) consumeToken(tok string) error {
	p.skipWS()
	if !strings.HasPrefix(p.input[p.pos:], tok) {
		return fmt.Errorf("expected %q at position %d, got %q", tok, p.pos, excerpt(p.input, p.pos))
	}
	p.pos += len(tok)
	return nil
}

func (p *pseudoParser) consumeChar(ch byte) error {
	p.skipWS()
	if p.pos >= len(p.input) || p.input[p.pos] != ch {
		return fmt.Errorf("expected %q at position %d, got %q", ch, p.pos, excerpt(p.input, p.pos))
	}
	p.pos++
	return nil
}

func (p *pseudoParser) consumeInt() (int, error) {
	p.skipWS()
	start := p.pos
	if p.pos < len(p.input) && (p.input[p.pos] == '-' || p.input[p.pos] == '+') {
		p.pos++
	}
	digits := p.pos
	for p.pos < len(p.input) && unicode.IsDigit(rune(p.input[p.pos])) {
		p.pos++
	}
	if p.pos == digits {
		p.pos = start
		return 0, fmt.Errorf("expected integer at position %d, got %q", p.pos, excerpt(p.input, p.pos))
	}
	n, err := strconv.Atoi(p.input[start:p.pos])
	if err != nil {
		p.pos = start
		return 0, fmt.Errorf("invalid integer at position %d: %w", start, err)
	}
	return n, nil
}

func (p *pseudoParser) parseInputPseudo() (repeat bool, vals []int, ident string, err error) {
	// Optional leading "↺" (U+21BA).
	p.skipWS()
	if strings.HasPrefix(p.input[p.pos:], "↺") {
		p.pos += len("↺")
		repeat = true
	}

	if rawErr := p.consumeChar('['); rawErr != nil {
		// Use just the first word so the token is concise (e.g., "not" not "not valid...").
		tok := p.peekWord()
		if tok == "" {
			tok = excerpt(p.input, p.pos)
		}
		err = &parseError{kind: parseErrBadStart, token: tok, wrapped: rawErr}
		return
	}

	// intList: optional, comma-separated
	p.skipWS()
	if p.pos < len(p.input) && p.input[p.pos] != ']' {
		var n int
		if rawErr := func() error { var e error; n, e = p.consumeInt(); return e }(); rawErr != nil {
			tok := excerpt(p.input, p.pos)
			err = &parseError{kind: parseErrBadInt, token: tok, wrapped: rawErr}
			return
		}
		vals = append(vals, n)
		for {
			p.skipWS()
			if p.pos >= len(p.input) || p.input[p.pos] != ',' {
				break
			}
			p.pos++ // consume ','
			if rawErr := func() error { var e error; n, e = p.consumeInt(); return e }(); rawErr != nil {
				tok := excerpt(p.input, p.pos)
				err = &parseError{kind: parseErrBadInt, token: tok, wrapped: rawErr}
				return
			}
			vals = append(vals, n)
		}
	}

	if rawErr := p.consumeChar(']'); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		err = &parseError{kind: parseErrUnclosedBrace, token: tok, wrapped: rawErr}
		return
	}
	if rawErr := p.consumeToken("->"); rawErr != nil {
		tok := excerpt(p.input, p.pos)
		err = &parseError{kind: parseErrGeneric, token: tok, wrapped: rawErr}
		return
	}
	if rawIdent, rawErr := p.consumeIdent(); rawErr != nil {
		_ = rawIdent
		p.skipWS()
		tok := excerpt(p.input, p.pos)
		err = &parseError{kind: parseErrMissingIdent, token: tok, wrapped: rawErr}
		return
	} else {
		ident = rawIdent
	}

	// Must be at end (after optional whitespace).
	p.skipWS()
	if p.pos != len(p.input) {
		tok := excerpt(p.input, p.pos)
		err = &parseError{kind: parseErrTrailing, token: tok, wrapped: fmt.Errorf("unexpected trailing content at position %d: %q", p.pos, tok)}
		return
	}
	return
}

func excerpt(s string, pos int) string {
	end := min(pos+20, len(s))
	return s[pos:end]
}
