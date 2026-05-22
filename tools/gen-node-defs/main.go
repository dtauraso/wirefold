// gen-node-defs walks nodes/*/ and emits src/webview/rf/nodes/node-defs.ts.
// Port names and directions are derived from Go channel-typed struct fields.
// View metadata and per-port accent overrides are read from SPEC.md.
// Run: go run ../../tools/gen-node-defs (from tools/topology-vscode/)
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// wireProp represents one wire:"prop,..." tagged field on specEdge.
type wireProp struct {
	jsonName string // from json:"..." tag
	tsType   string // from tsType:... in wire tag
	required bool   // false if "optional", true if "required"
}

// port represents one channel-typed struct field.
type port struct {
	id        string // Go field name
	direction string // "in" or "out"
	accent    string // optional hex color from SPEC.md
	edgeKind  string // optional edge kind from SPEC.md Ports table EdgeKind column
}

// dataField represents a wire:"data.*" tagged struct field.
type dataField struct {
	wireTag string // full tag value after wire:"data." prefix, e.g. "init" or "initialSlots.held"
	goType  string // Go type string, e.g. "[]int", "int", "string"
}

// viewDef holds the SPEC.md ## View section fields.
type viewDef struct {
	kind         string
	bg           string
	border       string
	text         string
	accent       string
	minWidth     string
	sublabel     string
	displays     string
	defaultLabel string
	// NodeTypeDef-compatible fields (used by schema/node-types consumers).
	role   string
	shape  string
	fill   string
	stroke string
	width  string
	height string
}

// kindEntry is one node kind to emit.
type kindEntry struct {
	kind        string // RF/view kind name (camelCase, from SPEC.md)
	goKind      string // Go/topology kind name (PascalCase, from Wiring.Register)
	view        viewDef
	ports       []port
	dataFields  []dataField
	defaultData string // raw JSON from SPEC.md ## Default data, or ""
}

func main() {
	// Generator is invoked from tools/topology-vscode/ via npm run gen:node-defs.
	// Resolve repo root relative to this file's location at compile time is not
	// possible; instead, walk up from cwd until we find a "nodes/" directory.
	cwd, err := os.Getwd()
	if err != nil {
		fatalf("getwd: %v", err)
	}
	repoRoot := findRepoRoot(cwd)
	if repoRoot == "" {
		fatalf("could not locate repo root (no nodes/ dir found from %s)", cwd)
	}

	nodesDir := filepath.Join(repoRoot, "nodes")
	entries, err := os.ReadDir(nodesDir)
	if err != nil {
		fatalf("readdir nodes: %v", err)
	}

	var kinds []kindEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(nodesDir, e.Name())
		if !hasRegister(pkgDir) {
			continue
		}
		ports, err := parsePortsFromAST(pkgDir)
		if err != nil {
			fatalf("parse ports %s: %v", e.Name(), err)
		}
		dataFields, err := parseDataFieldsFromAST(pkgDir)
		if err != nil {
			fatalf("parse data fields %s: %v", e.Name(), err)
		}
		goKind, err := parseGoKindName(pkgDir)
		if err != nil {
			fatalf("parse go kind name %s: %v", e.Name(), err)
		}
		view, accentOverrides, edgeKindOverrides, err := parseSpecMD(pkgDir)
		if err != nil {
			// SPEC.md missing or no View section — skip this kind.
			continue
		}
		if view.kind == "" {
			continue
		}
		// Apply accent and edgeKind overrides from SPEC.md Ports table.
		for i, p := range ports {
			if a, ok := accentOverrides[p.id]; ok && a != "" {
				ports[i].accent = a
			}
			if ek, ok := edgeKindOverrides[p.id]; ok && ek != "" {
				ports[i].edgeKind = ek
			}
		}
		defaultData := parseDefaultData(pkgDir)
		kinds = append(kinds, kindEntry{kind: view.kind, goKind: goKind, view: view, ports: ports, dataFields: dataFields, defaultData: defaultData})
	}

	// Sort alphabetically by RF kind name (matching original generator behaviour).
	sort.Slice(kinds, func(i, j int) bool {
		return kinds[i].kind < kinds[j].kind
	})

	outPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "webview", "rf", "nodes", "node-defs.ts")
	if err := writeNodeDefs(outPath, kinds); err != nil {
		fatalf("write %s: %v", outPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d entries)\n", outPath, len(kinds))

	dataTypesPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "node-data-types.ts")
	if err := writeNodeDataTypes(dataTypesPath, kinds); err != nil {
		fatalf("write %s: %v", dataTypesPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s\n", dataTypesPath)

	loaderPath := filepath.Join(repoRoot, "nodes", "Wiring", "loader.go")
	wireProps, err := parseWirePropsFromFile(loaderPath)
	if err != nil {
		fatalf("parse wire props from loader.go: %v", err)
	}
	wireDefsPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "wire-defs.ts")
	if err := writeWireDefs(wireDefsPath, wireProps); err != nil {
		fatalf("write %s: %v", wireDefsPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d entries)\n", wireDefsPath, len(wireProps))
}

// findRepoRoot walks up from dir until it finds a directory containing "nodes/".
func findRepoRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "nodes")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasRegister reports whether any .go file in dir contains "Wiring.Register(".
func hasRegister(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte("Wiring.Register(")) {
			return true
		}
	}
	return false
}

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
	for _, files := range pkgs {
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
						for _, name := range field.Names {
							ports = append(ports, port{id: name.Name, direction: dir})
						}
					}
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
			return "out", true
		}
	}
	return "", false
}

