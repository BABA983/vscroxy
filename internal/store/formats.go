package store

// The app's file formats. Every file the app owns is one of these; a format
// with no Steps yet is version 1. Compatibility work has started: changing a
// persisted format gives it its own Current here (N+1, as MCPConsents does),
// leaving store.Version alone, and adds one pure Step per version left behind.
// The format's existing testdata/ fixture is the old-version file: keep it (or
// move it to a versioned name) and add a new-version expectation, with a
// migration test in version_test.go (see AGENTS.md).
//
// Not listed here because they are content, not app state: request scripts and
// addons under tools/, hosts text under tools/hosts, response bodies under
// mocks/, a compiled protobuf descriptor set, experiment spill bodies (raw
// request/response bytes; their directory name carries the history format
// version) and the binary key files (vault.key, certificates.key, upstream.key,
// mcp-consents.key). A format version on those would be a comment the tool
// that owns the file does not understand.
//
// Not listed because they are ephemeral state, not settings read across
// launches: control.json (a loopback port and token, published with a hard
// link, removed on exit and reclaimed when stale) and proxy-recovery.json (a
// proxy snapshot written by the recovery helper and removed after restore).
var (
	Settings           = &Format{Name: "settings"}
	Rules              = &Format{Name: "rules", Member: "rules"}
	SSL                = &Format{Name: "ssl"}
	Env                = &Format{Name: "environments"}
	ProjectEnv         = &Format{Name: "project environments"}
	Views              = &Format{Name: "saved capture filters", Member: "filters"}
	APIs               = &Format{Name: "OpenAPI documents", Member: "apis"}
	Breakpoints        = &Format{Name: "breakpoint templates", Member: "templates"}
	NetworkPresets     = &Format{Name: "network presets"}
	HostsProfiles      = &Format{Name: "host profiles"}
	Cookies            = &Format{Name: "cookies"}
	AndroidProxy       = &Format{Name: "Android proxy records", Member: "records"}
	Session            = &Format{Name: "session"}
	Collection         = &Format{Name: "collection"}
	Vault              = &Format{Name: "vault"}
	CertificateSecrets = &Format{Name: "certificate passphrases"}
	UpstreamSecrets    = &Format{Name: "upstream proxy passwords"}
	OAuthTokens        = &Format{Name: "OAuth tokens"}
	Experiments        = &Format{Name: "experiment history"}
	ProtobufTypes      = &Format{Name: "protobuf message types"}
	MCPConsents        = &Format{Name: "MCP consents", Current: 2, Steps: []Step{
		// Version 1 had no shape/full distinction; discard its approvals.
		func([]byte) ([]byte, error) {
			return []byte(`{"format":"vscroxy-mcp-consents","version":2,"approvals":{}}`), nil
		},
	}}
	Queries = &Format{Name: "saved JSON queries", Member: "queries"}
	Tabs    = &Format{Name: "open tabs"}

	Lists = &ListFormat{Name: "host and app lists"}
)
