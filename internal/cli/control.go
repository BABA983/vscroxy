package cli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/store"
)

// controlCommand calls only the desktop's allowlisted actions. It never reads
// desktop files directly or silently starts a desktop/proxy process.
func controlCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("control", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "", "Private discovery file from VSCROXY_CONTROL_FILE")
	params := flags.String("params", "{}", "Action parameters as a JSON object; details:true MAY EXPOSE SECRETS")
	confirm := flags.Bool("confirm", false, "Explicitly authorize this action's side effects")
	output := flags.String("output", "", "Save successful JSON privately to a local file instead of stdout")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *file == "" || flags.NArg() != 1 || jsontext.Value(*params).Kind() != '{' {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli control --file <discovery> [--params <JSON object>] [--confirm] [--output <path>] <action>")
		return 2
	}
	var values map[string]jsontext.Value
	if err := json.Unmarshal([]byte(*params), &values); err != nil {
		fmt.Fprintln(stderr, "Invalid parameter object.")
		return 2
	}
	if *confirm {
		values["confirm"] = jsontext.Value("true")
	}
	if *output != "" {
		// Reuse the CLI's path/ancestor symlink protection and also reject
		// hard links: a different path can still name the discovery file.
		outInfo, outErr := os.Stat(*output)
		fileInfo, fileErr := os.Stat(*file)
		if overlaps(*output, *file) || outErr == nil && fileErr == nil && os.SameFile(outInfo, fileInfo) {
			fmt.Fprintln(stderr, "Output destination overlaps the discovery file; choose a separate file.")
			return 2
		}
	}
	client, err := control.Load(*file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var result jsontext.Value
	if err := client.Call(ctx, flags.Arg(0), values, &result); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	var failure struct {
		Error string `json:"error"`
	}
	failed := json.Unmarshal(result, &failure) == nil && failure.Error != ""
	if *output != "" {
		if failed {
			fmt.Fprintln(stderr, failure.Error)
			return 1
		}
		if err := store.WriteFile(*output, result); err != nil {
			fmt.Fprintln(stderr, "Could not write output.")
			return 2
		}
		return 0
	}
	if _, err := fmt.Fprintln(stdout, string(result)); err != nil {
		return 2
	}
	if failed {
		return 1
	}
	return 0
}
