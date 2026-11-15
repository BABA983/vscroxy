package cli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/baba983/vscroxy/internal/openapi"
)

// openapiCommand checks a spec and its local $ref files. It never fetches a
// URL; lint findings beyond structural validation are printed with their
// location, and error-severity findings fail the command.
func openapiCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, "Usage: vscroxy-cli openapi lint [--json] <spec.yaml | spec.json>")
		return 0
	}
	if args[0] != "lint" {
		fmt.Fprintln(stderr, "Unknown openapi command; use 'openapi lint'.")
		return 2
	}
	flags := flag.NewFlagSet("openapi lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "Print findings as JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "Provide one OpenAPI file.")
		return 2
	}
	if err := ctx.Err(); err != nil {
		return 130
	}
	spec, err := openapi.Load(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "Could not load OpenAPI document:", err)
		return 2
	}
	issues := openapi.Lint(spec.Doc)
	if *asJSON {
		data, err := json.Marshal(struct {
			Version int             `json:"version"`
			Files   []string        `json:"files"`
			Issues  []openapi.Issue `json:"issues"`
		}{1, spec.Files, issues}, jsontext.WithIndent("  "))
		if err != nil {
			fmt.Fprintln(stderr, "Could not encode findings.")
			return 2
		}
		if _, err := stdout.Write(append(data, '\n')); err != nil {
			return 2
		}
	} else {
		for _, issue := range issues {
			if _, err := fmt.Fprintf(stdout, "%s %s: %s (%s)\n", issue.Severity, issue.Pointer, issue.Message, issue.Code); err != nil {
				return 2
			}
		}
		if _, err := fmt.Fprintf(stdout, "%d files · %d findings\n", len(spec.Files), len(issues)); err != nil {
			return 2
		}
	}
	for _, issue := range issues {
		if issue.Severity == "error" {
			return 1
		}
	}
	return 0
}
