package request

import (
	"fmt"
	"strings"
)

// PrepareGRPC expands the document's JSON and metadata using the same variable,
// inherited auth and host-restricted Vault scopes as HTTP. HTTP-only features
// are rejected, never silently skipped. Scripts/Checks/Runner are not gRPC APIs.
func (r Request) PrepareGRPC(in Context, vars map[string]string) (*Prepared, error) {
	if r.Protocol != "gRPC" {
		return nil, fmt.Errorf("not a gRPC document")
	}
	if r.Pre != "" || r.Post != "" || r.Checks != (Checks{}) || len(r.TokenSources) != 0 {
		return nil, fmt.Errorf("gRPC does not run HTTP scripts, Checks or token bindings; remove them first")
	}
	for _, f := range in.Folders {
		if f.Pre != "" || f.Post != "" {
			return nil, fmt.Errorf("gRPC does not run inherited HTTP scripts; use a collection without scripts")
		}
	}
	r.Auth, _ = in.InheritAuth(r.Auth)
	switch r.Auth.Type {
	case "", "None", "Bearer", "Basic":
	case "API key":
		if r.Auth.Location == "Query" {
			return nil, fmt.Errorf("gRPC API keys belong in metadata, not the query string")
		}
	default:
		return nil, fmt.Errorf("gRPC supports None, Bearer, Basic or API key metadata auth")
	}
	r.Protocol, r.Method, r.BodyMode = "", "POST", "Raw"
	values := in.Values(vars)
	r = r.withDynamic(values)
	if err := in.unlock(r, values); err != nil {
		return nil, err
	}
	for _, name := range r.Variables() {
		if _, ok := values[name]; !ok {
			return nil, fmt.Errorf("set variable {{%s}} before calling gRPC", name)
		}
	}
	// Header parsing otherwise ignores malformed lines. Metadata must not
	// quietly drop an authored credential or accept HTTP pseudo-headers.
	for line := range strings.SplitSeq(ExpandVars(r.Headers, values), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("metadata must use name: value lines")
		}
	}
	return r.Prepare(values)
}
