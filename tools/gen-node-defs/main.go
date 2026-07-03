// gen-node-defs walks nodes/*/ and emits src/schema/node-defs.ts.
// Port names and directions are derived from Go channel-typed struct fields.
// View metadata and per-port accent overrides are read from SPEC.md.
// Run: go run ../../tools/gen-node-defs (from tools/topology-vscode/)
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
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
	isMulti   bool   // true when the Go type is Wiring.OutMulti
	optional  bool   // true when SPEC.md Ports table marks this port Optional=yes
}

// dataField represents a wire:"data.*" tagged struct field.
type dataField struct {
	wireTag   string // full tag value after wire:"data." prefix, e.g. "init" or "state"
	goType    string // Go type string, e.g. "[]int", "int", "string"
	fieldName string // Go struct field name (used for wire:"data.state" key derivation)
}

// viewDef holds the SPEC.md ## View section fields.
type viewDef struct {
	kind         string
	bg           string
	border       string
	text         string
	accent       string
	minWidth     string
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
	seenGoKind := map[string]string{} // goKind → dir name that registered it
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
		// Fallback: if AST found no ports (e.g. all ports are in an embedded struct
		// from another package), read them from the SPEC.md Ports table.
		if len(ports) == 0 {
			ports = parsePortsFromSpec(pkgDir)
		}
		dataFields, err := parseDataFieldsFromAST(pkgDir)
		if err != nil {
			fatalf("parse data fields %s: %v", e.Name(), err)
		}
		goKind, err := parseGoKindName(pkgDir)
		if err != nil {
			fatalf("parse go kind name %s: %v", e.Name(), err)
		}
		// A duplicate goKind across dirs produces a silent last-wins duplicate TS
		// object key in node-defs.ts; reject it here naming both dirs.
		if prev, dup := seenGoKind[goKind]; dup {
			fatalf("duplicate kind name %q registered by both %q and %q", goKind, prev, e.Name())
		}
		seenGoKind[goKind] = e.Name()
		view, accentOverrides, edgeKindOverrides, optionalPorts, err := parseSpecMD(pkgDir)
		if err != nil {
			// This dir has a Wiring.Register (a real node package), so a missing or
			// broken SPEC.md View section is a half-landed kind — fail loudly rather
			// than silently dropping the kind from all generated files.
			fatalf("kind %q registers a Go runtime but its SPEC.md View section is missing/broken: %v", e.Name(), err)
		}
		if view.kind == "" {
			fatalf("kind %q registers a Go runtime but its SPEC.md ## View has an empty view.kind", e.Name())
		}
		// Apply accent, edgeKind overrides, and optional flags from SPEC.md Ports table.
		for i, p := range ports {
			if a, ok := accentOverrides[p.id]; ok && a != "" {
				ports[i].accent = a
			}
			if ek, ok := edgeKindOverrides[p.id]; ok && ek != "" {
				ports[i].edgeKind = ek
			}
			if optionalPorts[p.id] {
				ports[i].optional = true
			}
		}
		defaultData := parseDefaultData(pkgDir)
		kinds = append(kinds, kindEntry{kind: view.kind, goKind: goKind, view: view, ports: ports, dataFields: dataFields, defaultData: defaultData})
	}

	// Sort alphabetically by Go kind name (PascalCase spec kind).
	sort.Slice(kinds, func(i, j int) bool {
		return kinds[i].goKind < kinds[j].goKind
	})

	outPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "node-defs.ts")
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

	traceDir := filepath.Join(repoRoot, "Trace")
	traceGoPath := filepath.Join(traceDir, "Trace.go")
	traceKinds, err := parseTraceKinds(traceDir)
	if err != nil {
		fatalf("parse trace kinds: %v", err)
	}
	traceKindsPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "trace-kinds.ts")
	if err := writeTraceKinds(traceKindsPath, traceKinds); err != nil {
		fatalf("write %s: %v", traceKindsPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d kinds)\n", traceKindsPath, len(traceKinds))

	// Field-level parity for the node-status trace event: parse the local struct
	// Trace.go's MarshalJSON marshals for KindNodeStatus and emit a generated TS
	// payload type + runtime validator, so a Go field rename/retype regenerates and
	// breaks tsc (via messages.ts) instead of silently casting through.
	nodeStatusFields, err := parseNodeStatusFields(traceGoPath)
	if err != nil {
		fatalf("parse node-status fields: %v", err)
	}
	traceFieldsPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "trace-event-fields.ts")
	if err := writeTraceEventFields(traceFieldsPath, nodeStatusFields); err != nil {
		fatalf("write %s: %v", traceFieldsPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d node-status fields)\n", traceFieldsPath, len(nodeStatusFields))

	nodeDimsGoPath := filepath.Join(repoRoot, "nodes", "Wiring", "node_dims_gen.go")
	if err := writeNodeDims(nodeDimsGoPath, kinds); err != nil {
		fatalf("write %s: %v", nodeDimsGoPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d kinds)\n", nodeDimsGoPath, len(kinds))

	nodeKindIDGoPath := filepath.Join(repoRoot, "Buffer", "node_kind_id_gen.go")
	if err := writeNodeKindID(nodeKindIDGoPath, kinds); err != nil {
		fatalf("write %s: %v", nodeKindIDGoPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d kinds)\n", nodeKindIDGoPath, len(kinds))

	curveParamsGoPath := filepath.Join(repoRoot, "nodes", "Wiring", "curve_params.go")
	curveParams, err := parseCurveParams(curveParamsGoPath)
	if err != nil {
		fatalf("parse curve params: %v", err)
	}
	curveParamsTsPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "curve-params.ts")
	if err := writeCurveParams(curveParamsTsPath, curveParams); err != nil {
		fatalf("write %s: %v", curveParamsTsPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d constants)\n", curveParamsTsPath, len(curveParams))

	messagesTSPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "messages.ts")
	overlayFlags, err := parseOverlayFlags(messagesTSPath)
	if err != nil {
		fatalf("parse overlay flags: %v", err)
	}
	overlayGenGoPath := filepath.Join(repoRoot, "nodes", "Wiring", "overlay_gen.go")
	if err := writeOverlayGen(overlayGenGoPath, overlayFlags); err != nil {
		fatalf("write %s: %v", overlayGenGoPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d overlay flags)\n", overlayGenGoPath, len(overlayFlags))

	shadingParamsGoPath := filepath.Join(repoRoot, "nodes", "Wiring", "shading_params.go")
	shadingParams, err := parseShadingParams(shadingParamsGoPath)
	if err != nil {
		fatalf("parse shading params: %v", err)
	}
	shadingParamsTsPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "shading-params.ts")
	if err := writeShadingParams(shadingParamsTsPath, shadingParams); err != nil {
		fatalf("write %s: %v", shadingParamsTsPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d constants)\n", shadingParamsTsPath, len(shadingParams))

	bufLayoutGoPath := filepath.Join(repoRoot, "Buffer", "layout.go")
	bufSchema, err := parseBufferLayout(bufLayoutGoPath)
	if err != nil {
		fatalf("parse buffer layout: %v", err)
	}
	bufLayoutGenGoPath := filepath.Join(repoRoot, "Buffer", "buffer_layout_gen.go")
	if err := writeBufferLayoutGo(bufLayoutGenGoPath, bufSchema); err != nil {
		fatalf("write %s: %v", bufLayoutGenGoPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d blocks, %d events)\n", bufLayoutGenGoPath, len(bufSchema.blocks), len(bufSchema.events))

	bufLayoutTSPath := filepath.Join(repoRoot, "tools", "topology-vscode", "src", "schema", "buffer-layout.ts")
	if err := writeBufferLayoutTS(bufLayoutTSPath, bufSchema); err != nil {
		fatalf("write %s: %v", bufLayoutTSPath, err)
	}
	fmt.Fprintf(os.Stderr, "gen-node-defs: wrote %s (%d blocks, %d events)\n", bufLayoutTSPath, len(bufSchema.blocks), len(bufSchema.events))

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
		kind:         vmap["kind"],
		bg:           vmap["bg"],
		border:       vmap["border"],
		text:         vmap["text"],
		accent:       vmap["accent"],
		minWidth:     vmap["minWidth"],
		displays:     vmap["displays"],
		defaultLabel: vmap["defaultLabel"],
		role:         vmap["role"],
		shape:        vmap["shape"],
		fill:         vmap["fill"],
		stroke:       vmap["stroke"],
		width:        vmap["width"],
		height:       vmap["height"],
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

// writeNodeDefs emits the node-defs.ts file.
func writeNodeDefs(outPath string, kinds []kindEntry) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit. Source: nodes/<Kind>/<Kind>.go + SPEC.md`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export interface NodeDef {`)
	fmt.Fprintln(w, `  bg: string;`)
	fmt.Fprintln(w, `  border: string;`)
	fmt.Fprintln(w, `  text: string;`)
	fmt.Fprintln(w, `  minWidth?: number;`)
	fmt.Fprintln(w, `  // NodeTypeDef-compatible fields for schema/adapter consumers.`)
	fmt.Fprintln(w, `  role?: string;`)
	fmt.Fprintln(w, `  shape?: string;`)
	fmt.Fprintln(w, `  fill?: string;`)
	fmt.Fprintln(w, `  stroke?: string;`)
	fmt.Fprintln(w, `  width?: number;`)
	fmt.Fprintln(w, `  height?: number;`)
	fmt.Fprintln(w, `  inputs?: { name: string; kind: string; isMulti?: boolean }[];`)
	fmt.Fprintln(w, `  outputs?: { name: string; kind: string; isMulti?: boolean }[];`)
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	// Emit RUNTIME_IMPLEMENTED_KINDS from goKind names.
	fmt.Fprintln(w, `// PascalCase Go kind names that have a Go runtime.`)
	fmt.Fprintln(w, `// Single source of truth — derived from Wiring.Register calls.`)
	fmt.Fprintf(w, "export const RUNTIME_IMPLEMENTED_KINDS: ReadonlySet<string> = new Set([\n")
	for _, e := range kinds {
		fmt.Fprintf(w, "  %q,\n", e.goKind)
	}
	fmt.Fprintln(w, `]);`)
	fmt.Fprintln(w)
	// Pre-build the def strings so we can reuse them for both NODE_DEFS and NODE_DEFS_ARRAY.
	type kindDef struct {
		goKind string
		def    string
	}
	defs := make([]kindDef, len(kinds))
	for i, e := range kinds {
		defs[i] = kindDef{goKind: e.goKind, def: buildDef(e.view, e.ports)}
	}
	fmt.Fprintln(w, `export const NODE_DEFS: Record<string, NodeDef> = {`)
	for _, kd := range defs {
		fmt.Fprintf(w, "  %s: %s,\n", kd.goKind, kd.def)
	}
	fmt.Fprint(w, `};`, "\n")
	fmt.Fprintln(w)
	// NODE_DEFS_ARRAY: entries in the same alphabetical Go-kind order as NODE_DEFS,
	// matching the KindId index produced by Go's kindIDMap (Buffer/node_kind_id_gen.go).
	// Index i here ↔ KindId i in the buffer node block. Emitting literals directly
	// (not NODE_DEFS[key]) so tsc with noUncheckedIndexedAccess can't widen to NodeDef|undefined.
	fmt.Fprintf(w, "export const NODE_DEFS_ARRAY: readonly NodeDef[] = [\n")
	for _, kd := range defs {
		fmt.Fprintf(w, "  %s,\n", kd.def)
	}
	fmt.Fprintln(w, `];`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

func buildDef(v viewDef, ports []port) string {
	targets := filterPorts(ports, "in")
	sources := filterPorts(ports, "out")

	var fields []string
	fields = append(fields, fmt.Sprintf(`bg: "%s"`, v.bg))
	fields = append(fields, fmt.Sprintf(`border: "%s"`, v.border))
	fields = append(fields, fmt.Sprintf(`text: "%s"`, v.text))
	if v.minWidth != "" {
		fields = append(fields, fmt.Sprintf(`minWidth: %s`, v.minWidth))
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

// tsValidatorBody returns a TS snippet that validates one dataField path.
// path is the dot-separated path after "data.", e.g. "init" or "state".
func tsValidatorSnippets(fields []dataField) []string {
	var lines []string
	for _, f := range fields {
		tsType, err := goTypeToTS(f.goType)
		if err != nil {
			// This will fail at go build time if unsupported.
			lines = append(lines, fmt.Sprintf(`    // ERROR: %v`, err))
			continue
		}
		// wire:"data.state" — key is lowerFirst(fieldName), value is always int.
		if f.wireTag == "state" {
			key := strings.ToLower(f.fieldName[:1]) + f.fieldName[1:]
			lines = append(lines, fmt.Sprintf(`    { const p = d["state"] as Record<string, unknown>|undefined; if (!p || typeof p !== "object") throw new ParseError(path+".data.state: expected object"); if (typeof p["%s"] !== "number") throw new ParseError(path+".data.state.%s: expected number"); }`, key, key))
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
			if key == "state" {
				// wire:"data.state" — each field maps to state[lowerFirst(fieldName)].
				fmt.Fprintf(w, "  %s: {\n", key)
				for _, f := range fields {
					childKey := strings.ToLower(f.fieldName[:1]) + f.fieldName[1:]
					tsType, _ := goTypeToTS(f.goType)
					fmt.Fprintf(w, "    %s: %s;\n", childKey, tsType)
				}
				fmt.Fprintln(w, "  };")
			} else if len(fields) == 1 && !strings.Contains(fields[0].wireTag, ".") {
				tsType, _ := goTypeToTS(fields[0].goType)
				fmt.Fprintf(w, "  %s: %s;\n", key, tsType)
			} else {
				// Nested object (dot-separated path).
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
			// Explicit no-data case: the Go struct carries no wire:"data.*" fields,
			// so there is nothing to validate. Emitting the case (instead of letting
			// the kind fall through to default) keeps parseNodeData exhaustive over
			// every RUNTIME_IMPLEMENTED_KIND. If a data field is later added to this
			// kind, the generator replaces this with a validating case automatically,
			// so a new field can never silently bypass validation.
			fmt.Fprintf(w, "    case %q:\n", e.goKind)
			fmt.Fprintln(w, `      return data; // no wire:"data.*" fields on the Go struct`)
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
				// Extract wire tag value (text after `wire:"` up to the closing quote).
				_, wireVal, hasWire := strings.Cut(rawTag, `wire:"`)
				if !hasWire {
					continue
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

// parseTraceKinds scans EVERY non-test *.go file under the Trace/ dir and returns
// the string values of all Kind* constants (e.g. "recv", "fire", "send", "slot").
// Scanning the whole dir (not just Trace.go) means a Kind* const declared in any
// sibling file under Trace/ is still picked up — the single-file-path guard-blindness
// class (memory: feedback_guards_hardcoding_single_file_break_on_split).
func parseTraceKinds(traceDir string) ([]string, error) {
	entries, err := os.ReadDir(traceDir)
	if err != nil {
		return nil, err
	}
	var kinds []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(traceDir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}
			for _, spec := range genDecl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, nm := range vs.Names {
					if !strings.HasPrefix(nm.Name, "Kind") {
						continue
					}
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					kinds = append(kinds, strings.Trim(lit.Value, `"`))
				}
			}
		}
	}
	// No sort: os.ReadDir yields filenames in lexical order and go/ast preserves
	// in-file declaration order, so the emitted slice is deterministic across runs.
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no Kind* constants found in %s", traceDir)
	}
	return kinds, nil
}

// writeTraceKinds emits trace-kinds.ts from the parsed kind slice.
func writeTraceKinds(outPath string, kinds []string) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: Trace/Trace.go Kind* constants. Regenerate with: npm run gen:node-defs`)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "export const TRACE_EVENT_KINDS = [")
	for i, k := range kinds {
		if i > 0 {
			fmt.Fprint(w, ", ")
		}
		fmt.Fprintf(w, "%q", k)
	}
	fmt.Fprintln(w, "] as const;")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export type TraceEventKind = (typeof TRACE_EVENT_KINDS)[number];`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// traceField is one field of a trace event's JSON payload struct.
type traceField struct {
	jsonName   string // from json:"..." tag
	tsType     string // TS type: "number" | "string" | "boolean"
	typeofName string // JS typeof result: "number" | "string" | "boolean"
}

// goTypeToTraceTS maps a Go scalar type (as used in the Trace.go MarshalJSON
// payload structs) to its TS type + JS typeof string. Only the scalar types the
// trace payloads actually use are supported; anything else is a hard error so a
// new field type is caught at generate time rather than silently skipped.
func goTypeToTraceTS(goType string) (tsType, typeofName string, err error) {
	switch goType {
	case "int", "float64":
		return "number", "number", nil
	case "string":
		return "string", "string", nil
	case "bool":
		return "boolean", "boolean", nil
	}
	return "", "", fmt.Errorf("unsupported trace field Go type %q — add it to goTypeToTraceTS", goType)
}

// parseNodeStatusFields reads Trace.go and returns the fields of the local
// `nodeStatus` struct declared inside MarshalJSON's `case KindNodeStatus:` block,
// in declaration order. This is the authoritative wire shape of the node-status
// trace event.
func parseNodeStatusFields(traceGoPath string) ([]traceField, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, traceGoPath, nil, 0)
	if err != nil {
		return nil, err
	}
	var fields []traceField
	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name == nil || ts.Name.Name != "nodeStatus" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		found = true
		for _, field := range st.Fields.List {
			if field.Tag == nil || len(field.Names) == 0 {
				continue
			}
			tag := strings.Trim(field.Tag.Value, "`")
			_, after, ok := strings.Cut(tag, `json:"`)
			if !ok {
				continue
			}
			jsonName, _, _ := strings.Cut(after, `"`)
			jsonName, _, _ = strings.Cut(jsonName, ",")
			if jsonName == "" {
				continue
			}
			goType, ok := goTypeExprStr(field.Type)
			if !ok {
				continue
			}
			tsType, typeofName, err2 := goTypeToTraceTS(goType)
			if err2 != nil {
				err = err2
				return false
			}
			fields = append(fields, traceField{jsonName: jsonName, tsType: tsType, typeofName: typeofName})
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("nodeStatus struct not found in %s (MarshalJSON case KindNodeStatus)", traceGoPath)
	}
	return fields, nil
}

// writeTraceEventFields emits trace-event-fields.ts: the generated node-status
// payload interface + a runtime field-level validator. Generated from Trace.go so
// a Go field change regenerates the TS shape (breaking tsc where messages.ts /
// pump.ts reference the fields) and the validator drops malformed Go payloads.
func writeTraceEventFields(outPath string, fields []traceField) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: Trace/Trace.go MarshalJSON node-status payload struct.`)
	fmt.Fprintln(w, `// Regenerate with: npm run gen:node-defs`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `export interface NodeStatusEvent {`)
	for _, fld := range fields {
		if fld.jsonName == "kind" {
			fmt.Fprintln(w, `  kind: "node-status";`)
			continue
		}
		fmt.Fprintf(w, "  %s: %s;\n", fld.jsonName, fld.tsType)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "/** Field-level validator for the node-status trace payload, generated from")
	fmt.Fprintln(w, " *  Trace.go field-for-field. `step` is validated by the caller (numeric")
	fmt.Fprintln(w, " *  envelope) and `kind` by the TRACE_EVENT_KINDS set, so only the payload")
	fmt.Fprintln(w, " *  fields are checked here. Malformed Go payloads are dropped, not cast. */")
	fmt.Fprintln(w, `export function validateNodeStatusFields(obj: Record<string, unknown>): boolean {`)
	fmt.Fprintln(w, `  return (`)
	var checks []string
	for _, fld := range fields {
		if fld.jsonName == "step" || fld.jsonName == "kind" {
			continue
		}
		checks = append(checks, fmt.Sprintf(`    typeof obj[%q] === %q`, fld.jsonName, fld.typeofName))
	}
	fmt.Fprintln(w, strings.Join(checks, " &&\n"))
	fmt.Fprintln(w, `  );`)
	fmt.Fprintln(w, `}`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// writeNodeDims emits nodes/Wiring/node_dims_gen.go: a kind → render width/height
// map sourced from each kind's SPEC.md ## View width/height fields. The Go
// Go uses these to mirror nodeRadius/nodeWorldPos in geometry-helpers.ts
// when computing port-to-port arc length. Single source of truth = SPEC.md.
func writeNodeDims(outPath string, kinds []kindEntry) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: nodes/<Kind>/SPEC.md ## View width/height fields.`)
	fmt.Fprintln(w, `// Regenerate with: cd tools/topology-vscode && npm run gen:node-defs`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `package Wiring`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// kindDim is the render width/height for one node kind, mirroring`)
	fmt.Fprintln(w, `// NODE_DEFS[kind].width/height in node-defs.ts.`)
	fmt.Fprintln(w, `type kindDim struct{ Width, Height float64 }`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// kindDims maps each runtime kind to its render dimensions.`)
	fmt.Fprintln(w, `var kindDims = map[string]kindDim{`)
	for _, e := range kinds {
		width := e.view.width
		height := e.view.height
		if width == "" {
			width = "110"
		}
		if height == "" {
			height = "60"
		}
		fmt.Fprintf(w, "\t%q: {Width: %s, Height: %s},\n", e.goKind, width, height)
	}
	fmt.Fprintln(w, `}`)

	w.Flush()
	// gofmt the generated Go so the output is canonical and the repo-wide
	// gofmt guard (tools/check-gofmt.sh) stays in agreement with check-generated.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format node_dims_gen.go: %w", err)
	}
	return os.WriteFile(outPath, formatted, 0644)
}

// writeNodeKindID emits Buffer/node_kind_id_gen.go: a kind → uint8 index map so
// Go's SnapshotState can populate the KindId column in the buffer node block.
// The index is 0-based and follows the same alphabetical Go-kind sort order as
// NODE_DEFS_ARRAY in node-defs.ts, guaranteeing Go and TS use the same numbering.
func writeNodeKindID(outPath string, kinds []kindEntry) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: nodes/<Kind>/SPEC.md + Wiring.Register calls.`)
	fmt.Fprintln(w, `// Regenerate with: cd tools/topology-vscode && npm run gen:node-defs`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `package Buffer`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// KindIDUnknown is the sentinel KindId value when a node's kind is not in kindIDMap.`)
	fmt.Fprintln(w, `const KindIDUnknown uint8 = 0xFF`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// kindIDMap maps a node's Go kind name (PascalCase) to its 0-based index`)
	fmt.Fprintln(w, `// in the alphabetically-sorted NODE_DEFS_ARRAY emitted by the same generator.`)
	fmt.Fprintln(w, `// Index i here ↔ NODE_DEFS_ARRAY[i] on the TS side.`)
	fmt.Fprintln(w, `var kindIDMap = map[string]uint8{`)
	for i, e := range kinds {
		fmt.Fprintf(w, "\t%q: %d,\n", e.goKind, i)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// NodeKindID returns the buffer KindId for a node's Go kind string.`)
	fmt.Fprintln(w, `// Returns KindIDUnknown (0xFF) when the kind is not in the registry.`)
	fmt.Fprintln(w, `func NodeKindID(kind string) uint8 {`)
	fmt.Fprintln(w, `	if id, ok := kindIDMap[kind]; ok {`)
	fmt.Fprintln(w, `		return id`)
	fmt.Fprintln(w, `	}`)
	fmt.Fprintln(w, `	return KindIDUnknown`)
	fmt.Fprintln(w, `}`)

	w.Flush()
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format node_kind_id_gen.go: %w", err)
	}
	return os.WriteFile(outPath, formatted, 0644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-node-defs: "+format+"\n", args...)
	os.Exit(1)
}

// curveParam is one exported constant from curve_params.go with a "CurveParam"
// name prefix.
type curveParam struct {
	tsName string // TS export name (SCREAMING_SNAKE, CurveParam prefix stripped)
	value  string // raw literal value (string or numeric)
	isInt  bool   // true if the literal contains no decimal point
}

// parseCurveParams reads the Go source at goPath and returns all top-level
// const declarations whose names start with "CurveParam".
func parseCurveParams(goPath string) ([]curveParam, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goPath, nil, 0)
	if err != nil {
		return nil, err
	}
	var params []curveParam
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "CurveParam") {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				raw := lit.Value
				// Strip surrounding quotes from string literals.
				if lit.Kind == token.STRING {
					raw = strings.Trim(raw, `"`)
				}
				// Convert Go name CurveParamFooBar → TS name CURVE_PARAM_FOO_BAR
				tsName := camelToScreamingSnake(name.Name)
				isInt := lit.Kind == token.INT
				params = append(params, curveParam{tsName: tsName, value: raw, isInt: isInt})
			}
		}
	}
	if len(params) == 0 {
		return nil, fmt.Errorf("no CurveParam* constants found in %s", goPath)
	}
	return params, nil
}

// camelToScreamingSnake converts PascalCase/camelCase to SCREAMING_SNAKE_CASE.
// e.g. CurveParamBulgeFactor → CURVE_PARAM_BULGE_FACTOR
// Inserts '_' before an uppercase letter only when the PRECEDING rune was
// lowercase, so abbreviations like "BeadID" → "BEAD_ID" and "CX" → "CX"
// stay intact (consecutive uppercase letters are NOT split).
func camelToScreamingSnake(s string) string {
	runes := []rune(s)
	var out []rune
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			if prev >= 'a' && prev <= 'z' {
				out = append(out, '_')
			}
		}
		if r >= 'a' && r <= 'z' {
			out = append(out, r-32) // to upper
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// writeCurveParams emits curve-params.ts from the parsed curveParam slice.
func writeCurveParams(outPath string, params []curveParam) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: nodes/Wiring/curve_params.go CurveParam* constants.`)
	fmt.Fprintln(w, `// Regenerate with: npm run gen:node-defs`)
	fmt.Fprintln(w)
	for _, p := range params {
		if p.isInt {
			fmt.Fprintf(w, "export const %s = %s;\n", p.tsName, p.value)
		} else {
			fmt.Fprintf(w, "export const %s = %s;\n", p.tsName, p.value)
		}
	}

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// shadingParam is one exported constant from shading_params.go with a
// "ShadingParam" name prefix. Unlike curveParam, shading params include string
// literals (hex colors), so isStr drives quoting in the emitted TS.
type shadingParam struct {
	tsName string // TS export name (SCREAMING_SNAKE, ShadingParam prefix stripped)
	value  string // raw literal value (unquoted for strings)
	isStr  bool   // true if the Go literal is a string (emit quoted in TS)
}

// parseShadingParams reads the Go source at goPath and returns all top-level
// const declarations whose names start with "ShadingParam". Mirrors
// parseCurveParams but records string-ness so writeShadingParams can quote
// color literals correctly.
func parseShadingParams(goPath string) ([]shadingParam, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goPath, nil, 0)
	if err != nil {
		return nil, err
	}
	var params []shadingParam
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "ShadingParam") {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					continue
				}
				raw := lit.Value
				isStr := lit.Kind == token.STRING
				if isStr {
					raw = strings.Trim(raw, `"`)
				}
				tsName := camelToScreamingSnake(name.Name)
				params = append(params, shadingParam{tsName: tsName, value: raw, isStr: isStr})
			}
		}
	}
	if len(params) == 0 {
		return nil, fmt.Errorf("no ShadingParam* constants found in %s", goPath)
	}
	return params, nil
}

