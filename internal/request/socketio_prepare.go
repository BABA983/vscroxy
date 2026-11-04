package request

import "fmt"

// PrepareSocketIO shares HTTP's variables, inherited header auth and host-bound
// Vault access, but never silently executes/skips HTTP scripts or body encoders.
func (r Request) PrepareSocketIO(in Context, vars map[string]string) (*Prepared, SocketIOConfig, error) {
	if r.Protocol != "Socket.IO" {
		return nil, SocketIOConfig{}, fmt.Errorf("not a Socket.IO document")
	}
	if r.Pre != "" || r.Post != "" || r.Checks != (Checks{}) || len(r.TokenSources) > 0 {
		return nil, SocketIOConfig{}, fmt.Errorf("Socket.IO does not run HTTP scripts, Checks or token bindings; remove them first")
	}
	for _, f := range in.Folders {
		if f.Pre != "" || f.Post != "" {
			return nil, SocketIOConfig{}, fmt.Errorf("Socket.IO does not run inherited HTTP scripts; use a collection without scripts")
		}
	}
	r.Auth, _ = in.InheritAuth(r.Auth)
	switch r.Auth.Type {
	case "", "None", "Bearer", "Basic", "API key":
	default:
		return nil, SocketIOConfig{}, fmt.Errorf("Socket.IO handshake supports None, Bearer, Basic or API key auth; namespace auth is a separate JSON object")
	}
	r.Method, r.BodyMode = "GET", "Raw"
	values := in.Values(vars)
	r = r.withDynamic(values)
	if err := in.unlock(r, values); err != nil {
		return nil, SocketIOConfig{}, err
	}
	for _, name := range r.Variables() {
		if _, ok := values[name]; !ok {
			return nil, SocketIOConfig{}, fmt.Errorf("set variable {{%s}} before using Socket.IO", name)
		}
	}
	cfg := r.SocketIO
	cfg.Namespace, cfg.Auth = ExpandVars(cfg.Namespace, values), ExpandVars(cfg.Auth, values)
	cfg.Event, cfg.Attachments = ExpandVars(cfg.Event, values), ExpandVars(cfg.Attachments, values)
	r.Protocol = ""
	p, err := r.Prepare(values)
	return p, cfg, err
}