// parseSpecMD reads SPEC.md in pkgDir and returns the view definition,
// a map of port-name → accent override, and a map of port-name → edgeKind
// from the Ports table.
func parseSpecMD(pkgDir string) (viewDef, map[string]string, map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, "SPEC.md"))
	if err != nil {
		return viewDef{}, nil, nil, err
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
		return viewDef{}, nil, nil, fmt.Errorf("no View section")
	}
	headers, rows := parseTable(viewLines)
	fieldIdx := indexOf(headers, "Field")
	valueIdx := indexOf(headers, "Value")
	if fieldIdx == -1 || valueIdx == -1 {
		return viewDef{}, nil, nil, fmt.Errorf("View table missing Field/Value columns")
	}
	vmap := map[string]string{}
	for _, row := range rows {
		if fieldIdx < len(row) && valueIdx < len(row) {
			vmap[row[fieldIdx]] = row[valueIdx]
		}
	}
	view := viewDef{
		kind:         vmap["kind"],
		bg:           vmap["bg"],
		border:       vmap["border"],
		text:         vmap["text"],
		accent:       vmap["accent"],
		minWidth:     vmap["minWidth"],
		sublabel:     vmap["sublabel"],
		displays:     vmap["displays"],
		defaultLabel: vmap["defaultLabel"],
		role:         vmap["role"],
		shape:        vmap["shape"],
		fill:         vmap["fill"],
		stroke:       vmap["stroke"],
		width:        vmap["width"],
		height:       vmap["height"],
	}

	// Parse Ports section for accent and edgeKind overrides.
	accentOverrides := map[string]string{}
	edgeKindOverrides := map[string]string{}
	portsLines := sectionLines("Ports")
	if portsLines != nil {
		headers, rows := parseTable(portsLines)
		nameIdx := indexOf(headers, "Name")
		accentIdx := indexOf(headers, "Accent")
		edgeKindIdx := indexOf(headers, "EdgeKind")
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
		}
	}

	return view, accentOverrides, edgeKindOverrides, nil
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