// writeShadingParams emits shading-params.ts from the parsed shadingParam slice.
// String literals (hex colors) are emitted quoted; numeric literals raw.
func writeShadingParams(outPath string, params []shadingParam) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: nodes/Wiring/shading_params.go ShadingParam* constants.`)
	fmt.Fprintln(w, `// Regenerate with: npm run gen:node-defs`)
	fmt.Fprintln(w, `//`)
	fmt.Fprintln(w, `// Go owns the shading PARAMETER values (MODEL.md). TS reads these and applies`)
	fmt.Fprintln(w, `// them to GPU materials / bakes the env map from them — no shading values of its`)
	fmt.Fprintln(w, `// own. The GPU machinery (three.js materials, PMREM bake, binding) stays in TS.`)
	fmt.Fprintln(w)
	for _, p := range params {
		if p.isStr {
			fmt.Fprintf(w, "export const %s = %q;\n", p.tsName, p.value)
		} else {
			fmt.Fprintf(w, "export const %s = %s;\n", p.tsName, p.value)
		}
	}

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// overlayFlag is one entry of the OVERLAY_FLAG_NAMES vocabulary with the mechanical
// Go names derived from (or overridden for) its camelCase flag string.
type overlayFlag struct {
	flag       string // camelCase wire flag, e.g. "tori"
	field      string // overlayState bool field, e.g. "sceneToriVisible"
	method     string // Toggle/Emit/Trace method basename, e.g. "SceneTori"
	breadcrumb string // Breadcrumb scope arg on Toggle ("scene"/"nodes"); "" = uniform flip
	accessor   bool   // emit a bare bool accessor method (only angleLabels)
	defaultOn  bool   // startup default value
}

