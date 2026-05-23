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
// On error: prints {"error":"..."} JSON to stderr and exits 2.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/dtauraso/wirefold/tools/pseudo"
)

func main() {
	if len(os.Args) < 3 {
		fatal("usage: pseudo input render|save ...")
	}
	if os.Args[1] != "input" {
		fatal("unknown subcommand group %q; expected \"input\"", os.Args[1])
	}
	switch os.Args[2] {
	case "render":
		runRender(os.Args[3:])
	case "save":
		runSave(os.Args[3:])
	default:
		fatal("unknown subcommand %q; expected \"render\" or \"save\"", os.Args[2])
	}
}

// parseFlags extracts named flag values from args.
// Returns an error if a required flag is missing.
func parseFlags(args []string, required ...string) (map[string]string, error) {
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--go-file", "--spec-json", "--pseudo":
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
	flags, err := parseFlags(args, "go-file", "spec-json")
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

	view, err := pseudo.FromInput(goSrc, specEntry)
	if err != nil {
		fatal("%s", err)
	}

	fmt.Println(pseudo.RenderInput(view))
}

func runSave(args []string) {
	flags, err := parseFlags(args, "go-file", "spec-json", "pseudo")
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

	prior, err := pseudo.FromInput(goSrc, specEntry)
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
