// cmd/pseudo wraps tools/pseudo for CLI use.
//
// Subcommands:
//
//	pseudo input render --go-file <path> --spec-json <json>
//	  Reads go-file + parses spec-json, calls pseudo.FromInput + pseudo.RenderInput,
//	  prints the pseudo string to stdout and exits 0.
//
//	pseudo input save --go-file <path> --spec-json <json> --pseudo <text>
//	  Calls FromInput → ParseInput(pseudo, prior) → ToInput.
//	  Prints {"go": "<new source>", "spec": {...}} JSON to stdout and exits 0.
//
//	pseudo readgate render --go-file <path> --out-neighbor <id>
//	  Reads go-file, calls pseudo.FromReadGate + pseudo.RenderReadGate,
//	  prints the pseudo text to stdout and exits 0.
//
//	pseudo readgate save --go-file <path> --out-neighbor <id> --pseudo <text>
//	  Calls FromReadGate → ParseReadGate(pseudo, prior) → ToReadGate.
//	  Prints {"go": "<new source>", "outNeighbor": "<id>", "removedPorts": [...]} JSON to stdout and exits 0.
//
//	pseudo chaininhibitor render --go-file <path> --out-neighbors <id[,id...]>
//	  Reads go-file, calls pseudo.FromChainInhibitor + pseudo.RenderChainInhibitor,
//	  prints the pseudo text to stdout and exits 0.
//
//	pseudo chaininhibitor save --go-file <path> --out-neighbors <id[,id...]> --pseudo <text>
//	  Calls FromChainInhibitor → ParseChainInhibitor(pseudo, prior) → ToChainInhibitor.
//	  Prints {"go": "<new source>", "outNeighbors": ["<id>",...], "removedPorts": [...]} JSON to stdout and exits 0.
//
// On error: prints {"error":"..."} JSON to stderr and exits 2.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dtauraso/wirefold/tools/pseudo"
)

type kindHandlers struct {
	render func([]string)
	save   func([]string)
}

var pseudoDispatch = map[string]kindHandlers{
	"input":           {render: runRender, save: runSave},
	"readgate":        {render: runReadGateRender, save: runReadGateSave},
	"chaininhibitor":  {render: runChainInhibitorRender, save: runChainInhibitorSave},
}

func main() {
	if len(os.Args) < 3 {
		fatal("usage: pseudo <input|readgate|chaininhibitor> <render|save> ...")
	}
	kind, op := os.Args[1], os.Args[2]
	handlers, ok := pseudoDispatch[kind]
	if !ok {
		fatal("unknown subcommand group %q; expected \"input\", \"readgate\", or \"chaininhibitor\"", kind)
	}
	switch op {
	case "render":
		handlers.render(os.Args[3:])
	case "save":
		handlers.save(os.Args[3:])
	default:
		fatal("unknown subcommand %q; expected \"render\" or \"save\"", op)
	}
}

func runReadGateRender(args []string) {
	flags, err := parseReadGateFlags(args, "go-file", "out-neighbor")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	view, err := pseudo.FromReadGate(goSrc, flags["out-neighbor"])
	if err != nil {
		fatal("%s", err)
	}

	fmt.Print(pseudo.RenderReadGate(view))
}

func runReadGateSave(args []string) {
	flags, err := parseReadGateFlags(args, "go-file", "out-neighbor", "pseudo")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	prior, err := pseudo.FromReadGate(goSrc, flags["out-neighbor"])
	if err != nil {
		fatal("FromReadGate: %s", err)
	}

	updated, err := pseudo.ParseReadGate(flags["pseudo"], prior)
	if err != nil {
		suggestion := ""
		var pe *pseudo.ParseReadGateError
		if errors.As(err, &pe) {
			suggestion = pe.Suggestion()
		}
		fatalWithSuggestion("ParseReadGate: %s", err, suggestion)
	}

	newGoSrc, newOutNeighbor, removedPorts, err := pseudo.ToReadGate(updated)
	if err != nil {
		fatal("ToReadGate: %s", err)
	}

	out := map[string]any{
		"go":           string(newGoSrc),
		"outNeighbor":  newOutNeighbor,
		"removedPorts": removedPorts,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fatal("encoding output: %s", err)
	}
}

// ── ChainInhibitor subcommands ────────────────────────────────────────────────

func runChainInhibitorRender(args []string) {
	flags, err := parseChainInhibitorFlags(args, "go-file", "out-neighbors")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	outNeighbors := splitNeighbors(flags["out-neighbors"])
	view, err := pseudo.FromChainInhibitor(goSrc, outNeighbors)
	if err != nil {
		fatal("%s", err)
	}

	fmt.Print(pseudo.RenderChainInhibitor(view))
}