// overlayOverride names the per-flag deviations from the uniform derivation. Kept
// data-driven (per the task's option 1) so the deviating flags are still generated,
// just with their extra behavior. Any flag absent here is fully uniform.
type overlayOverride struct {
	field, method, breadcrumb string
	accessor, defaultOff      bool
}

var overlayOverrides = map[string]overlayOverride{
	"tori":        {field: "sceneToriVisible", method: "SceneTori"},
	"scenePoles":  {breadcrumb: "scene"},
	"nodePoles":   {breadcrumb: "nodes"},
	"angleLabels": {accessor: true},
	"overlays":    {method: "OverlaysVis"},
	"doubleLinks": {defaultOff: true},
}

// parseOverlayFlags reads the OVERLAY_FLAG_NAMES const in messages.ts (bounded by the
// OVERLAY_FLAGS_START / OVERLAY_FLAGS_END sentinels) and returns the flag metadata in
// source order, applying overlayOverrides for the deviating flags.
func parseOverlayFlags(messagesPath string) ([]overlayFlag, error) {
	data, err := os.ReadFile(messagesPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	start, end := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "OVERLAY_FLAGS_START") {
			start = i
		} else if strings.Contains(l, "OVERLAY_FLAGS_END") {
			end = i
			break
		}
	}
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("OVERLAY_FLAGS_START/END sentinels not found in %s", messagesPath)
	}
	strLit := regexp.MustCompile(`"([A-Za-z][A-Za-z0-9]*)"`)
	var flags []overlayFlag
	seen := map[string]bool{}
	for _, l := range lines[start+1 : end] {
		m := strLit.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		name := m[1]
		seen[name] = true
		of := overlayFlag{
			flag:      name,
			field:     name + "Visible",
			method:    strings.ToUpper(name[:1]) + name[1:],
			defaultOn: true,
		}
		if ov, ok := overlayOverrides[name]; ok {
			if ov.field != "" {
				of.field = ov.field
			}
			if ov.method != "" {
				of.method = ov.method
			}
			of.breadcrumb = ov.breadcrumb
			of.accessor = ov.accessor
			if ov.defaultOff {
				of.defaultOn = false
			}
		}
		flags = append(flags, of)
	}
	if len(flags) == 0 {
		return nil, fmt.Errorf("no overlay flags parsed from %s", messagesPath)
	}
	// Every overlayOverrides key MUST name a real flag in OVERLAY_FLAG_NAMES. A typo
	// in an override key (e.g. "tori" mistyped "toriz") would otherwise silently fall
	// back to the uniform derivation, generating a wrong Go field/method with a clean
	// build. fatalf naming the bad key closes that gap.
	for key := range overlayOverrides {
		if !seen[key] {
			fatalf("overlayOverrides key %q is not a real overlay flag in OVERLAY_FLAG_NAMES", key)
		}
	}
	return flags, nil
}