// writeNodeDefs emits the node-defs.ts file.
func writeNodeDefs(outPath string, kinds []kindEntry) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit. Source: nodes/<Kind>/<Kind>.go + SPEC.md`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export type DisplayKind = "queue" | "repeat" | "held";`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export interface NodePort {`)
	fmt.Fprintln(w, `  id: string;`)
	fmt.Fprintln(w, `  accent?: string;`)
	fmt.Fprintln(w, `  edgeKind?: string;`)
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export interface NodeDef {`)
	fmt.Fprintln(w, `  defaultLabel: string;`)
	fmt.Fprintln(w, `  bg: string;`)
	fmt.Fprintln(w, `  border: string;`)
	fmt.Fprintln(w, `  text: string;`)
	fmt.Fprintln(w, `  accent: string;`)
	fmt.Fprintln(w, `  minWidth?: number;`)
	fmt.Fprintln(w, `  sublabel?: string;`)
	fmt.Fprintln(w, `  targets?: NodePort[];`)
	fmt.Fprintln(w, `  sources?: NodePort[];`)
	fmt.Fprintln(w, `  displays?: DisplayKind[];`)
	fmt.Fprintln(w, `  // NodeTypeDef-compatible fields for schema/adapter consumers.`)
	fmt.Fprintln(w, `  role?: string;`)
	fmt.Fprintln(w, `  shape?: string;`)
	fmt.Fprintln(w, `  fill?: string;`)
	fmt.Fprintln(w, `  stroke?: string;`)
	fmt.Fprintln(w, `  width?: number;`)
	fmt.Fprintln(w, `  height?: number;`)
	fmt.Fprintln(w, `  inputs?: { name: string; kind: string }[];`)
	fmt.Fprintln(w, `  outputs?: { name: string; kind: string }[];`)
	fmt.Fprintln(w, `  defaultData?: Record<string, unknown>;`)
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	// Emit RUNTIME_IMPLEMENTED_KINDS from goKind names.
	fmt.Fprintln(w, `// PascalCase Go kind names that have a substrate runtime.`)
	fmt.Fprintln(w, `// Single source of truth — derived from Wiring.Register calls.`)
	fmt.Fprintf(w, "export const RUNTIME_IMPLEMENTED_KINDS: ReadonlySet<string> = new Set([\n")
	for _, e := range kinds {
		fmt.Fprintf(w, "  %q,\n", e.goKind)
	}
	fmt.Fprintln(w, `]);`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export const NODE_DEFS: Record<string, NodeDef> = {`)
	for _, e := range kinds {
		fmt.Fprintf(w, "  %s: %s,\n", e.kind, buildDef(e.view, e.ports, e.defaultData))
	}
	fmt.Fprint(w, `};`, "\n")

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

func buildDef(v viewDef, ports []port, defaultData string) string {
	targets := filterPorts(ports, "in")
	sources := filterPorts(ports, "out")

	var fields []string
	defaultLabel := v.defaultLabel
	if defaultLabel == "" {
		defaultLabel = v.kind
	}
	fields = append(fields, fmt.Sprintf(`defaultLabel: "%s"`, defaultLabel))
	fields = append(fields, fmt.Sprintf(`bg: "%s"`, v.bg))
	fields = append(fields, fmt.Sprintf(`border: "%s"`, v.border))
	fields = append(fields, fmt.Sprintf(`text: "%s"`, v.text))
	fields = append(fields, fmt.Sprintf(`accent: "%s"`, v.accent))
	if v.minWidth != "" {
		fields = append(fields, fmt.Sprintf(`minWidth: %s`, v.minWidth))
	}
	if v.sublabel != "" {
		fields = append(fields, fmt.Sprintf(`sublabel: "%s"`, v.sublabel))
	}
	if len(targets) > 0 {
		fields = append(fields, fmt.Sprintf(`targets: [%s]`, joinPorts(targets)))
	}
	if len(sources) > 0 {
		fields = append(fields, fmt.Sprintf(`sources: [%s]`, joinPorts(sources)))
	}
	if v.role != "" {
		fields = append(fields, fmt.Sprintf(`role: "%s"`, v.role))
	}
	if v.shape != "" {
		fields = append(fields, fmt.Sprintf(`shape: "%s"`, v.shape))
	}
	if v.fill != "" {
		fields = append(fields, fmt.Sprintf(`fill: "%s"`, v.fill))
	}
	if v.stroke != "" {
		fields = append(fields, fmt.Sprintf(`stroke: "%s"`, v.stroke))
	}
	if v.width != "" {
		fields = append(fields, fmt.Sprintf(`width: %s`, v.width))
	}
	if v.height != "" {
		fields = append(fields, fmt.Sprintf(`height: %s`, v.height))
	}
	// Emit typed inputs/outputs for schema/adapter consumers.
	if len(targets) > 0 {
		fields = append(fields, fmt.Sprintf(`inputs: [%s]`, joinPortsTyped(targets)))
	}
	if len(sources) > 0 {
		fields = append(fields, fmt.Sprintf(`outputs: [%s]`, joinPortsTyped(sources)))
	}
	if v.displays != "" {
		items := strings.Split(v.displays, ",")
		var quoted []string
		for _, item := range items {
			quoted = append(quoted, fmt.Sprintf(`"%s"`, strings.TrimSpace(item)))
		}
		fields = append(fields, fmt.Sprintf(`displays: [%s]`, strings.Join(quoted, ", ")))
	}
	if defaultData != "" {
		fields = append(fields, fmt.Sprintf(`defaultData: %s`, defaultData))
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

func filterPorts(ports []port, dir string) []port {
	var out []port
	for _, p := range ports {
		if p.direction == dir {
			out = append(out, p)
		}
	}
	return out
}

// goTypeToTS converts a Go type expression string to a TypeScript type string.
// Supported: int, string, bool, []int, []string, map[string]int.
// Returns an error for unsupported types.
func goTypeToTS(goType string) (string, error) {
	switch goType {
	case "int":
		return "number", nil
	case "string":
		return "string", nil
	case "bool":
		return "boolean", nil
	case "[]int":
		return "number[]", nil
	case "[]string":
		return "string[]", nil
	case "map[string]int":
		return "Record<string, number>", nil
	}
	return "", fmt.Errorf("unsupported Go type %q — add it to goTypeToTS", goType)
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
					typeStr, ok := goTypeExprStr(field.Type)
					if !ok {
						return nil, fmt.Errorf("field %v: cannot stringify type", field.Names)
					}
					fields = append(fields, dataField{wireTag: wireKey, goType: typeStr})
				}
			}
		}
	}
	return fields, nil
}

// tsValidatorBody returns a TS snippet that validates one dataField path.
// path is the dot-separated path after "data.", e.g. "init" or "initialSlots.held".
func tsValidatorSnippets(fields []dataField) []string {
	var lines []string
	for _, f := range fields {
		tsType, err := goTypeToTS(f.goType)
		if err != nil {
			// This will fail at go build time if unsupported.
			lines = append(lines, fmt.Sprintf(`    // ERROR: %v`, err))
			continue
		}
		parts := strings.Split(f.wireTag, ".")
		// Build accessor chain with type checks.
		switch len(parts) {
		case 1:
			key := parts[0]
			switch tsType {
			case "number":
				lines = append(lines, fmt.Sprintf(`    if (typeof d["%s"] !== "number") throw new ParseError(path+".data.%s: expected number");`, key, key))
			case "boolean":
				lines = append(lines, fmt.Sprintf(`    if (typeof d["%s"] !== "boolean") throw new ParseError(path+".data.%s: expected boolean");`, key, key))
			case "string":
				lines = append(lines, fmt.Sprintf(`    if (typeof d["%s"] !== "string") throw new ParseError(path+".data.%s: expected string");`, key, key))
			case "number[]":
				lines = append(lines, fmt.Sprintf(`    if (!Array.isArray(d["%s"]) || !(d["%s"] as unknown[]).every((x) => typeof x === "number")) throw new ParseError(path+".data.%s: expected number[]");`, key, key, key))
			case "string[]":
				lines = append(lines, fmt.Sprintf(`    if (!Array.isArray(d["%s"]) || !(d["%s"] as unknown[]).every((x) => typeof x === "string")) throw new ParseError(path+".data.%s: expected string[]");`, key, key, key))
			case "Record<string, number>":
				lines = append(lines, fmt.Sprintf(`    if (typeof d["%s"] !== "object" || d["%s"] === null || Array.isArray(d["%s"])) throw new ParseError(path+".data.%s: expected object");`, key, key, key, key))
			}
		case 2:
			// e.g. initialSlots.held → d.initialSlots.held
			parent := parts[0]
			child := parts[1]
			lines = append(lines, fmt.Sprintf(`    { const p = d["%s"] as Record<string, unknown>|undefined; if (!p || typeof p !== "object") throw new ParseError(path+".data.%s: expected object"); if (typeof p["%s"] !== "number") throw new ParseError(path+".data.%s.%s: expected number"); }`, parent, parent, child, parent, child))
		}
	}
	return lines
}

