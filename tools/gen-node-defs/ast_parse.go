// AST/SPEC.md parsing helpers shared by the node-defs and wire-defs pipelines:
// discovering ports/data-fields/kind names from Go source, and view metadata
// from SPEC.md.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// parsePortsFromAST reads all .go files in pkgDir and returns ports derived
// from channel-typed struct fields. Fields with wire:"data.*" tags are skipped.
func parsePortsFromAST(pkgDir string) ([]port, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	pkgs := map[string][]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fullPath := filepath.Join(pkgDir, name)
		f, err := parser.ParseFile(fset, fullPath, nil, 0)
		if err != nil {
			return nil, err
		}
		pkgName := f.Name.Name
		pkgs[pkgName] = append(pkgs[pkgName], f)
	}
	var ports []port
	// Iterate package names in sorted order so the emitted port order is
	// deterministic even when a dir contains two package names (map iteration
	// order is otherwise random and would flip-flop check-generated).
	pkgNames := make([]string, 0, len(pkgs))
	for name := range pkgs {
		pkgNames = append(pkgNames, name)
	}
	sort.Strings(pkgNames)
	for _, pkgName := range pkgNames {
		files := pkgs[pkgName]
		for _, file := range files {
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					for _, field := range structType.Fields.List {
						dir, ok := chanDirection(field.Type)
						if !ok {
							continue
						}
						// Skip wire:"data.*" fields.
						if field.Tag != nil {
							tag := strings.Trim(field.Tag.Value, "`")
							if strings.Contains(tag, `wire:"data.`) {
								continue
							}
						}
						// Get field name(s).
						multi := dir == "outMulti"
						outDir := dir
						if multi {
							outDir = "out"
						}
						for _, name := range field.Names {
							ports = append(ports, port{id: name.Name, direction: outDir, isMulti: multi})
						}
					}
				}
			}
		}
	}
	return ports, nil
}

// parseEmbeddedPorts scans pkgDir's struct declarations for anonymous
// (embedded) fields whose type is a selector into another local nodes/<pkg>
// package (e.g. gatecommon.GateNode), and returns the channel-typed ports
// declared on that embedded package's own structs (recursively, guarded by
// visited to avoid cycles). This lets a wrapper kind's SPEC.md-independent
// AST port discovery still pick up promoted fields from a shared embedded
// struct package.
func parseEmbeddedPorts(nodesDir, pkgDir string, visited map[string]bool) ([]port, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	var ports []port
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					if len(field.Names) != 0 {
						continue // not embedded
					}
					sel, ok := field.Type.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					pkgIdent, ok := sel.X.(*ast.Ident)
					if !ok {
						continue
					}
					embedDir := filepath.Join(nodesDir, strings.ToLower(pkgIdent.Name))
					if visited[embedDir] {
						continue
					}
					visited[embedDir] = true
					if _, statErr := os.Stat(embedDir); statErr != nil {
						continue // not a local nodes/ package (e.g. Wiring itself)
					}
					embedded, err := parsePortsFromAST(embedDir)
					if err != nil {
						return nil, err
					}
					ports = append(ports, embedded...)
					more, err := parseEmbeddedPorts(nodesDir, embedDir, visited)
					if err != nil {
						return nil, err
					}
					ports = append(ports, more...)
				}
			}
		}
	}
	return ports, nil
}

// chanDirection returns ("in", true) for *Wiring.In, ("out", true) for *Wiring.Out
// or Wiring.OutMulti, and ("", false) for anything else.
func chanDirection(expr ast.Expr) (string, bool) {
	// *Wiring.In or *Wiring.Out — pointer to selector
	if star, ok := expr.(*ast.StarExpr); ok {
		if sel, ok := star.X.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "Wiring" {
				switch sel.Sel.Name {
				case "In":
					return "in", true
				case "Out":
					return "out", true
				}
			}
		}
		return "", false
	}
	// Wiring.OutMulti — bare selector (type alias, no pointer)
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "Wiring" && sel.Sel.Name == "OutMulti" {
			return "outMulti", true
		}
	}
	return "", false
}

