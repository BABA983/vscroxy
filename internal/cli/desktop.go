package cli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/control"
)

// The four commands below are thin, task-shaped clients of the same
// authenticated control endpoint as `control`. They never read desktop files
// or accept an action name, and write verbs require an explicit --confirm.

type desktopArgs struct {
	verb    string
	file    string
	confirm bool
	flags   *flag.FlagSet
}

// desktopValueFlags lets the common flags come before or after the verb and
// keeps a path or JSON value from being mistaken for one.
var desktopValueFlags = map[string]bool{
	"file": true, "id": true, "value": true, "collection": true,
	"rate-limit": true, "error-rate": true, "error-status": true, "error-body": true,
}

func desktopSplit(args []string) (verb string, flags, positional []string) {
	for i := 0; i < len(args); i++ {
		token := args[i]
		if !strings.HasPrefix(token, "-") {
			verb = token
			args = slices.Delete(slices.Clone(args), i, i+1)
			break
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(token, "-"), "=")
		if !hasValue && desktopValueFlags[name] && i+1 < len(args) {
			i++ // this next argument belongs to the flag, not the verb
		}
	}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if strings.HasPrefix(token, "-") {
			flags = append(flags, token)
			name, _, hasValue := strings.Cut(strings.TrimLeft(token, "-"), "=")
			if !hasValue && desktopValueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, token)
	}
	return verb, flags, positional
}

// defaultDesktopFile keeps task-shaped commands on the same discovery default
// as env/exec: VSCROXY_CONTROL_FILE, then control.json in the data root.
func defaultDesktopFile(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return control.DefaultFile()
}

func parseDesktopArgs(command string, args []string, stderr io.Writer, extra func(*flag.FlagSet)) (desktopArgs, error) {
	verb, flags, positional := desktopSplit(args)
	if verb == "" {
		return desktopArgs{}, errors.New("a verb is required")
	}
	parsed := desktopArgs{verb: verb}
	parsed.flags = flag.NewFlagSet(command, flag.ContinueOnError)
	parsed.flags.SetOutput(stderr)
	file := parsed.flags.String("file", "", "Private discovery file; defaults to VSCROXY_CONTROL_FILE or control.json in the data root")
	confirm := parsed.flags.Bool("confirm", false, "Explicitly authorize this action's side effects")
	if extra != nil {
		extra(parsed.flags)
	}
	if err := parsed.flags.Parse(append(flags, positional...)); err != nil {
		return desktopArgs{}, err
	}
	path, err := defaultDesktopFile(*file)
	if err != nil {
		return desktopArgs{}, errors.New("cannot find the desktop control discovery file")
	}
	parsed.file, parsed.confirm = path, *confirm
	return parsed, nil
}

func desktopUsage(stderr io.Writer, command string, err error) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintf(stderr, "%s: %v\n", command, err)
	return 2
}

func desktopGroupHelp(group string) string {
	switch group {
	case "settings":
		return "Usage: vscroxy-cli settings [--file <discovery>] [options] list|get|set\n  --file <path>          Discovery file; defaults to VSCROXY_CONTROL_FILE or control.json\n  --id <stable-id>       Setting id from 'settings get' (never a localized label)\n  --value <JSON>         New boolean, integer or JSON array value for set\n  --confirm              Authorize a set (desktop writes are confirmed)"
	case "sessions":
		return "Usage: vscroxy-cli sessions [--file <discovery>] [options] list|save|open|close\n  A session key from 'sessions list' follows open or close.\n  --confirm              Authorize save, open or close"
	case "certificates":
		return "Usage: vscroxy-cli certificates [--file <discovery>] status\n  Prints CA trust, path, SHA-256 fingerprint and HTTPS inspection hosts."
	case "mocks":
		return "Usage: vscroxy-cli mocks [--file <discovery>] [options] list|start|stop\n  --collection <path>    Saved collection whose Examples are served\n  --rate-limit N         Requests per second for start (0 for unlimited)\n  --error-rate 0–1       Injected error share for start\n  --error-status N       Injected error status, 400–599\n  --error-body <text>    Optional injected error body\n  --confirm              Authorize start or stop"
	}
	return ""
}