// writeOverlayGen emits nodes/Wiring/overlay_gen.go: the entire Go-side overlay wiring
// mechanically derived from OVERLAY_FLAG_NAMES — the overlayState struct + flip/emit
// methods, the defaultOverlayState constructor, the MoveDispatch delegators, the
// overlayToggles method-expression table, the stdinGuideVisPayload wire struct, and the
// overlayStateFromPayload mapper. Deviating flags (scene/node poles Breadcrumb, the
// angleLabels accessor) are generated from overlayOverrides. Adding an overlay flag now
// means editing OVERLAY_FLAG_NAMES (+ the ~4-5 TS/render sites); every Go site above is
// regenerated. Parity of the generated table/struct is guarded by check-edit-op-parity.sh
// (which reads this file's OVERLAY_TOGGLES / GUIDEVIS_FIELDS sentinels) and staleness by
// check-generated.sh.
func writeOverlayGen(outPath string, flags []overlayFlag) error {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: OVERLAY_FLAG_NAMES in tools/topology-vscode/src/messages.ts.`)
	fmt.Fprintln(w, `// Regenerate with: cd tools/topology-vscode && npm run gen:node-defs`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `package Wiring`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `import (`)
	fmt.Fprintln(w, "\t\"fmt\"")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\tT \"github.com/dtauraso/wirefold/Trace\"")
	fmt.Fprintln(w, `)`)
	fmt.Fprintln(w)

	// overlayState struct.
	fmt.Fprintln(w, `// overlayState groups the per-toggle overlay-visibility booleans and their`)
	fmt.Fprintln(w, `// flip/emit logic. Owned by MoveDispatch (md.ov); the delegators below keep the`)
	fmt.Fprintln(w, `// stdin reader's overlayToggles method-expression table binding MoveDispatch.`)
	fmt.Fprintln(w, `type overlayState struct {`)
	for _, f := range flags {
		fmt.Fprintf(w, "\t%s bool\n", f.field)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)

	// setFlag helper.
	fmt.Fprintln(w, `// setFlag flips *field and emits the new value via emit. Shared body of the uniform`)
	fmt.Fprintln(w, `// (flip-then-emit) Toggle* methods.`)
	fmt.Fprintln(w, `func (o *overlayState) setFlag(field *bool, emit func(bool)) {`)
	fmt.Fprintln(w, "\t*field = !*field")
	fmt.Fprintln(w, "\temit(*field)")
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)

	// Per-flag Toggle/Emit (+ accessor) on overlayState.
	for _, f := range flags {
		fmt.Fprintf(w, "// Toggle%s flips %s and emits a %s event.\n", f.method, f.field, kebabOf(f.method))
		fmt.Fprintf(w, "func (o *overlayState) Toggle%s(tr *T.Trace) {\n", f.method)
		if f.breadcrumb != "" {
			fmt.Fprintf(w, "\to.%s = !o.%s\n", f.field, f.field)
			fmt.Fprintf(w, "\ttr.Breadcrumb(\"pole-toggle-go\", %q, \"\", fmt.Sprintf(\"visible=%%v\", o.%s))\n", f.breadcrumb, f.field)
			fmt.Fprintf(w, "\ttr.%s(o.%s)\n", f.method, f.field)
		} else {
			fmt.Fprintf(w, "\to.setFlag(&o.%s, tr.%s)\n", f.field, f.method)
		}
		fmt.Fprintln(w, `}`)
		fmt.Fprintln(w)
		fmt.Fprintf(w, "// Emit%s emits the current %s without toggling it.\n", f.method, f.field)
		fmt.Fprintf(w, "func (o *overlayState) Emit%s(tr *T.Trace) {\n", f.method)
		fmt.Fprintf(w, "\ttr.%s(o.%s)\n", f.method, f.field)
		fmt.Fprintln(w, `}`)
		fmt.Fprintln(w)
		if f.accessor {
			fmt.Fprintf(w, "// %s returns the current %s.\n", f.method, f.field)
			fmt.Fprintf(w, "func (o *overlayState) %s() bool {\n", f.method)
			fmt.Fprintf(w, "\treturn o.%s\n", f.field)
			fmt.Fprintln(w, `}`)
			fmt.Fprintln(w)
		}
	}

	// SetGuideVisibility on overlayState.
	fmt.Fprintln(w, `// SetGuideVisibility installs an explicit-visibility snapshot wholesale (the TS`)
	fmt.Fprintln(w, `// startup push so settings survive a Go respawn) and emits each so the renderer`)
	fmt.Fprintln(w, `// reflects them.`)
	fmt.Fprintln(w, `func (o *overlayState) SetGuideVisibility(ov overlayState, tr *T.Trace) {`)
	fmt.Fprintln(w, "\t*o = ov")
	for _, f := range flags {
		fmt.Fprintf(w, "\to.Emit%s(tr)\n", f.method)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)

	// defaultOverlayState constructor.
	fmt.Fprintln(w, `// defaultOverlayState is the startup overlay snapshot used by newMoveDispatch.`)
	fmt.Fprintln(w, `func defaultOverlayState() overlayState {`)
	fmt.Fprintln(w, "\treturn overlayState{")
	for _, f := range flags {
		if f.defaultOn {
			fmt.Fprintf(w, "\t\t%s: true,\n", f.field)
		}
	}
	fmt.Fprintln(w, "\t}")
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)

	// MoveDispatch delegators.
	fmt.Fprintln(w, `// Overlay-visibility API — thin delegators to the owned overlayState. The public`)
	fmt.Fprintln(w, `// signatures are unchanged (overlayToggles binds these method expressions).`)
	for _, f := range flags {
		fmt.Fprintf(w, "func (md *MoveDispatch) Toggle%s(tr *T.Trace) { md.ov.Toggle%s(tr) }\n", f.method, f.method)
		fmt.Fprintf(w, "func (md *MoveDispatch) Emit%s(tr *T.Trace) { md.ov.Emit%s(tr) }\n", f.method, f.method)
		if f.accessor {
			fmt.Fprintf(w, "func (md *MoveDispatch) %s() bool { return md.ov.%s() }\n", f.method, f.method)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// SetGuideVisibility delegates the wholesale explicit-visibility push.`)
	fmt.Fprintln(w, `func (md *MoveDispatch) SetGuideVisibility(ov overlayState, tr *T.Trace) {`)
	fmt.Fprintln(w, "\tmd.ov.SetGuideVisibility(ov, tr)")
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)

	// overlayToggles map (sentinel-bounded for check-edit-op-parity.sh axis 3).
	fmt.Fprintln(w, `// overlayToggles maps an overlay FLAG name (the attr="toggle" wire name) to the`)
	fmt.Fprintln(w, `// MoveDispatch method that flips it.`)
	fmt.Fprintln(w, `//`)
	fmt.Fprintln(w, `// OVERLAY_TOGGLES_START`)
	fmt.Fprintln(w, `var overlayToggles = map[string]func(*MoveDispatch, *T.Trace){`)
	for _, f := range flags {
		fmt.Fprintf(w, "\t%q: (*MoveDispatch).Toggle%s,\n", f.flag, f.method)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// OVERLAY_TOGGLES_END`)
	fmt.Fprintln(w)

	// stdinGuideVisPayload struct (sentinel-bounded for axis 4) + mapper.
	fmt.Fprintln(w, `// stdinGuideVisPayload holds the explicit-visibility fields for the overlays`)
	fmt.Fprintln(w, `// attr="set" op. The json tags are the overlay FLAG vocabulary shared with the TS`)
	fmt.Fprintln(w, `// OverlayState.`)
	fmt.Fprintln(w, `//`)
	fmt.Fprintln(w, `// GUIDEVIS_FIELDS_START`)
	fmt.Fprintln(w, `type stdinGuideVisPayload struct {`)
	for _, f := range flags {
		fmt.Fprintf(w, "\t%s bool `json:%q`\n", exportedName(f.flag), f.flag)
	}
	fmt.Fprintln(w, `}`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// GUIDEVIS_FIELDS_END`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// overlayStateFromPayload maps the wire payload onto the named overlayState (no`)
	fmt.Fprintln(w, `// positional bool order to get wrong).`)
	fmt.Fprintln(w, `func overlayStateFromPayload(s *stdinGuideVisPayload) overlayState {`)
	fmt.Fprintln(w, "\treturn overlayState{")
	for _, f := range flags {
		fmt.Fprintf(w, "\t\t%s: s.%s,\n", f.field, exportedName(f.flag))
	}
	fmt.Fprintln(w, "\t}")
	fmt.Fprintln(w, `}`)

	w.Flush()
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format overlay_gen.go: %w", err)
	}
	return os.WriteFile(outPath, formatted, 0644)
}

// exportedName upper-cases the first rune of a camelCase flag for a Go struct field.
func exportedName(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}

// kebabOf converts a PascalCase method name to its kebab trace-kind string (doc only).
func kebabOf(s string) string {
	var out []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			out = append(out, '-')
		}
		if r >= 'A' && r <= 'Z' {
			out = append(out, r+32)
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// ─── Buffer layout codegen ───────────────────────────────────────────────────

// bufCol is one column within a buffer block (from a buf: struct tag).
type bufCol struct {
	name    string // Go field name, e.g. "X"
	bufType string // "f32" | "i32" | "u32" | "u8"
	offset  int    // byte offset within one row (packed, no padding)
}

// bufBlock is one column block from a bufLayout* struct definition.
type bufBlock struct {
	name    string   // e.g. "Bead", "Node", "Edge", "Camera", "Overlay"
	columns []bufCol // in declaration order
	stride  int      // total bytes per row
}

// bufEventEntry is one entry in the semantic event enum (BufEvent* constants).
type bufEventEntry struct {
	name  string // e.g. "Recv"
	value int
}

// bufLayoutSchema is the parsed content of Buffer/layout.go.
type bufLayoutSchema struct {
	version int
	blocks  []bufBlock
	events  []bufEventEntry
}

// bufTypeSize returns the byte width of a buf: type tag value.
func bufTypeSize(t string) (int, error) {
	switch t {
	case "f32", "i32", "u32":
		return 4, nil
	case "u8":
		return 1, nil
	}
	return 0, fmt.Errorf("unknown buf type %q (expected f32|i32|u32|u8)", t)
}

// parseBufferLayout reads Buffer/layout.go and returns the schema:
// - BufLayoutVersion const → version
// - BufEvent* consts in source order → events
// - bufLayout* struct types in source order → blocks with columns + strides
func parseBufferLayout(layoutPath string) (bufLayoutSchema, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, layoutPath, nil, 0)
	if err != nil {
		return bufLayoutSchema{}, err
	}

	var schema bufLayoutSchema

	// Walk declarations in source order to preserve relative ordering of consts
	// and struct types (they are interleaved intentionally — version first, then
	// events, then blocks).
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok == token.CONST {
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for i, nm := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						lit, ok := vs.Values[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.INT {
							continue
						}
						var ival int
						fmt.Sscan(lit.Value, &ival)
						switch {
						case nm.Name == "BufLayoutVersion":
							schema.version = ival
						case strings.HasPrefix(nm.Name, "BufEvent"):
							suffix := nm.Name[len("BufEvent"):]
							schema.events = append(schema.events, bufEventEntry{name: suffix, value: ival})
						}
					}
				}
			} else if d.Tok == token.TYPE {
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if !strings.HasPrefix(ts.Name.Name, "bufLayout") {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					blockName := ts.Name.Name[len("bufLayout"):]
					block := bufBlock{name: blockName}
					offset := 0
					for _, field := range st.Fields.List {
						if field.Tag == nil || len(field.Names) == 0 {
							continue
						}
						rawTag := strings.Trim(field.Tag.Value, "`")
						_, after, ok := strings.Cut(rawTag, `buf:"`)
						if !ok {
							continue
						}
						bufType, _, _ := strings.Cut(after, `"`)
						sz, err := bufTypeSize(bufType)
						if err != nil {
							return bufLayoutSchema{}, fmt.Errorf("block %s field %s: %w", blockName, field.Names[0].Name, err)
						}
						block.columns = append(block.columns, bufCol{
							name:    field.Names[0].Name,
							bufType: bufType,
							offset:  offset,
						})
						offset += sz
					}
					block.stride = offset
					schema.blocks = append(schema.blocks, block)
				}
			}
		}
	}

	if schema.version == 0 {
		return bufLayoutSchema{}, fmt.Errorf("BufLayoutVersion const not found in %s", layoutPath)
	}
	if len(schema.events) == 0 {
		return bufLayoutSchema{}, fmt.Errorf("no BufEvent* consts found in %s", layoutPath)
	}
	if len(schema.blocks) == 0 {
		return bufLayoutSchema{}, fmt.Errorf("no bufLayout* struct types found in %s", layoutPath)
	}
	return schema, nil
}