func runChainInhibitorSave(args []string) {
	flags, err := parseChainInhibitorFlags(args, "go-file", "out-neighbors", "pseudo")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	outNeighbors := splitNeighbors(flags["out-neighbors"])
	prior, err := pseudo.FromChainInhibitor(goSrc, outNeighbors)
	if err != nil {
		fatal("FromChainInhibitor: %s", err)
	}

	updated, err := pseudo.ParseChainInhibitor(flags["pseudo"], prior)
	if err != nil {
		suggestion := ""
		var pe *pseudo.ParseChainInhibitorError
		if errors.As(err, &pe) {
			suggestion = pe.Suggestion()
		}
		fatalWithSuggestion("ParseChainInhibitor: %s", err, suggestion)
	}

	newGoSrc, newOutNeighbors, removedPorts, err := pseudo.ToChainInhibitor(updated)
	if err != nil {
		fatal("ToChainInhibitor: %s", err)
	}

	out := map[string]any{
		"go":            string(newGoSrc),
		"outNeighbors":  newOutNeighbors,
		"removedPorts":  removedPorts,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fatal("encoding output: %s", err)
	}
}

// splitNeighbors splits a comma-separated neighbor list, trimming whitespace.
func splitNeighbors(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseFlags extracts named flag values from args.
// Returns an error if a required flag is missing.
func parseFlags(args []string, required ...string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--go-file", "--spec-json", "--pseudo", "--out-neighbor":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a value", args[i])
			}
			flags[args[i][2:]] = args[i+1]
			i++
		default:
			return nil, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	for _, r := range required {
		if _, ok := flags[r]; !ok {
			return nil, fmt.Errorf("missing required flag --%s", r)
		}
	}
	return flags, nil
}

// parseReadGateFlags extracts named flag values for readgate subcommands.
// Returns an error if a required flag is missing.
func parseReadGateFlags(args []string, required ...string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--go-file", "--out-neighbor", "--pseudo":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a value", args[i])
			}
			flags[args[i][2:]] = args[i+1]
			i++
		default:
			return nil, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	for _, r := range required {
		if _, ok := flags[r]; !ok {
			return nil, fmt.Errorf("missing required flag --%s", r)
		}
	}
	return flags, nil
}

// parseChainInhibitorFlags extracts named flag values for chaininhibitor subcommands.
// Uses --out-neighbors (plural, comma-separated) instead of --out-neighbor.
// Returns an error if a required flag is missing.
func parseChainInhibitorFlags(args []string, required ...string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--go-file", "--out-neighbors", "--pseudo":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s requires a value", args[i])
			}
			flags[args[i][2:]] = args[i+1]
			i++
		default:
			return nil, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	for _, r := range required {
		if _, ok := flags[r]; !ok {
			return nil, fmt.Errorf("missing required flag --%s", r)
		}
	}
	return flags, nil
}

func runRender(args []string) {
	flags, err := parseFlags(args, "go-file", "spec-json", "out-neighbor")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	var specEntry map[string]any
	if err := json.Unmarshal([]byte(flags["spec-json"]), &specEntry); err != nil {
		fatal("parsing spec-json: %s", err)
	}

	view, err := pseudo.FromInput(goSrc, specEntry, flags["out-neighbor"])
	if err != nil {
		fatal("%s", err)
	}

	fmt.Println(pseudo.RenderInput(view))
}

func runSave(args []string) {
	flags, err := parseFlags(args, "go-file", "spec-json", "pseudo", "out-neighbor")
	if err != nil {
		fatal("%s", err)
	}

	goSrc, err := os.ReadFile(flags["go-file"])
	if err != nil {
		fatal("reading go-file: %s", err)
	}

	var specEntry map[string]any
	if err := json.Unmarshal([]byte(flags["spec-json"]), &specEntry); err != nil {
		fatal("parsing spec-json: %s", err)
	}

	prior, err := pseudo.FromInput(goSrc, specEntry, flags["out-neighbor"])
	if err != nil {
		fatal("FromInput: %s", err)
	}

	updated, err := pseudo.ParseInput(flags["pseudo"], prior)
	if err != nil {
		suggestion := ""
		var pe *pseudo.ParseInputError
		if errors.As(err, &pe) {
			suggestion = pe.Suggestion()
		}
		fatalWithSuggestion("ParseInput: %s", err, suggestion)
	}

	newGoSrc, newSpec, err := pseudo.ToInput(updated)
	if err != nil {
		fatal("ToInput: %s", err)
	}

	out := map[string]any{
		"go":   string(newGoSrc),
		"spec": newSpec,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fatal("encoding output: %s", err)
	}
}

func fatal(format string, args ...any) {
	exitWithError(fmt.Sprintf(format, args...), "")
}

func fatalWithSuggestion(format string, arg any, suggestion string) {
	exitWithError(fmt.Sprintf(format, arg), suggestion)
}

func exitWithError(msg, suggestion string) {
	enc := json.NewEncoder(os.Stderr)
	payload := map[string]string{"error": msg}
	if suggestion != "" {
		payload["suggestion"] = suggestion
	}
	_ = enc.Encode(payload)
	os.Exit(2)
}
