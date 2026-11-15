package store

// The app's file formats. Every file the app owns is one of these; a format
// with no Steps yet is version 1, the first version that shipped. A format
// change adds a step and the test data that goes with it (see AGENTS.md).
//
// Not listed here because they are content, not app state: request scripts and
// addons under tools/, hosts text under tools/hosts, response bodies under
// mocks/, and a compiled protobuf descriptor set. A format version on those
// would be a comment the tool that owns the file does not understand.
var (
	Settings       = &Format{Name: "settings"}
	Rules          = &Format{Name: "rules", Member: "rules"}
	SSL            = &Format{Name: "ssl"}
	Env            = &Format{Name: "environments"}
	ProjectEnv     = &Format{Name: "project environments"}
	Views          = &Format{Name: "saved capture filters", Member: "filters"}
	APIs           = &Format{Name: "OpenAPI documents", Member: "apis"}
	Breakpoints    = &Format{Name: "breakpoint templates", Member: "templates"}
	NetworkPresets = &Format{Name: "network presets"}
	HostsProfiles  = &Format{Name: "host profiles"}
	Cookies        = &Format{Name: "cookies"}
	AndroidProxy   = &Format{Name: "Android proxy records", Member: "records"}
	Session        = &Format{Name: "session"}
	Collection     = &Format{Name: "collection"}

	Lists = &ListFormat{Name: "host and app lists"}
)
