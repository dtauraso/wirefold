// Buffer-layout codegen: parses Buffer/layout.go's buf: struct tags and emits
// both Buffer/buffer_layout_gen.go (Go writers) and src/schema/buffer-layout.ts
// (TS readers) from one shared schema + one bufTypeTable, so the two sides
// cannot disagree about a column's width, signedness, or endianness (see
// TestBufTypeTableConsistency).
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
	"strings"
)

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

// bufLayoutSchema is the parsed content of Buffer/layout.go.
type bufLayoutSchema struct {
	version              int
	blocks               []bufBlock
	interiorSlotsPerNode int
}

// bufTypeInfo is the SINGLE source of truth for one buf: type tag's byte width,
// Go representation, and TS DataView accessor. Every emitter (Go writer, Go param
// type, TS getter, TS endianness flag) derives from this one table entry, so the
// Go side and TS side of a given bufType CANNOT disagree by construction — there
// used to be four parallel switches over the same bufType set (goWriterCall,
// goParamType, tsDataViewGetter, tsDataViewLE), any one of which could be edited
// without the others, producing generated files that carry the SAME
// BUF_LAYOUT_FINGERPRINT while reading DIFFERENT bytes. See
// TestBufTypeTableConsistency in main_test.go for the guard.
type bufTypeInfo struct {
	size     int                          // byte width
	goType   string                       // Go parameter type
	goWrite  func(off, val string) string // Go statement writing val into buf[off:]
	tsGetter string                       // DataView getter method name
	tsLE     bool                         // true if the getter takes a little-endian flag arg
}

// bufTypeTable is keyed by the buf: type tag string ("f32" | "i32" | "u32" | "u8").
// Add a new bufType here — and ONLY here — to teach both emitters about it.
var bufTypeTable = map[string]bufTypeInfo{
	"f32": {
		size:   4,
		goType: "float32",
		goWrite: func(off, val string) string {
			return fmt.Sprintf("binary.LittleEndian.PutUint32(buf[%s:], math.Float32bits(%s))", off, val)
		},
		tsGetter: "getFloat32",
		tsLE:     true,
	},
	"i32": {
		size:   4,
		goType: "int32",
		goWrite: func(off, val string) string {
			return fmt.Sprintf("binary.LittleEndian.PutUint32(buf[%s:], uint32(%s))", off, val)
		},
		tsGetter: "getInt32",
		tsLE:     true,
	},
	"u32": {
		size:   4,
		goType: "uint32",
		goWrite: func(off, val string) string {
			return fmt.Sprintf("binary.LittleEndian.PutUint32(buf[%s:], %s)", off, val)
		},
		tsGetter: "getUint32",
		tsLE:     true,
	},
	"u8": {
		size:   1,
		goType: "uint8",
		goWrite: func(off, val string) string {
			return fmt.Sprintf("buf[%s] = %s", off, val)
		},
		tsGetter: "getUint8",
		tsLE:     false,
	},
}

// lookupBufType returns the table entry for t, or fatalf's — a bufType missing from
// the table must be a loud build failure, not a silent default (the old switches
// each had their own silent fallback: "" from goWriterCall, "byte" from
// goParamType, "getUint8"/"" from the TS pair — all different, all wrong).
func lookupBufType(t string) bufTypeInfo {
	info, ok := bufTypeTable[t]
	if !ok {
		fatalf("unknown buf type %q (expected one of: f32, i32, u32, u8 — add it to bufTypeTable)", t)
	}
	return info
}

// bufTypeSize returns the byte width of a buf: type tag value.
func bufTypeSize(t string) (int, error) {
	info, ok := bufTypeTable[t]
	if !ok {
		return 0, fmt.Errorf("unknown buf type %q (expected f32|i32|u32|u8)", t)
	}
	return info.size, nil
}

// parseBufferLayout reads Buffer/layout.go and returns the schema:
// - BufLayoutVersion const → version
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
	// blocks).
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
						case nm.Name == "BufInteriorSlotsPerNode":
							schema.interiorSlotsPerNode = ival
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
	if schema.interiorSlotsPerNode == 0 {
		return bufLayoutSchema{}, fmt.Errorf("BufInteriorSlotsPerNode const not found in %s", layoutPath)
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
	parts = append(parts, fmt.Sprintf("interiorSlotsPerNode:%d", schema.interiorSlotsPerNode))
	for _, blk := range schema.blocks {
		var cols []string
		for _, c := range blk.columns {
			cols = append(cols, fmt.Sprintf("%s:%s:%d", c.name, c.bufType, c.offset))
		}
		parts = append(parts, fmt.Sprintf("block:%s[%s]:stride:%d", blk.name, strings.Join(cols, ","), blk.stride))
	}
	return strings.Join(parts, "|")
}

