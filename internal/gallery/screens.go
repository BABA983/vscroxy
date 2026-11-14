package gallery

// screensGroup is the app's own screens, built by internal/app from fake data
// and drawn by the app's real view. The gallery runs no proxy, so the title
// bar's pill reads Proxy stopped on the capture screens, as it does in the app
// when capture is off; the traffic in the table is the fixture's.
func (g *Gallery) screensGroup() *Group {
	type screen struct {
		id, title, note string
		width, height   int
	}
	screens := []screen{
		{"capture-empty", "Capture · empty", "No traffic yet: the empty state, the sidebar and the stopped pill", 1280, 800},
		{"capture-traffic", "Capture · traffic", "Six requests with one chosen; the inspector shows its response", 1280, 800},
		{"capture-error", "Capture · error", "A 500 chosen: the status ink, the response body and the error color", 1280, 800},
		{"capture-tunnel", "Capture · tunnel", "A CONNECT row chosen: the lock, no path, and the decrypt action", 1280, 800},
		{"capture-activity", "Capture · brushed", "The activity strip brushed over the busy half minute", 1280, 800},
		{"editor-http-empty", "Request editor · HTTP", "A new HTTP request with no response yet", 1280, 800},
		{"editor-http-response", "Request editor · response", "A captured request edited in place, sent, with a 201 and two checks", 1280, 800},
		{"editor-mqtt", "Request editor · MQTT", "An MQTT 5 session with subscriptions and a retained reading", 1280, 800},
		{"editor-grpc", "Request editor · gRPC", "A finished gRPC call with two responses and its trailers", 1280, 800},
		{"editor-websocket", "Request editor · WebSocket", "The WebSocket session over the capture screen, a message chosen", 1280, 800},
		{"settings-general", "Settings · General", "The Settings window's General page", 760, 560},
		{"settings-proxy", "Settings · Proxy", "The Proxy page, with the system proxy switch", 760, 560},
		{"settings-https", "Settings · HTTPS", "The HTTPS page and its certificate checks", 760, 560},
		{"settings-appearance", "Settings · Appearance", "The Appearance page: system, light or dark", 760, 560},
		{"settings-capture", "Settings · Capture", "The Capture page: what the proxy keeps", 760, 560},
		{"settings-devices", "Settings · Devices", "The Devices page: LAN devices and the gateway", 760, 560},
		{"settings-certificates", "Settings · Certificates", "The Certificates page: client certificates and roots", 760, 560},
		{"command-palette", "Command palette", "The palette open over the capture screen, no query yet", 1280, 800},
		{"sidebar-collapsed", "Sidebar collapsed", "The capture screen with the sidebar hidden: the content takes the window", 1280, 800},

		{"tools-rules", "Tools · Rules", "The Rules page: matching conditions, actions and the test box", 1280, 800},
		{"tools-hosts", "Tools · Host profiles", "Host profiles in priority order beside the chosen hosts file", 1280, 800},
		{"tools-scripts", "Tools · Scripts", "A rule script open with its hooks, console and parse problem", 1280, 800},
		{"tools-mocks", "Tools · Mocks", "The Mocks page: Map Local rules and the files they answer from", 1280, 800},
		{"tools-scenarios", "Tools · Scenarios", "Scenarios with their rule counts and the active one switched on", 1280, 800},
		{"client-setup", "Client setup", "One-click client setup for a local terminal, capture stopped", 1280, 800},
		{"diagnostics", "Diagnostics", "The connection checks from listener to actual traffic, first failure open", 1280, 800},
		{"https-rules", "HTTPS rules", "The decryption list and its per-app rules", 1280, 800},
		{"api-endpoints", "API · endpoints", "An OpenAPI contract with its endpoint list and operation schema", 1280, 800},
		{"runs-records", "Runs · records", "The retained run records with a baseline and one result open", 1280, 800},
		{"runs-running", "Runs · running", "The collection runner mid-run: one result in, Cancel available", 1280, 800},
		{"runs-results", "Runs · results", "The collection runner finished with one failed check", 1280, 800},
		{"sessions", "Sessions", "Two saved sessions; the newer one selected with its payload read-only", 1280, 800},
		{"collection-wizard", "Collection wizard", "The capture-to-collection wizard on its Endpoints step", 1280, 800},
		{"postman-import", "Postman import", "The import review after a Postman collection with unsupported fields", 1280, 800},
		{"variables-environment", "Variables · environment", "The environment editor with initial, current and credential rows", 1280, 800},
		{"certificates-editor", "Certificates · editor", "The certificate editor with a saved passphrase, extra roots and unsaved edits", 760, 560},
		{"protobuf-inspector", "Protobuf inspector", "A gRPC response decoded with a loaded schema, two messages", 1280, 800},
		{"sse-session", "SSE session", "A live event stream with a price event selected", 1280, 800},
		{"analysis-board", "Investigation board", "Two captured requests compared on the summary board", 1280, 800},
		{"setup-guide", "Setup guide", "The first-launch checklist with trust and decryption already done", 1280, 800},
		{"mcp-catalog", "MCP catalog", "A connected MCP server: the tools catalog beside the tool call", 1280, 800},
		{"editor-socketio", "Request editor · Socket.IO", "A Socket.IO session with namespace events and a chosen message", 1280, 800},
	}
	entries := make([]*Entry, 0, len(screens))
	for _, s := range screens {
		entries = append(entries, &Entry{ID: s.id, Title: s.title, Note: s.note, Width: s.width, Height: s.height})
	}
	return &Group{ID: "screens", Title: "Screens", Entries: entries}
}