// buildBufFingerprint builds a deterministic fingerprint string from the schema.
// Both generated files embed this as a comment; the parity guard greps and compares.
func buildBufFingerprint(schema bufLayoutSchema) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("version:%d", schema.version))
	for _, blk := range schema.blocks {
		var cols []string
		for _, c := range blk.columns {
			cols = append(cols, fmt.Sprintf("%s:%s:%d", c.name, c.bufType, c.offset))
		}
		parts = append(parts, fmt.Sprintf("block:%s[%s]:stride:%d", blk.name, strings.Join(cols, ","), blk.stride))
	}
	var evs []string
	for _, ev := range schema.events {
		evs = append(evs, fmt.Sprintf("%s:%d", ev.name, ev.value))
	}
	parts = append(parts, "event["+strings.Join(evs, ",")+"]")
	return strings.Join(parts, "|")
}

// goWriterCall returns the Go snippet to write one column into buf[off+c.offset:].
func goWriterCall(c bufCol) string {
	off := fmt.Sprintf("off+%d", c.offset)
	param := strings.ToLower(c.name[:1]) + c.name[1:]
	switch c.bufType {
	case "f32":
		return fmt.Sprintf("\tbinary.LittleEndian.PutUint32(buf[%s:], math.Float32bits(%s))", off, param)
	case "i32":
		return fmt.Sprintf("\tbinary.LittleEndian.PutUint32(buf[%s:], uint32(%s))", off, param)
	case "u32":
		return fmt.Sprintf("\tbinary.LittleEndian.PutUint32(buf[%s:], %s)", off, param)
	case "u8":
		return fmt.Sprintf("\tbuf[%s] = %s", off, param)
	}
	return ""
}