// writeNodeDataTypes emits the node-data-types.ts file.
func writeNodeDataTypes(outPath string, kinds []kindEntry) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit. Source: nodes/<Kind>/<Kind>.go wire:"data.*" tags.`)
	fmt.Fprintln(w, `// Validates node.data blobs against Go struct field types.`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `import { ParseError } from "./parse-primitives";`)
	fmt.Fprintln(w)

	// Emit per-kind interfaces.
	for _, e := range kinds {
		if len(e.dataFields) == 0 {
			continue
		}
		fmt.Fprintf(w, "export interface %sData {\n", e.goKind)
		// Group fields by top-level key.
		topKeys := map[string][]dataField{}
		var topOrder []string
		for _, f := range e.dataFields {
			parts := strings.SplitN(f.wireTag, ".", 2)
			key := parts[0]
			if _, exists := topKeys[key]; !exists {
				topOrder = append(topOrder, key)
			}
			topKeys[key] = append(topKeys[key], f)
		}
		for _, key := range topOrder {
			fields := topKeys[key]
			if len(fields) == 1 && !strings.Contains(fields[0].wireTag, ".") {
				tsType, _ := goTypeToTS(fields[0].goType)
				fmt.Fprintf(w, "  %s: %s;\n", key, tsType)
			} else {
				// Nested object (e.g. initialSlots).
				fmt.Fprintf(w, "  %s: {\n", key)
				for _, f := range fields {
					parts := strings.SplitN(f.wireTag, ".", 2)
					child := parts[1]
					tsType, _ := goTypeToTS(f.goType)
					fmt.Fprintf(w, "    %s: %s;\n", child, tsType)
				}
				fmt.Fprintln(w, "  };")
			}
		}
		fmt.Fprintln(w, "}")
		fmt.Fprintln(w)
	}

	// Emit parseNodeData function.
	fmt.Fprintln(w, `// parseNodeData validates node.data for known kinds. Unknown kinds pass through.`)
	fmt.Fprintln(w, `// Throws ParseError if the data shape does not match the Go struct.`)
	fmt.Fprintln(w, `export function parseNodeData(kind: string, data: unknown, path: string): unknown {`)
	fmt.Fprintln(w, `  if (data === undefined || data === null) return data;`)
	fmt.Fprintln(w, `  switch (kind) {`)
	for _, e := range kinds {
		if len(e.dataFields) == 0 {
			continue
		}
		fmt.Fprintf(w, "    case %q: {\n", e.goKind)
		fmt.Fprintln(w, `      if (typeof data !== "object" || Array.isArray(data)) throw new ParseError(path+".data: expected object");`)
		fmt.Fprintln(w, `      const d = data as Record<string, unknown>;`)
		snippets := tsValidatorSnippets(e.dataFields)
		for _, s := range snippets {
			fmt.Fprintln(w, s)
		}
		fmt.Fprintln(w, `      return data;`)
		fmt.Fprintln(w, `    }`)
	}
	fmt.Fprintln(w, `    default:`)
	fmt.Fprintln(w, `      return data;`)
	fmt.Fprintln(w, `  }`)
	fmt.Fprintln(w, `}`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// parseWirePropsFromFile parses wire:"prop,..." tags on fields of specEdge
// in the given Go source file and returns them in declaration order.
func parseWirePropsFromFile(filePath string) ([]wireProp, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		return nil, err
	}
	var props []wireProp
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "specEdge" {
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
				rawTag := strings.Trim(field.Tag.Value, "`")
				// Extract wire tag value.
				wireVal, _, hasWire := strings.Cut(rawTag, `wire:"`)
				if !hasWire {
					// Try alternate: tag may not start with wire
					_, after, found := strings.Cut(rawTag, `wire:"`)
					if !found {
						continue
					}
					wireVal = after
				} else {
					_ = wireVal
					_, after, found := strings.Cut(rawTag, `wire:"`)
					if !found {
						continue
					}
					wireVal = after
				}
				wireVal, _, _ = strings.Cut(wireVal, `"`)
				if !strings.HasPrefix(wireVal, "prop,") {
					continue
				}
				// Parse segments: prop, optional|required, tsType:X
				segments := strings.Split(wireVal, ",")
				if len(segments) < 3 {
					continue
				}
				required := segments[1] == "required"
				tsType := ""
				for _, seg := range segments[2:] {
					if after, found := strings.CutPrefix(seg, "tsType:"); found {
						tsType = after
					}
				}
				if tsType == "" {
					continue
				}
				// Extract json name.
				jsonName := ""
				_, after, found := strings.Cut(rawTag, `json:"`)
				if found {
					jsonName, _, _ = strings.Cut(after, `"`)
					// Strip omitempty etc.
					jsonName, _, _ = strings.Cut(jsonName, ",")
				}
				if jsonName == "" && len(field.Names) > 0 {
					jsonName = strings.ToLower(field.Names[0].Name[:1]) + field.Names[0].Name[1:]
				}
				props = append(props, wireProp{jsonName: jsonName, tsType: tsType, required: required})
			}
		}
	}
	return props, nil
}

