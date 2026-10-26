package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// environment is a named set of variables a composed request uses as {{name}},
// kept with the others so a request can move between a dev and a live host.
type environment struct {
	Name string            `json:"name"`
	Vars map[string]string `json:"vars,omitempty"`
}

// envFile is what the environments file holds.
type envFile struct {
	Active string        `json:"active"`
	Envs   []environment `json:"environments"`
}

// loadEnvs reads the environments and the one in use; a missing file has none.
func loadEnvs(path string) ([]environment, string, error) {
	if path == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var f envFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, "", err
	}
	return f.Envs, f.Active, nil
}

// saveEnvs keeps the environments and which one is in use.
func saveEnvs(path string, envs []environment, active string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(envFile{Active: active, Envs: envs})
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

// expandVars replaces {{name}} with a variable's value, leaving a name it does
// not know as it is, so the mistake shows in the request.
func expandVars(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "{{") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		name := strings.TrimSpace(s[start+2 : start+end])
		if value, ok := vars[name]; ok {
			b.WriteString(value)
		} else {
			b.WriteString(s[start : start+end+2])
		}
		s = s[start+end+2:]
	}
}

// varLines reads the editor's variables, one "name = value" per line.
func varLines(text string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			vars[name] = strings.TrimSpace(value)
		}
	}
	return vars
}

// formatVars writes variables the way the editor takes them back.
func formatVars(vars map[string]string) string {
	var lines []string
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		lines = append(lines, name+" = "+vars[name])
	}
	return strings.Join(lines, "\n")
}

// testResult is one assertion a request script made.
type testResult struct {
	Name string
	Pass bool
}

// jsRequest is the req object a pre-request script gets: what it changes is
// what the proxy sends.
type jsRequest struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
	header http.Header
}

// Get is the request header named.
func (r *jsRequest) Get(name string) string { return r.header.Get(name) }

// Set sets a request header.
func (r *jsRequest) Set(name, value string) { r.header.Set(name, value) }

// Remove drops a request header.
func (r *jsRequest) Remove(name string) { r.header.Del(name) }

// jsResponse is the res object a post-response script gets.
type jsResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	header http.Header
}

// Get is the response header named.
func (r *jsResponse) Get(name string) string { return r.header.Get(name) }

// Json is the body parsed as JSON, nil when it is not JSON.
func (r *jsResponse) Json() any {
	var v any
	if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
		return nil
	}
	return v
}

// scriptRun is one execution of a request script: the variables it changed,
// the assertions it made and what it logged.
type scriptRun struct {
	vars  map[string]string
	tests []testResult
	logs  []string
}

// runScript executes src with the objects given, which are the globals the
// script sees beside vars, test and log.
func runScript(src string, run *scriptRun, objects map[string]any) error {
	return runScriptContext(context.Background(), src, run, objects)
}

// Bound user scripts as well as network I/O. Interrupt also lets Runner cancel
// a script loop without leaving a goroutine (or the UI thread) stuck in Goja.
func runScriptContext(ctx context.Context, src string, run *scriptRun, objects map[string]any) error {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil
	}
	if run.vars == nil {
		run.vars = map[string]string{}
	}
	vm := goja.New()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	vm.Set("vars", map[string]any{
		"get": func(name string) string { return run.vars[name] },
		"set": func(name, value string) { run.vars[name] = value },
	})
	vm.Set("test", func(name string, pass bool) {
		run.tests = append(run.tests, testResult{Name: name, Pass: pass})
	})
	vm.Set("log", func(values ...any) {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		run.logs = append(run.logs, strings.Join(parts, " "))
	})
	for name, value := range objects {
		if err := vm.Set(name, value); err != nil {
			return err
		}
	}
	_, err := vm.RunString(src)
	return err
}

// passed counts the assertions that held.
func (r *scriptRun) passed() int {
	n := 0
	for _, t := range r.tests {
		if t.Pass {
			n++
		}
	}
	return n
}