// parseSpecMD reads SPEC.md in pkgDir and returns the view definition,
// a map of port-name → accent override, a map of port-name → edgeKind,
// and a set of optional port names from the Ports table.
func parseSpecMD(pkgDir string) (viewDef, map[string]string, map[string]string, map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "SPEC.md"))
	if err != nil {
		return viewDef{}, nil, nil, nil, err
	}
	lines := strings.Split(string(data), "\n")

	sectionLines := func(heading string) []string {
		start := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == "## "+heading {
				start = i
				break
			}
		}
		if start == -1 {
			return nil
		}
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") {
				end = i
				break
			}
		}
		return lines[start+1 : end]
	}

	// Parse a markdown table into rows.
	parseTable := func(tableLines []string) ([]string, [][]string) {
		var rows []string
		var headers []string
		var result [][]string
		for _, l := range tableLines {
			if !strings.Contains(l, "|") {
				continue
			}
			rows = append(rows, l)
		}
		if len(rows) < 2 {
			return nil, nil
		}
		// First row is headers.
		parts := strings.Split(rows[0], "|")
		for _, p := range parts {
			h := strings.TrimSpace(p)
			if h != "" {
				headers = append(headers, h)
			}
		}
		for _, row := range rows[1:] {
			parts := strings.Split(row, "|")
			var cells []string
			for _, p := range parts {
				cells = append(cells, strings.TrimSpace(p))
			}
			// Remove leading/trailing empty cells from split on "|".
			if len(cells) > 0 && cells[0] == "" {
				cells = cells[1:]
			}
			if len(cells) > 0 && cells[len(cells)-1] == "" {
				cells = cells[:len(cells)-1]
			}
			// Skip separator rows.
			allSep := true
			for _, c := range cells {
				if !isSep(c) {
					allSep = false
					break
				}
			}
			if allSep {
				continue
			}
			result = append(result, cells)
		}
		return headers, result
	}

	// Parse View section.
	viewLines := sectionLines("View")
	if viewLines == nil {
		return viewDef{}, nil, nil, nil, fmt.Errorf("no View section")
	}
	headers, rows := parseTable(viewLines)
	fieldIdx := indexOf(headers, "Field")
	valueIdx := indexOf(headers, "Value")
	if fieldIdx == -1 || valueIdx == -1 {
		return viewDef{}, nil, nil, nil, fmt.Errorf("view table missing Field/Value columns")
	}
	vmap := map[string]string{}
	for _, row := range rows {
		if fieldIdx < len(row) && valueIdx < len(row) {
			vmap[row[fieldIdx]] = row[valueIdx]
		}
	}
	view := viewDef{
		kind:     vmap["kind"],
		bg:       vmap["bg"],
		border:   vmap["border"],
		text:     vmap["text"],
		minWidth: vmap["minWidth"],
		role:     vmap["role"],
		shape:    vmap["shape"],
		fill:     vmap["fill"],
		stroke:   vmap["stroke"],
		width:    vmap["width"],
		height:   vmap["height"],
	}

	// Parse Ports section for accent, edgeKind overrides, and optional flags.
	accentOverrides := map[string]string{}
	edgeKindOverrides := map[string]string{}
	optionalPorts := map[string]bool{}
	portsLines := sectionLines("Ports")
	if portsLines != nil {
		headers, rows := parseTable(portsLines)
		nameIdx := indexOf(headers, "Name")
		accentIdx := indexOf(headers, "Accent")
		edgeKindIdx := indexOf(headers, "EdgeKind")
		optionalIdx := indexOf(headers, "Optional")
		for _, row := range rows {
			if nameIdx >= len(row) {
				continue
			}
			name := row[nameIdx]
			if name == "" {
				continue
			}
			if accentIdx != -1 && accentIdx < len(row) && row[accentIdx] != "" {
				accentOverrides[name] = row[accentIdx]
			}
			if edgeKindIdx != -1 && edgeKindIdx < len(row) && row[edgeKindIdx] != "" {
				edgeKindOverrides[name] = row[edgeKindIdx]
			}
			if optionalIdx != -1 && optionalIdx < len(row) && row[optionalIdx] == "yes" {
				optionalPorts[name] = true
			}
		}
	}

	return view, accentOverrides, edgeKindOverrides, optionalPorts, nil
}