// goParamType returns the Go parameter type for a buf: type tag.
func goParamType(t string) string {
	switch t {
	case "f32":
		return "float32"
	case "i32":
		return "int32"
	case "u32":
		return "uint32"
	case "u8":
		return "uint8"
	}
	return "byte"
}

// tsDataViewGetter returns the DataView getter method for a buf: type.
func tsDataViewGetter(t string) string {
	switch t {
	case "f32":
		return "getFloat32"
	case "i32":
		return "getInt32"
	case "u32":
		return "getUint32"
	case "u8":
		return "getUint8"
	}
	return "getUint8"
}

// tsDataViewLE returns ", true" for multi-byte types (little-endian flag) or ""
// for single-byte types.
func tsDataViewLE(t string) string {
	if t == "u8" {
		return ""
	}
	return ", true"
}

// colGoName converts a block name + column name to the Go const name.
// e.g. Bead + X → BufBeadColX; Bead + stride → BufBeadStride.
func colGoName(block, col string) string {
	return "Buf" + block + "Col" + col
}

// strideGoName returns the Go stride constant name for a block.
func strideGoName(block string) string {
	return "Buf" + block + "Stride"
}

// colTSName converts a block name + column name to the TS const name (SCREAMING_SNAKE).
// e.g. Bead + X → BEAD_COL_X; Node + CX → NODE_COL_CX.
func colTSName(block, col string) string {
	return camelToScreamingSnake(block) + "_COL_" + camelToScreamingSnake(col)
}