func desktopCall(ctx context.Context, file, action string, params map[string]any, stdout, stderr io.Writer) int {
	client, err := control.Load(file)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var result jsontext.Value
	if err := client.Call(ctx, action, params, &result); err != nil {
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
	if _, err := fmt.Fprintln(stdout, string(result)); err != nil {
		return 2
	}
	if failed {
		return 1
	}
	return 0
}

func settingsCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var id, value *string
	parsed, err := parseDesktopArgs("settings", args, stderr, func(flags *flag.FlagSet) {
		id = flags.String("id", "", "Stable setting id from settings get (not a localized label)")
		value = flags.String("value", "", "New JSON value: true, false, an integer, or a JSON array of host strings")
	})
	if err != nil {
		return desktopUsage(stderr, "settings", err)
	}
	switch parsed.verb {
	case "list":
		return desktopCall(ctx, parsed.file, "settings.list", nil, stdout, stderr)
	case "get":
		if *value != "" || parsed.flags.NArg() != 0 {
			return desktopUsage(stderr, "settings get", errors.New("no value or extra arguments"))
		}
		params := map[string]any{}
		if *id != "" {
			params["id"] = *id
		}
		action := "settings.get"
		if *id == "" {
			action = "settings.list"
		}
		return desktopCall(ctx, parsed.file, action, params, stdout, stderr)
	case "set":
		if !parsed.confirm {
			return desktopUsage(stderr, "settings set", errors.New("--confirm is required for writes"))
		}
		if *id == "" || *value == "" || parsed.flags.NArg() != 0 {
			return desktopUsage(stderr, "settings set", errors.New("--id and --value are required"))
		}
		raw := jsontext.Value(*value)
		switch {
		case !raw.IsValid():
			return desktopUsage(stderr, "settings set", errors.New("--value must be valid JSON"))
		case raw.Kind() != 't' && raw.Kind() != 'f' && raw.Kind() != '[' && !strings.ContainsRune("0123456789-", rune(raw.Kind())):
			return desktopUsage(stderr, "settings set", errors.New("--value must be a JSON boolean, integer or array"))
		}
		return desktopCall(ctx, parsed.file, "settings.set", map[string]any{"id": *id, "value": raw, "confirm": true}, stdout, stderr)
	default:
		return desktopUsage(stderr, "settings", errors.New("use list, get or set"))
	}
}

func sessionsCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseDesktopArgs("sessions", args, stderr, nil)
	if err != nil {
		return desktopUsage(stderr, "sessions", err)
	}
	action := "sessions." + parsed.verb
	params := map[string]any{}
	switch parsed.verb {
	case "list":
	case "save":
		if !parsed.confirm {
			return desktopUsage(stderr, "sessions save", errors.New("--confirm is required for writes"))
		}
		params["confirm"] = true
	case "open", "close":
		if !parsed.confirm {
			return desktopUsage(stderr, "sessions "+parsed.verb, errors.New("--confirm is required for writes"))
		}
		if parsed.flags.NArg() != 1 || parsed.flags.Arg(0) == "" {
			return desktopUsage(stderr, "sessions "+parsed.verb, errors.New("one session key from sessions list is required"))
		}
		params["key"], params["confirm"] = parsed.flags.Arg(0), true
	default:
		return desktopUsage(stderr, "sessions", errors.New("use list, save, open or close"))
	}
	return desktopCall(ctx, parsed.file, action, params, stdout, stderr)
}

func certificatesCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	parsed, err := parseDesktopArgs("certificates", args, stderr, nil)
	if err != nil {
		return desktopUsage(stderr, "certificates", err)
	}
	if parsed.verb != "status" {
		return desktopUsage(stderr, "certificates", errors.New("use status"))
	}
	return desktopCall(ctx, parsed.file, "certificates.get", nil, stdout, stderr)
}

func mocksCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var collection, errorBody *string
	var rateLimit, errorStatus *int
	var errorRate *float64
	parsed, err := parseDesktopArgs("mocks", args, stderr, func(flags *flag.FlagSet) {
		collection = flags.String("collection", "", "Saved collection path from control collections.list")
		rateLimit = flags.Int("rate-limit", 0, "Answer at most this many requests per second (0 for no limit)")
		errorRate = flags.Float64("error-rate", 0, "Inject an error for this share of requests, 0–1")
		errorStatus = flags.Int("error-status", 0, "Injected error status, 400–599 (desktop default 503)")
		errorBody = flags.String("error-body", "", "Optional injected error body")
	})
	if err != nil {
		return desktopUsage(stderr, "mocks", err)
	}
	params := map[string]any{}
	switch parsed.verb {
	case "list":
	case "start", "stop":
		if !parsed.confirm {
			return desktopUsage(stderr, "mocks "+parsed.verb, errors.New("--confirm is required for writes"))
		}
		if *collection == "" || parsed.flags.NArg() != 0 {
			return desktopUsage(stderr, "mocks "+parsed.verb, errors.New("--collection is required"))
		}
		params["collection"], params["confirm"] = *collection, true
		if parsed.verb == "start" {
			if *rateLimit < 0 || *rateLimit > 100000 || *errorRate < 0 || *errorRate > 1 || *errorStatus != 0 && (*errorStatus < 400 || *errorStatus > 599) {
				return desktopUsage(stderr, "mocks start", errors.New("rate limit 0–100000, error rate 0–1 and error status 400–599 or 0 are required"))
			}
			params["rateLimit"], params["errorRate"], params["errorStatus"], params["errorBody"] = *rateLimit, *errorRate, *errorStatus, *errorBody
		}
	default:
		return desktopUsage(stderr, "mocks", errors.New("use list, start or stop"))
	}
	action := "mock.get"
	if parsed.verb != "list" {
		action = "mock." + parsed.verb
	}
	return desktopCall(ctx, parsed.file, action, params, stdout, stderr)
}