// writeWireDefs emits wire-defs.ts from the parsed wireProp slice.
func writeWireDefs(outPath string, props []wireProp) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by: go run ./tools/gen-node-defs (regenerate after editing wire:"..." tags in nodes/Wiring/loader.go)`)
	fmt.Fprintln(w, `// Source: nodes/Wiring/loader.go wire:"prop,..." tags. Do not edit by hand.`)
	fmt.Fprintln(w)
	// Collect non-primitive tsTypes that need importing from ./types.
	primitives := map[string]bool{"string": true, "number": true, "boolean": true}
	seen := map[string]bool{}
	var nonPrim []string
	for _, p := range props {
		t := p.tsType
		if primitives[t] || seen[t] {
			continue
		}
		seen[t] = true
		nonPrim = append(nonPrim, t)
	}
	sort.Strings(nonPrim)
	if len(nonPrim) > 0 {
		fmt.Fprintf(w, "import type { %s } from \"./types\";\n", strings.Join(nonPrim, ", "))
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, `export interface WirePropDef {`)
	fmt.Fprintln(w, `  tsType: string;`)
	fmt.Fprintln(w, `  required: boolean;`)
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export const WIRE_PROPS: Record<string, WirePropDef> = {`)
	for _, p := range props {
		req := "false"
		if p.required {
			req = "true"
		}
		fmt.Fprintf(w, "  %-12s { tsType: %-12s required: %s },\n",
			p.jsonName+":", `"`+p.tsType+`",`, req)
	}
	fmt.Fprintln(w, `};`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export type WirePropName = keyof typeof WIRE_PROPS;`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// Derived from WIRE_PROPS — do not hand-edit. Consumed by Edge and EdgeData.`)
	fmt.Fprintln(w, `export type WireProps = {`)
	for _, p := range props {
		opt := "?"
		if p.required {
			opt = ""
		}
		fmt.Fprintf(w, "  %s%s: %s;\n", p.jsonName, opt, p.tsType)
	}
	fmt.Fprintln(w, `};`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-node-defs: "+format+"\n", args...)
	os.Exit(1)
}

func joinPorts(ports []port) string {
	var parts []string
	for _, p := range ports {
		if p.accent != "" && p.edgeKind != "" {
			parts = append(parts, fmt.Sprintf(`{ id: "%s", accent: "%s", edgeKind: "%s" }`, p.id, p.accent, p.edgeKind))
		} else if p.accent != "" {
			parts = append(parts, fmt.Sprintf(`{ id: "%s", accent: "%s" }`, p.id, p.accent))
		} else if p.edgeKind != "" {
			parts = append(parts, fmt.Sprintf(`{ id: "%s", edgeKind: "%s" }`, p.id, p.edgeKind))
		} else {
			parts = append(parts, fmt.Sprintf(`{ id: "%s" }`, p.id))
		}
	}
	return strings.Join(parts, ", ")
}

// joinPortsTyped emits {name, kind} pairs for NodeTypeDef-compatible consumers.
func joinPortsTyped(ports []port) string {
	var parts []string
	for _, p := range ports {
		ek := p.edgeKind
		if ek == "" {
			ek = "chain" // default
		}
		parts = append(parts, fmt.Sprintf(`{ name: "%s", kind: "%s" }`, p.id, ek))
	}
	return strings.Join(parts, ", ")
}