// strideTSName returns the TS stride constant name for a block.
func strideTSName(block string) string {
	return camelToScreamingSnake(block) + "_STRIDE"
}

// writerFnGoName returns the Go writer function name for a block.
func writerFnGoName(block string) string {
	return "Set" + block + "Row"
}

// readerFnTSName returns the TS reader function name for one column.
func readerFnTSName(block, col string) string {
	return "read" + block + col
}

// writeBufferLayoutGo emits Buffer/buffer_layout_gen.go.
func writeBufferLayoutGo(outPath string, schema bufLayoutSchema) error {
	fp := buildBufFingerprint(schema)
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: Buffer/layout.go  Regenerate: cd tools/topology-vscode && npm run gen:node-defs`)
	fmt.Fprintf(w, "// BUF_LAYOUT_FINGERPRINT: %s\n", fp)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `package Buffer`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `import (`)
	fmt.Fprintln(w, `	"encoding/binary"`)
	fmt.Fprintln(w, `	"math"`)
	fmt.Fprintln(w, `)`)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "// BufLayoutVersionGenerated must equal BufLayoutVersion in layout.go.\n")
	fmt.Fprintf(w, "const BufLayoutVersionGenerated = %d\n", schema.version)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// BufHeaderSize is the byte width of the snapshot header:`)
	fmt.Fprintln(w, `// [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32][portCount:u32]`)
	fmt.Fprintln(w, `const BufHeaderSize = 20`)

	for _, blk := range schema.blocks {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "// ── %s block ", blk.name)
		fmt.Fprintln(w, strings.Repeat("─", 60-len(blk.name)-9))
		fmt.Fprintln(w, `const (`)
		for _, c := range blk.columns {
			fmt.Fprintf(w, "\t%-35s = %d // %s\n", colGoName(blk.name, c.name), c.offset, c.bufType)
		}
		fmt.Fprintf(w, "\t%-35s = %d\n", strideGoName(blk.name), blk.stride)
		fmt.Fprintln(w, `)`)
		fmt.Fprintln(w)

		// Writer function.
		var params []string
		if blk.name == "Camera" || blk.name == "Overlay" {
			// Camera and Overlay always have exactly 1 row; omit row param.
			for _, c := range blk.columns {
				pname := strings.ToLower(c.name[:1]) + c.name[1:]
				params = append(params, fmt.Sprintf("%s %s", pname, goParamType(c.bufType)))
			}
			fmt.Fprintf(w, "// %s writes the %s row into buf (always 1 row; no row param).\n", writerFnGoName(blk.name), blk.name)
			fmt.Fprintf(w, "func %s(buf []byte, %s) {\n", writerFnGoName(blk.name), strings.Join(params, ", "))
			for _, c := range blk.columns {
				pname := strings.ToLower(c.name[:1]) + c.name[1:]
				off := fmt.Sprintf("%d", c.offset)
				switch c.bufType {
				case "f32":
					fmt.Fprintf(w, "\tbinary.LittleEndian.PutUint32(buf[%s:], math.Float32bits(%s))\n", off, pname)
				case "i32":
					fmt.Fprintf(w, "\tbinary.LittleEndian.PutUint32(buf[%s:], uint32(%s))\n", off, pname)
				case "u32":
					fmt.Fprintf(w, "\tbinary.LittleEndian.PutUint32(buf[%s:], %s)\n", off, pname)
				case "u8":
					fmt.Fprintf(w, "\tbuf[%s] = %s\n", off, pname)
				}
			}
			fmt.Fprintln(w, `}`)
		} else {
			for _, c := range blk.columns {
				pname := strings.ToLower(c.name[:1]) + c.name[1:]
				params = append(params, fmt.Sprintf("%s %s", pname, goParamType(c.bufType)))
			}
			fmt.Fprintf(w, "// %s writes one %s row into buf[row*%s:].\n", writerFnGoName(blk.name), blk.name, strideGoName(blk.name))
			fmt.Fprintf(w, "func %s(buf []byte, row int, %s) {\n", writerFnGoName(blk.name), strings.Join(params, ", "))
			fmt.Fprintf(w, "\toff := row * %s\n", strideGoName(blk.name))
			for _, c := range blk.columns {
				fmt.Fprintln(w, goWriterCall(c))
			}
			fmt.Fprintln(w, `}`)
		}
	}

	// Event enum.
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// ── Semantic event enum ─────────────────────────────────────────────────────────`)
	fmt.Fprintln(w, `// Transient per-node flags stored in node rows (EvRecv/EvFire/…).`)
	fmt.Fprintln(w, `// These ids are embedded in snapshots; names are resolved from this schema.`)
	fmt.Fprintln(w, `const (`)
	for _, ev := range schema.events {
		fmt.Fprintf(w, "\tBufEvent%sID = %d\n", ev.name, ev.value)
	}
	fmt.Fprintln(w, `)`)

	w.Flush()
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("format buffer_layout_gen.go: %w\n--- unformatted ---\n%s", err, buf.String())
	}
	return os.WriteFile(outPath, formatted, 0644)
}