// parsePortsFromSpec reads nodes/<Kind>/SPEC.md and returns ports derived from
// the Ports table (Name + Direction columns). Used as a fallback when AST
// parsing discovers 0 ports — e.g. when all ports live in an embedded struct
// from another package that the AST walker cannot follow.
func parsePortsFromSpec(pkgDir string) []port {
	data, err := os.ReadFile(filepath.Join(pkgDir, "SPEC.md"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	// Locate ## Ports section.
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## Ports" {
			start = i
			break
		}
	}
	if start == -1 {
		return nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	tableLines := lines[start+1 : end]
	// Parse the markdown table.
	var rows []string
	for _, l := range tableLines {
		if strings.Contains(l, "|") {
			rows = append(rows, l)
		}
	}
	if len(rows) < 2 {
		return nil
	}
	// Parse header row.
	var headers []string
	for _, p := range strings.Split(rows[0], "|") {
		h := strings.TrimSpace(p)
		if h != "" {
			headers = append(headers, h)
		}
	}
	nameIdx := indexOf(headers, "Name")
	dirIdx := indexOf(headers, "Direction")
	if nameIdx == -1 || dirIdx == -1 {
		return nil
	}
	var ports []port
	for _, row := range rows[1:] {
		parts := strings.Split(row, "|")
		var cells []string
		for _, p := range parts {
			cells = append(cells, strings.TrimSpace(p))
		}
		if len(cells) > 0 && cells[0] == "" {
			cells = cells[1:]
		}
		if len(cells) > 0 && cells[len(cells)-1] == "" {
			cells = cells[:len(cells)-1]
		}
		// Skip separator rows.
		allSep := true
		for _, c := range cells {
			if !isSep(c) {
				allSep = false
				break
			}
		}
		if allSep {
			continue
		}
		if nameIdx >= len(cells) || dirIdx >= len(cells) {
			continue
		}
		name := cells[nameIdx]
		dir := cells[dirIdx]
		if name == "" || (dir != "in" && dir != "out") {
			continue
		}
		ports = append(ports, port{id: name, direction: dir})
	}
	return ports
}

// parseDefaultData reads nodes/<Kind>/SPEC.md and returns the JSON string from
// the first fenced code block inside ## Default data, or "" if absent.
func parseDefaultData(pkgDir string) string {
	data, err := os.ReadFile(filepath.Join(pkgDir, "SPEC.md"))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	inSection := false
	inFence := false
	var jsonLines []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "## Default data" {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(l, "## ") {
			break
		}
		if inSection && !inFence && strings.TrimSpace(l) == "```json" {
			inFence = true
			continue
		}
		if inSection && inFence {
			if strings.TrimSpace(l) == "```" {
				break
			}
			jsonLines = append(jsonLines, l)
		}
	}
	return strings.TrimSpace(strings.Join(jsonLines, "\n"))
}

func isSep(s string) bool {
	for _, c := range s {
		if c != '-' && c != ':' && c != ' ' {
			return false
		}
	}
	return len(s) > 0
}

func indexOf[T comparable](slice []T, val T) int {
	for i, v := range slice {
		if v == val {
			return i
		}
	}
	return -1
}

// goTypeExprStr converts an ast.Expr to a Go type string.
func goTypeExprStr(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, true
	case *ast.ArrayType:
		elt, ok := goTypeExprStr(t.Elt)
		if !ok {
			return "", false
		}
		return "[]" + elt, true
	case *ast.MapType:
		k, ok1 := goTypeExprStr(t.Key)
		v, ok2 := goTypeExprStr(t.Value)
		if !ok1 || !ok2 {
			return "", false
		}
		return "map[" + k + "]" + v, true
	}
	return "", false
}

// goIdentRE matches a legal TS/Go identifier. goKind is emitted as an unquoted
// TS object key in node-defs.ts, so a non-identifier name (hyphen, space, leading
// digit) would produce invalid TS; validate it at parse time and fail loudly.
var goIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseGoKindName extracts the first string argument to Wiring.Register in pkgDir.
func parseGoKindName(pkgDir string) (string, error) {
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			continue
		}
		s := string(data)
		const marker = `Wiring.Register("`
		_, rest, ok := strings.Cut(s, marker)
		if !ok {
			continue
		}
		name2, _, ok2 := strings.Cut(rest, `"`)
		if !ok2 {
			continue
		}
		if !goIdentRE.MatchString(name2) {
			fatalf("kind name %q from Wiring.Register in %s is not a legal identifier (must match [A-Za-z_][A-Za-z0-9_]*); it is emitted as an unquoted TS object key", name2, pkgDir)
		}
		return name2, nil
	}
	return "", fmt.Errorf("Wiring.Register not found in %s", pkgDir)
}

// parseDataFieldsFromAST reads all .go files in pkgDir and returns data fields
// derived from struct fields tagged with wire:"data.*".
func parseDataFieldsFromAST(pkgDir string) ([]dataField, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fullPath := filepath.Join(pkgDir, name)
		f, err := parser.ParseFile(fset, fullPath, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	var fields []dataField
	for _, file := range files {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range structType.Fields.List {
					if field.Tag == nil {
						continue
					}
					tag := strings.Trim(field.Tag.Value, "`")
					const prefix = `wire:"data.`
					_, after, ok := strings.Cut(tag, prefix)
					if !ok {
						continue
					}
					wireKey, _, ok2 := strings.Cut(after, `"`)
					if !ok2 {
						continue
					}
					var fname string
					if len(field.Names) > 0 {
						fname = field.Names[0].Name
					}
					typeStr, ok := goTypeExprStr(field.Type)
					if !ok {
						displayName := fname
						if displayName == "" {
							displayName = "<anonymous>"
						}
						return nil, fmt.Errorf("kind %q: wire:\"data.%s\" field %q has an unsupported/unstringifiable Go type %T", filepath.Base(pkgDir), wireKey, displayName, field.Type)
					}
					fields = append(fields, dataField{wireTag: wireKey, goType: typeStr, fieldName: fname})
				}
			}
		}
	}
	return fields, nil
}
