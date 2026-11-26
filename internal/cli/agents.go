package cli

// env and exec point a terminal at the running desktop's capture proxy. The
// desktop is the only source of the proxy address and CA paths; they are read
// from the authenticated control status, never from desktop files.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/clientsetup"
	"github.com/baba983/vscroxy/internal/control"
)

const desktopUnavailableMessage = "vscroxy desktop is not reachable. Open vscroxy and enable Settings → AI agents → Allow AI agents and scripts to control vscroxy."
const captureNotRunningMessage = "vscroxy capture is not running. Start capture in vscroxy and try again."

// controlStatus is the part of the read-only status result this command uses.
type controlStatus struct {
	ProxyRunning bool   `json:"proxyRunning"`
	ProxyAddr    string `json:"proxyAddr"`
	CAPath       string `json:"caPath"`
	BundlePath   string `json:"bundlePath"`
}

// captureProxy asks the desktop for its live capture listener and public CA.
// It never returns the discovery token or reads the desktop's settings.
func captureProxy(ctx context.Context, file string, stderr io.Writer) (clientsetup.Proxy, int) {
	var p clientsetup.Proxy
	if file == "" {
		path, err := control.DefaultFile()
		if err != nil {
			fmt.Fprintf(stderr, "vscroxy-cli: cannot find the discovery file: %v\n", err)
			return p, 2
		}
		file = path
	}
	client, err := control.Load(file)
	if err != nil {
		fmt.Fprintf(stderr, "%s (%v)\n", desktopUnavailableMessage, err)
		return p, 2
	}
	var status controlStatus
	if err := client.Call(ctx, "status", nil, &status); err != nil {
		if errors.Is(err, context.Canceled) {
			return p, 130
		}
		fmt.Fprintf(stderr, "%s (%v)\n", desktopUnavailableMessage, err)
		return p, 2
	}
	if !status.ProxyRunning || status.ProxyAddr == "" {
		fmt.Fprintln(stderr, captureNotRunningMessage)
		return p, 2
	}
	if status.CAPath == "" {
		fmt.Fprintln(stderr, captureNotRunningMessage)
		return p, 2
	}
	if _, err := os.Stat(status.CAPath); err != nil {
		fmt.Fprintln(stderr, "vscroxy: the capture CA file is not available. Start capture again, then retry.")
		return p, 2
	}
	p = clientsetup.Proxy{Addr: status.ProxyAddr, CAPath: status.CAPath, BundlePath: status.BundlePath}
	if p.BundlePath != "" {
		// Clients that replace their trust store need both the system roots
		// and the capture CA. The app writes this bundle for its own terminal
		// launch; doing it here keeps env self-sufficient.
		if err := clientsetup.EnsureTrustBundle(p.CAPath, p.BundlePath); err != nil {
			fmt.Fprintf(stderr, "vscroxy: could not prepare the CA bundle: %v. Continuing with the CA file only.\n", err)
			p.BundlePath = ""
		}
	}
	return p, 0
}

func envCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("env", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("control-file", "", "Private discovery file; defaults to VSCROXY_CONTROL_FILE or control.json in the data root")
	shell := flags.String("shell", "", "Output syntax: sh, fish, powershell or cmd (default from $SHELL or the OS)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli env [--control-file <discovery>] [--shell sh|fish|powershell|cmd]")
		return 2
	}
	name := *shell
	if name == "" {
		name = defaultEnvShell(os.Getenv("SHELL"), runtime.GOOS)
	}
	name, err := normalizeShell(name)
	if err != nil {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli env [--control-file <discovery>] [--shell sh|fish|powershell|cmd]")
		return 2
	}
	proxy, code := captureProxy(ctx, *file, stderr)
	if code != 0 {
		return code
	}
	environment, err := clientsetup.Environment(proxy)
	if err != nil {
		fmt.Fprintf(stderr, "vscroxy-cli: %v\n", err)
		return 2
	}
	lines, err := shellAssignments(environment, name)
	if err != nil {
		fmt.Fprintf(stderr, "vscroxy-cli: %v\n", err)
		return 2
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return 2
		}
	}
	return 0
}

func execCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("exec", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("control-file", "", "Private discovery file; defaults to VSCROXY_CONTROL_FILE or control.json in the data root")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	command := flags.Args()
	if len(command) == 0 {
		fmt.Fprintln(stderr, "Usage: vscroxy-cli exec [--control-file <discovery>] -- <command> [args...]")
		return 2
	}
	proxy, code := captureProxy(ctx, *file, stderr)
	if code != 0 {
		return code
	}
	environment, err := clientsetup.Environment(proxy)
	if err != nil {
		fmt.Fprintf(stderr, "vscroxy-cli: %v\n", err)
		return 2
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = mergeEnvironment(os.Environ(), environment)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	err = cmd.Run()
	if err == nil {
		return 0
	}
	if ctx.Err() != nil {
		return 130
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code
		}
		return 130
	}
	fmt.Fprintf(stderr, "vscroxy-cli exec: %v\n", err)
	return 2
}

// mergeEnvironment replaces variables already present and appends the rest.
func mergeEnvironment(base, additions []string) []string {
	merged := slices.Clone(base)
	at := make(map[string]int, len(base))
	for i, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		at[name] = i
	}
	for _, entry := range additions {
		name, _, _ := strings.Cut(entry, "=")
		if i, ok := at[name]; ok {
			merged[i] = entry
			continue
		}
		at[name] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

func normalizeShell(name string) (string, error) {
	switch strings.ToLower(name) {
	case "sh", "bash", "zsh", "dash", "ksh":
		return "sh", nil
	case "fish":
		return "fish", nil
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return "powershell", nil
	case "cmd", "cmd.exe":
		return "cmd", nil
	}
	return "", errors.New("unknown shell")
}

// defaultEnvShell follows $SHELL when it names a shell it knows, and otherwise
// uses the platform's usual shell.
func defaultEnvShell(shellValue, goos string) string {
	if shellValue != "" {
		// $SHELL is a POSIX path; a Windows one may use backslashes even
		// when this binary runs elsewhere, so split on both.
		name := shellValue
		if at := strings.LastIndexAny(name, `/\`); at >= 0 {
			name = name[at+1:]
		}
		if name, err := normalizeShell(name); err == nil {
			return name
		}
	}
	if goos == "windows" {
		return "cmd"
	}
	return "sh"
}

// shellAssignments renders NAME=VALUE entries for one shell. Values are quoted
// for that shell, so a path with spaces or a quote survives a paste.
func shellAssignments(entries []string, shell string) ([]string, error) {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, errors.New("invalid environment entry")
		}
		switch shell {
		case "sh":
			lines = append(lines, "export "+name+"="+clientsetup.POSIXQuote(value))
		case "fish":
			lines = append(lines, "set -gx "+name+" "+fishQuote(value))
		case "powershell":
			lines = append(lines, "$env:"+name+" = "+powershellQuote(value))
		case "cmd":
			if strings.Contains(value, `"`) {
				return nil, errors.New("cmd cannot represent a double quote in a value")
			}
			lines = append(lines, `set "`+name+"="+value+`"`)
		default:
			return nil, errors.New("unknown shell")
		}
	}
	return lines, nil
}

// fishQuote wraps a value in fish single quotes, where only a quote and a
// backslash need escaping.
func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// powershellQuote wraps a value in PowerShell single quotes, where a quote is
// written twice.
func powershellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