// writeBufferLayoutTS emits tools/topology-vscode/src/schema/buffer-layout.ts.
func writeBufferLayoutTS(outPath string, schema bufLayoutSchema) error {
	fp := buildBufFingerprint(schema)
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	fmt.Fprintln(w, `// GENERATED by tools/gen-node-defs — do not edit.`)
	fmt.Fprintln(w, `// Source: Buffer/layout.go  Regenerate: npm run gen:node-defs`)
	fmt.Fprintf(w, "// BUF_LAYOUT_FINGERPRINT: %s\n", fp)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "/** Schema version — must match BufLayoutVersion in Buffer/layout.go. */\n")
	fmt.Fprintf(w, "export const BUF_LAYOUT_VERSION = %d;\n", schema.version)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `/** Snapshot header: [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32][portCount:u32] */`)
	fmt.Fprintln(w, `export const BUF_HEADER_SIZE = 20;`)

	for _, blk := range schema.blocks {
		fmt.Fprintln(w)
		sep := strings.Repeat("─", 60-len(blk.name)-9)
		fmt.Fprintf(w, "// ── %s block %s\n", blk.name, sep)
		for _, c := range blk.columns {
			fmt.Fprintf(w, "export const %-35s = %d; // %s\n", colTSName(blk.name, c.name), c.offset, c.bufType)
		}
		fmt.Fprintf(w, "export const %-35s = %d;\n", strideTSName(blk.name), blk.stride)
		fmt.Fprintln(w)

		// Reader helpers.
		stride := strideTSName(blk.name)
		for _, c := range blk.columns {
			colConst := colTSName(blk.name, c.name)
			getter := tsDataViewGetter(c.bufType)
			le := tsDataViewLE(c.bufType)
			fnName := readerFnTSName(blk.name, c.name)
			if blk.name == "Camera" || blk.name == "Overlay" {
				// Single-row blocks: no row param.
				fmt.Fprintf(w, "export function %s(view: DataView): number { return view.%s(%s%s); }\n",
					fnName, getter, colConst, le)
			} else {
				fmt.Fprintf(w, "export function %s(view: DataView, row: number): number { return view.%s(row * %s + %s%s); }\n",
					fnName, getter, stride, colConst, le)
			}
		}
	}

	// Event enum.
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// ── Semantic event enum ─────────────────────────────────────────────────────────`)
	fmt.Fprintln(w, `// Transient per-node event flags; ids embedded in snapshots, names in this schema.`)
	for _, ev := range schema.events {
		tsName := "BUF_EVENT_" + strings.ToUpper(ev.name)
		fmt.Fprintf(w, "export const %-25s = %d;\n", tsName, ev.value)
	}

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}

// joinPortsTyped emits {name, kind, isMulti?} pairs for NodeTypeDef-compatible consumers.
func joinPortsTyped(ports []port) string {
	var parts []string
	for _, p := range ports {
		ek := p.edgeKind
		if ek == "" {
			ek = "chain" // default
		}
		if p.isMulti {
			parts = append(parts, fmt.Sprintf(`{ name: "%s", kind: "%s", isMulti: true }`, p.id, ek))
		} else {
			parts = append(parts, fmt.Sprintf(`{ name: "%s", kind: "%s" }`, p.id, ek))
		}
	}
	return strings.Join(parts, ", ")
}