// goWriterCall returns the Go snippet to write one column into buf[off+c.offset:].
func goWriterCall(c bufCol) string {
	off := fmt.Sprintf("off+%d", c.offset)
	param := strings.ToLower(c.name[:1]) + c.name[1:]
	return "\t" + lookupBufType(c.bufType).goWrite(off, param)
}

// goParamType returns the Go parameter type for a buf: type tag.
func goParamType(t string) string {
	return lookupBufType(t).goType
}

// tsDataViewGetter returns the DataView getter method for a buf: type.
func tsDataViewGetter(t string) string {
	return lookupBufType(t).tsGetter
}

// tsDataViewLE returns ", true" for multi-byte types (little-endian flag) or ""
// for single-byte types.
func tsDataViewLE(t string) string {
	if lookupBufType(t).tsLE {
		return ", true"
	}
	return ""
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
	fmt.Fprintln(w)
	fmt.Fprintln(w, `	T "github.com/dtauraso/wirefold/Trace"`)
	fmt.Fprintln(w, `)`)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "// BufLayoutVersionGenerated must equal BufLayoutVersion in layout.go.\n")
	fmt.Fprintf(w, "const BufLayoutVersionGenerated = %d\n", schema.version)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "// BufInteriorSlotsPerNodeGenerated must equal BufInteriorSlotsPerNode in layout.go.\n")
	fmt.Fprintf(w, "const BufInteriorSlotsPerNodeGenerated = %d\n", schema.interiorSlotsPerNode)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `// BufHeaderSize is the byte width of the SCENE frame's snapshot header. The Bead block`)
	fmt.Fprintln(w, `// (see BufBeadHeaderSize), the Node/Interior/Port blocks + Label/PortName bytes (see`)
	fmt.Fprintln(w, `// BufNodeFrameHeaderSize), and the Edge block + edge-label bytes (see`)
	fmt.Fprintln(w, `// BufEdgeFrameHeaderSize) are their own tagged frames — Buffer/frame_tags.go — so the`)
	fmt.Fprintln(w, `// SCENE header no longer carries their counts. The EVENT block was also RETIRED from`)
	fmt.Fprintln(w, `// this frame (memory/feedback_no_single_writer_bridge.md — each per-owner stream now`)
	fmt.Fprintln(w, `// carries its own trailing EVENTS section instead):`)
	fmt.Fprintln(w, `// [tick:u32][layoutLinkCount:u32]`)
	fmt.Fprintln(w, `const BufHeaderSize = 8`)

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
		if blk.name == "Overlay" {
			// The Overlay block is a single row with several same-typed (uint8) boolean
			// flag columns — a positional writer call here is exactly the "transposed
			// adjacent same-typed args compiles silently" hazard. Emit a named-field
			// struct (OverlayRow) instead: the writer takes ONE value, so there is no
			// per-field positional list at the call site to transpose at all.
			fmt.Fprintln(w, `// OverlayRow is the named-field snapshot of the Overlay block (single row).`)
			fmt.Fprintln(w, `// Passed BY VALUE to SetOverlayRow so the write call never enumerates fields`)
			fmt.Fprintln(w, `// positionally — closes the swapped-adjacent-uint8-args hazard a positional`)
			fmt.Fprintln(w, `// writer call would otherwise compile silently.`)
			fmt.Fprintln(w, `type OverlayRow struct {`)
			for _, c := range blk.columns {
				fmt.Fprintf(w, "\t%s %s\n", c.name, goParamType(c.bufType))
			}
			fmt.Fprintln(w, `}`)
			fmt.Fprintln(w)
			fmt.Fprintf(w, "// %s writes the %s row into buf (always 1 row; no row param).\n", writerFnGoName(blk.name), blk.name)
			fmt.Fprintf(w, "func %s(buf []byte, row OverlayRow) {\n", writerFnGoName(blk.name))
			for _, c := range blk.columns {
				fname := "row." + c.name
				off := fmt.Sprintf("%d", c.offset)
				fmt.Fprintf(w, "\t%s\n", lookupBufType(c.bufType).goWrite(off, fname))
			}
			fmt.Fprintln(w, `}`)
			fmt.Fprintln(w)

			// overlayFlagFieldsOf / IsOverlayFlagKind: mechanically generated from the
			// block's u8 columns (the boolean toggle flags — AbcDragCount is u32 and is
			// excluded by type, not by a hand-maintained exclusion list). Each u8 column
			// name X is matched to Trace's T.KindX by the same naming convention
			// overlay_gen.go already relies on (method name == Trace Kind suffix).
			var flagCols []bufCol
			for _, c := range blk.columns {
				if c.bufType == "u8" {
					flagCols = append(flagCols, c)
				}
			}
			fmt.Fprintln(w, `// overlayFlagFieldsOf returns the Trace-Kind -> field-pointer map used to apply an`)
			fmt.Fprintln(w, `// incoming overlay-flag event to row. Mechanically generated from the Overlay`)
			fmt.Fprintln(w, `// block's u8 columns in Buffer/layout.go — adding a flag column here requires no`)
			fmt.Fprintln(w, `// separate hand-edit anywhere else.`)
			fmt.Fprintln(w, `func overlayFlagFieldsOf(row *OverlayRow) map[string]*uint8 {`)
			fmt.Fprintln(w, `	return map[string]*uint8{`)
			for _, c := range flagCols {
				fmt.Fprintf(w, "\t\tT.Kind%s: &row.%s,\n", c.name, c.name)
			}
			fmt.Fprintln(w, `	}`)
			fmt.Fprintln(w, `}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, `// IsOverlayFlagKind reports whether kind is one of the Overlay block's u8`)
			fmt.Fprintln(w, `// boolean-flag Trace Kinds (the keys overlayFlagFieldsOf returns) — used instead`)
			fmt.Fprintln(w, `// of a hand-listed switch case in SnapshotState.Update.`)
			fmt.Fprintln(w, `func IsOverlayFlagKind(kind string) bool {`)
			fmt.Fprintln(w, `	switch kind {`)
			var kindNames []string
			for _, c := range flagCols {
				kindNames = append(kindNames, "T.Kind"+c.name)
			}
			fmt.Fprintf(w, "\tcase %s:\n", strings.Join(kindNames, ", "))
			fmt.Fprintln(w, `		return true`)
			fmt.Fprintln(w, `	}`)
			fmt.Fprintln(w, `	return false`)
			fmt.Fprintln(w, `}`)
		} else if blk.name == "Camera" || blk.name == "RuleBuilder" || blk.name == "Scene" {
			// Camera/RuleBuilder/Scene always have exactly 1 row; omit row param.
			for _, c := range blk.columns {
				pname := strings.ToLower(c.name[:1]) + c.name[1:]
				params = append(params, fmt.Sprintf("%s %s", pname, goParamType(c.bufType)))
			}
			fmt.Fprintf(w, "// %s writes the %s row into buf (always 1 row; no row param).\n", writerFnGoName(blk.name), blk.name)
			fmt.Fprintf(w, "func %s(buf []byte, %s) {\n", writerFnGoName(blk.name), strings.Join(params, ", "))
			for _, c := range blk.columns {
				pname := strings.ToLower(c.name[:1]) + c.name[1:]
				off := fmt.Sprintf("%d", c.offset)
				fmt.Fprintf(w, "\t%s\n", lookupBufType(c.bufType).goWrite(off, pname))
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
	fmt.Fprintf(w, "/** Fixed interior-grid slots per node — must match BufInteriorSlotsPerNode in Buffer/layout.go.\n")
	fmt.Fprintf(w, " * Generated (part of BUF_LAYOUT_FINGERPRINT): a mismatch fails check-buffer-layout-parity.sh. */\n")
	fmt.Fprintf(w, "export const INTERIOR_SLOTS_PER_NODE = %d;\n", schema.interiorSlotsPerNode)
	fmt.Fprintln(w)
	fmt.Fprintln(w, `/** SCENE frame's snapshot header. The Bead block (see BUF_BEAD_HEADER_SIZE), the`)
	fmt.Fprintln(w, ` * Node/Interior/Port blocks + Label/PortName bytes (see BUF_NODE_FRAME_HEADER_SIZE), and`)
	fmt.Fprintln(w, ` * the Edge block + edge-label bytes (see BUF_EDGE_FRAME_HEADER_SIZE) are their own tagged`)
	fmt.Fprintln(w, ` * frames — frame-tags.ts — so the SCENE header no longer carries their counts. The EVENT`)
	fmt.Fprintln(w, ` * block was also RETIRED from this frame (memory/feedback_no_single_writer_bridge.md —`)
	fmt.Fprintln(w, ` * each per-owner stream now carries its own trailing EVENTS section instead):`)
	fmt.Fprintln(w, ` * [tick:u32][layoutLinkCount:u32] */`)
	fmt.Fprintln(w, `export const BUF_HEADER_SIZE = 8;`)

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
			if blk.name == "Camera" || blk.name == "Overlay" || blk.name == "RuleBuilder" || blk.name == "Scene" {
				// Single-row blocks: no row param.
				fmt.Fprintf(w, "export function %s(view: DataView): number { return view.%s(%s%s); }\n",
					fnName, getter, colConst, le)
			} else {
				fmt.Fprintf(w, "export function %s(view: DataView, row: number): number { return view.%s(row * %s + %s%s); }\n",
					fnName, getter, stride, colConst, le)
			}
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, `/** Sentinel Node KindId value meaning "unknown kind" (matches KindIDUnknown in Buffer/node_kind_id_gen.go). */`)
	fmt.Fprintln(w, `export const UNKNOWN_KIND_ID = 0xff;`)

	w.Flush()
	return os.WriteFile(outPath, buf.Bytes(), 0644)
}
