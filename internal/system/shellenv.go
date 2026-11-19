package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baba983/vscroxy/internal/applog"
)

// shellEnvTimeout is VS Code's default for resolving a login shell. A profile
// that hangs must not keep child processes from starting forever.
const shellEnvTimeout = 10 * time.Second

// shellEnvKeys are the login-shell variables worth carrying into a child.
// Everything else stays as the GUI process inherited it: a shell profile can
// set credentials or proxy settings for its own interactive use that a client
// of this app must not silently pick up.
var shellEnvKeys = map[string]bool{
	"PATH":             true,
	"HOME":             true,
	"USER":             true,
	"LOGNAME":          true,
	"SHELL":            true,
	"TMPDIR":           true,
	"LANG":             true,
	"LC_ALL":           true,
	"SSH_AUTH_SOCK":    true,
	"XDG_CONFIG_HOME":  true,
	"ANDROID_HOME":     true,
	"ANDROID_SDK_ROOT": true,
	"JAVA_HOME":        true,
	"DOCKER_HOST":      true,
}

// resolvedShellEnv runs the login shell only once, like VS Code's cached
// Unix shell environment. It owns its timeout, because a cached result cannot
// belong to one caller's context; a canceled caller still keeps its own env.
var resolvedShellEnv = sync.OnceValues(func() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvTimeout)
	defer cancel()

	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("shell environment marker: %w", err)
	}
	marker := hex.EncodeToString(random[:])
	// Both /bin/sh and tcsh understand printf. Only the delimited lines are
	// read; profile output around them is ignored.
	script := "printf '%s\\n' " + marker + "; env; printf '%s\\n' " + marker
	cmd := exec.CommandContext(ctx, os.Getenv("SHELL"), "-ilc", script)
	cmd.Stderr = io.Discard // shell chatter can contain values; never log it
	setShellProcessGroup(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("login shell: %w", err)
	}
	values := parseShellEnvOutput(string(output), marker)
	if len(values) == 0 {
		return nil, errors.New("login shell did not report its environment")
	}
	return values, nil
})

// ShellEnv returns the environment to give a child process. It never blocks
// startup: Run resolves it on a background goroutine, and only child processes
// wait for that one login shell. A failure keeps the current environment.
func ShellEnv(ctx context.Context) []string {
	current := os.Environ()
	shell := os.Getenv("SHELL")
	if ctx.Err() != nil || runtime.GOOS == "windows" || shell == "" || startedFromTerminal() || inTestBinary() {
		return current
	}
	values, err := resolvedShellEnv()
	if err != nil {
		applog.For("system").Warn("using the current environment; shell environment unavailable", "err", err)
		return current
	}
	return mergeShellEnv(current, values)
}

// LookPath resolves name with the PATH the child will actually get. The
// process PATH comes second: on macOS a Finder launch finds /usr/bin there,
// while the login shell puts the user's toolchain first. exec.Cmd does not
// consult the PATH in cmd.Env, so the binary must be resolved before spawning.
func LookPath(ctx context.Context, name string) (string, error) {
	if runtime.GOOS == "windows" || strings.ContainsRune(name, os.PathSeparator) {
		return exec.LookPath(name)
	}
	merged := envValue(ShellEnv(ctx), "PATH")
	if path, ok := lookPathInPaths(name, merged, os.Getenv("PATH")); ok {
		return path, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// lookPathInPaths searches each PATH in order, like exec.LookPath does.
func lookPathInPaths(name string, paths ...string) (string, bool) {
	for _, search := range paths {
		for _, dir := range filepath.SplitList(search) {
			candidate := filepath.Join(dir, name)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, true
			}
		}
	}
	return "", false
}

func envValue(env []string, key string) string {
	// exec keeps the last duplicate, so read the same entry it would use.
	value := ""
	for _, entry := range env {
		if name, v, ok := strings.Cut(entry, "="); ok && name == key {
			value = v
		}
	}
	return value
}

// parseShellEnvOutput reads only the lines between the two random markers.
// A profile's own output and a value that happens to look like a variable
// outside the markers never enter the result.
func parseShellEnvOutput(output, marker string) map[string]string {
	values := map[string]string{}
	inside := false
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !inside {
			inside = line == marker
			continue
		}
		if line == marker {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && key != "" && shellEnvKeys[key] {
			values[key] = value
		}
	}
	return values
}

// mergeShellEnv replaces only the allowlisted variables, keeping their place
// in the current environment and leaving every other variable untouched.
func mergeShellEnv(current []string, shellValues map[string]string) []string {
	merged := slices.Clone(current)
	at := make(map[string]int, len(merged))
	for i, entry := range merged {
		if key, _, ok := strings.Cut(entry, "="); ok {
			at[key] = i
		}
	}
	for _, key := range slices.Sorted(maps.Keys(shellValues)) {
		value := shellValues[key]
		if !shellEnvKeys[key] || value == "" {
			continue
		}
		entry := key + "=" + value
		if i, ok := at[key]; ok {
			merged[i] = entry
		} else {
			merged = append(merged, entry)
		}
	}
	return merged
}

func inTestBinary() bool {
	name := filepath.Base(os.Args[0])
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}
