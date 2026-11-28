package control

import "slices"

// Action describes one desktop control action. The desktop dispatch and the
// CLI's `control --help` both read this catalog, so a new action cannot be
// documented in one place and unnamed in the other. Descriptions are short on
// purpose: parameters live in docs/automation.md and each action's own code.
type Action struct {
	Name    string
	Summary string
}

var actions = []Action{
	{"status", "Desktop, capture, recording and proxy status"},
	{"system-proxy.get", "Read the last OS system-proxy check"},
	{"system-proxy.set-enabled", "Enable or disable the system proxy (write)"},
	{"collections.list", "List saved collection and folder paths"},
	{"requests.list", "List saved requests, optionally by collection"},
	{"requests.get", "Read one saved request; details needs --allow-sensitive"},
	{"requests.save", "Save a live captured flow as a request (write)"},
	{"flows.list", "List live flows; before/after cursor, filters"},
	{"flows.get", "Read one live flow; body:\"text\" for safe text bodies"},
	{"flows.replay", "Replay a live HTTP flow (write, sends traffic)"},
	{"flows.export-har", "Export flows as HAR; raw details need --allow-sensitive"},
	{"rules.list", "List rules with index, fingerprint, type and state"},
	{"rules.set-enabled", "Enable or disable one rule (write)"},
	{"rules.import", "Append a native rules JSON array (write)"},
	{"rules.create", "Create one typed rule (write)"},
	{"rules.update", "Replace one rule by index and fingerprint (write)"},
	{"rules.delete", "Delete one rule by index and fingerprint (write)"},
	{"request.run", "Run a saved request (write, sends traffic)"},
	{"collection.run", "Run a saved collection (write, sends traffic)"},
	{"collection.load", "Load-test a saved collection (write, sends traffic)"},
	{"runs.get", "Poll an asynchronous run or load"},
	{"runs.cancel", "Cancel an asynchronous run or load (write)"},
	{"capture.recording", "Enable or pause capture recording (write)"},
	{"collection-recording.get", "Read collection recording status and counts"},
	{"collection-recording.start", "Record captured traffic into a collection (write)"},
	{"collection-recording.stop", "Stop collection recording (write)"},
	{"mock.get", "List running collection mock servers"},
	{"mock.start", "Start a collection mock server (write)"},
	{"mock.stop", "Stop a collection mock server (write)"},
	{"openapi.lint", "Lint an API document"},
	{"openapi.sync", "Diff or apply an API document sync (write to apply)"},
	{"token-links.analyze", "Stage token-link analysis (read-only staging)"},
	{"token-links.get", "Read a token-link analysis"},
	{"token-links.prepare", "Stage a token-link proposal (read-only staging)"},
	{"token-links.apply", "Apply a reviewed token-link proposal (write)"},
	{"flows.clear", "Clear unpinned captured flows (write)"},
	{"flows.annotate", "Pin, color or note a captured flow (write)"},
	{"breakpoints.list", "List paused breakpoint exchanges"},
	{"breakpoints.resolve", "Continue or abort the breakpoint on screen (write)"},
	{"ssl.get", "Read HTTPS inspection domains"},
	{"ssl.update", "Change HTTPS inspection domains (write)"},
	{"certificates.get", "Read local CA trust and configured certificates"},
	{"settings.list", "List allowlisted non-secret settings"},
	{"settings.get", "Read one allowlisted setting"},
	{"settings.set", "Change one allowlisted setting (write)"},
	{"sessions.list", "List saved sessions"},
	{"sessions.save", "Save the live table as a session (write)"},
	{"sessions.open", "Show a saved session (write)"},
	{"sessions.close", "Close a session tab (write)"},
}

// Actions returns a copy of the action catalog in help order.
func Actions() []Action { return slices.Clone(actions) }

// KnownAction reports whether name is in the catalog. The desktop dispatch
// uses this before its switch so an unknown action is distinguishable from a
// known action that fails its parameters.
func KnownAction(name string) bool {
	return slices.ContainsFunc(actions, func(a Action) bool { return a.Name == name })
}
