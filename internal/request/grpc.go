package request

import (
	"fmt"
	"strings"
)

// PrepareGRPC expands the document's JSON and metadata using the same variable,
// inherited auth and host-restricted Vault scopes as HTTP. Execute runs its
// scripts, Checks and examples; this only prepares the call.
func (r Request) PrepareGRPC(in Context, vars map[string]string) (*Prepared, error) {
	if r.Protocol != "gRPC" {
		return nil, fmt.Errorf("not a gRPC document")
	}
	r.Auth, _ = in.InheritAuth(r.Auth)
	values := in.Values(vars)
	if err := in.unlock(r, values); err != nil {
		return nil, err
	}
	return r.prepareGRPC(values)
}

// prepareGRPC expands a gRPC document with values that already merged every
// variable scope, and validates what a call cannot express.
func (r Request) prepareGRPC(values map[string]string) (*Prepared, error) {
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
	r = r.withDynamic(values)
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
