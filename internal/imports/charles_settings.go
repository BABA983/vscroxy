package imports

// Charles settings exports (Tools → Import/Export Settings) keep rules as XML.
// Only the tools whose element layout public Charles exports show are read:
// Map Local, Map Remote, Block List (blacklist), Breakpoints, No Caching and
// SSL proxying locations. Everything else is reported, never guessed. The
// layout comes from public exports and from how other tools read them; no
// real Charles export has been checked against this reader.

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"

	"github.com/baba983/vscroxy/internal/capture"
)

// maxCharlesSettingsRules bounds the rules one settings export can produce.
const maxCharlesSettingsRules = 1000

// CharlesSettings is what a Charles settings export maps onto this app.
type CharlesSettings struct {
	Rules []capture.Rule
	// SSLAll decrypts every host; SSLHosts are the host patterns to add.
	SSLAll   bool
	SSLHosts []string
	// NoCaching is true when Charles turned caching off for every host.
	NoCaching bool
	// Warnings say what was left out or assumed, for the import report.
	Warnings []string
}

type charlesSettingsExport struct {
	XMLName xml.Name `xml:"charles-export"`
	Proxy   struct {
		DecryptSSL   bool                   `xml:"decryptSSL"`
		SSLLocations []charlesLocationMatch `xml:"sslLocations>locationPatterns>locationMatch"`
	} `xml:"proxyConfiguration"`
	Tools []charlesToolEntry `xml:"toolConfiguration>configs>entry"`
}

// charlesToolEntry is one <entry> of toolConfiguration: its name and the
// body element of the tool. Inner is kept only to see whether an unhandled
// tool is switched on, for the report.
type charlesToolEntry struct {
	Name          string                `xml:"string"`
	Inner         []byte                `xml:",innerxml"`
	MapLocal      *charlesMapLocal      `xml:"mapLocal"`
	MapRemote     *charlesMapRemote     `xml:"map"`
	BlockList     *charlesBlockList     `xml:"blacklist"`
	SelectedHosts *charlesSelectedHosts `xml:"selectedHostsTool"`
	Rewrite       *charlesRewrite       `xml:"rewrite"`
}

type charlesLocation struct {
	Protocol string `xml:"protocol"`
	Host     string `xml:"host"`
	Port     string `xml:"port"`
	Path     string `xml:"path"`
	Query    string `xml:"query"`
}

type charlesLocationMatch struct {
	Location charlesLocation `xml:"location"`
	Enabled  bool            `xml:"enabled"`
}

type charlesMapLocal struct {
	ToolEnabled bool                  `xml:"toolEnabled"`
	Mappings    []charlesLocalMapping `xml:"mappings>mapLocalMapping"`
}

type charlesLocalMapping struct {
	Source        charlesLocation `xml:"sourceLocation"`
	Dest          string          `xml:"dest"`
	Enabled       bool            `xml:"enabled"`
	CaseSensitive *bool           `xml:"caseSensitive"`
}

type charlesMapRemote struct {
	ToolEnabled bool                   `xml:"toolEnabled"`
	Mappings    []charlesRemoteMapping `xml:"mappings>mapMapping"`
}

type charlesRemoteMapping struct {
	Source       charlesLocation `xml:"sourceLocation"`
	Dest         charlesLocation `xml:"destLocation"`
	PreserveHost bool            `xml:"preserveHostHeader"`
	Enabled      bool            `xml:"enabled"`
}

type charlesBlockList struct {
	ToolEnabled bool                   `xml:"toolEnabled"`
	Action      int                    `xml:"action"`
	Locations   []charlesLocationMatch `xml:"locations>locationPatterns>locationMatch"`
}

type charlesSelectedHosts struct {
	ToolEnabled bool                   `xml:"toolEnabled"`
	Locations   []charlesLocationMatch `xml:"locations>locationPatterns>locationMatch"`
}

type charlesRewrite struct {
	ToolEnabled bool `xml:"toolEnabled"`
	Sets        []struct {
		Name string `xml:"name"`
	} `xml:"sets>rewriteSet"`
}

// IsCharlesSettings reports whether data looks like a settings export. It only
// routes the file; ReadCharlesSettings does the real checks.
func IsCharlesSettings(data []byte) bool {
	head := bytes.TrimPrefix(data[:min(len(data), 4096)], []byte{0xEF, 0xBB, 0xBF})
	return bytes.HasPrefix(bytes.TrimSpace(head), []byte("<")) && bytes.Contains(head, []byte("<charles-export"))
}

// ReadCharlesSettings maps a Charles settings export onto rules, SSL
// proxying and No Caching, and lists what it could not map. Nothing is
// applied here; the app saves what the result says.
func ReadCharlesSettings(data []byte, name string) (CharlesSettings, error) {
	var out CharlesSettings
	if len(data) == 0 {
		return out, fmt.Errorf("%s: the file is empty", name)
	}
	if len(data) > MaxCaptureFileSize {
		return out, fmt.Errorf("%s: the file is larger than %d MiB", name, MaxCaptureFileSize>>20)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	// Charles exports carry no DTD. Refusing one closes off entity tricks
	// (XML external entity attacks) rather than relying on the decoder.
	if bytes.Contains(bytes.ToUpper(data), []byte("<!DOCTYPE")) {
		return out, fmt.Errorf("%s: a DOCTYPE declaration is refused in a settings export", name)
	}
	if err := checkCharlesXMLDepth(data); err != nil {
		return out, fmt.Errorf("%s: %w", name, err)
	}
	var export charlesSettingsExport
	if err := xml.Unmarshal(data, &export); err != nil {
		return out, fmt.Errorf("%s: not a readable Charles settings export", name)
	}

	w := &captureWarnings{}
	for _, entry := range export.Tools {
		tool := strings.TrimSpace(entry.Name)
		switch {
		case entry.MapLocal != nil:
			out.Rules = append(out.Rules, mapLocalRules(*entry.MapLocal, w)...)
		case entry.MapRemote != nil:
			out.Rules = append(out.Rules, mapRemoteRules(*entry.MapRemote, w)...)
		case entry.BlockList != nil:
			out.Rules = append(out.Rules, blockListRules(*entry.BlockList)...)
		case tool == "Breakpoints":
			// Only one public source names the breakpoint fields. A field read
			// wrong would match every request, so none are imported.
			if bytes.Contains(entry.Inner, []byte("<breakpoint>")) {
				w.add("Breakpoints: not imported; only one public source describes the breakpoint fields, and a misread field would match every request")
			}
		case tool == "No Caching" && entry.SelectedHosts != nil:
			noCaching(*entry.SelectedHosts, w, &out)
		case entry.Rewrite != nil:
			if rw := *entry.Rewrite; rw.ToolEnabled || len(rw.Sets) > 0 {
				w.add("Rewrite: %d set(s) not imported; header and body rewrites have no equivalent rule here", len(rw.Sets))
			}
		case bytes.Contains(entry.Inner, []byte("<toolEnabled>true</toolEnabled>")) || bytes.Contains(entry.Inner, []byte("<active>true</active>")):
			w.add("%s: switched on in the export but not imported; it has no equivalent here", tool)
		}
	}
	sslLocations(export.Proxy.DecryptSSL, export.Proxy.SSLLocations, w, &out)

	if len(out.Rules) > maxCharlesSettingsRules {
		return out, fmt.Errorf("%s: more than %d rules; split the export", name, maxCharlesSettingsRules)
	}
	kept := out.Rules[:0]
	for _, r := range out.Rules {
		if err := capture.ValidateRules([]capture.Rule{r}); err != nil {
			w.add("%s rule %q could not be imported: it is not valid here", r.Name, r.Match)
			continue
		}
		kept = append(kept, r)
	}
	out.Rules = kept
	out.Warnings = append(w.finish(), charlesUnconfirmed...)
	return out, nil
}

// charlesUnconfirmed is said in every import report: the layout and the
// matching rules come from public material, not from a checked real export.
var charlesUnconfirmed = []string{
	"Charles settings were read from public export layouts, not from a checked real export: check the imported rules on first use.",
	"Location matching is exact on protocol, host, port, path and query; a default port (80 or 443) is optional. Case sensitivity follows Charles' caseSensitive flag only for Map Local.",
	"Map Remote replaces the path only when the source path is literal. No Caching with no listed locations is treated as this app's global switch.",
}

func mapLocalRules(tool charlesMapLocal, w *captureWarnings) []capture.Rule {
	var rules []capture.Rule
	for _, m := range tool.Mappings {
		dest := strings.TrimSpace(m.Dest)
		if dest == "" {
			w.add("Map Local: a mapping without a destination file or folder was not imported")
			continue
		}
		rules = append(rules, capture.Rule{
			Name: "Charles Map Local", Type: capture.RuleMapLocal,
			Match: charlesPattern(m.Source, m.CaseSensitive == nil || *m.CaseSensitive), MatchMode: "regex",
			Target: dest, Disabled: !tool.ToolEnabled || !m.Enabled,
		})
	}
	return rules
}

func mapRemoteRules(tool charlesMapRemote, w *captureWarnings) []capture.Rule {
	var rules []capture.Rule
	for _, m := range tool.Mappings {
		dest := m.Dest
		if dest.Host == "" || strings.ContainsAny(dest.Host, "*?") {
			w.add("Map Remote: a destination without a plain host was not imported")
			continue
		}
		scheme := strings.ToLower(strings.TrimSpace(dest.Protocol))
		if scheme != "http" && scheme != "https" {
			scheme = "http"
		}
		target := scheme + "://" + dest.Host
		if p := strings.TrimSpace(dest.Port); p != "" && p != "*" && !defaultPort(scheme, p) {
			target += ":" + p
		}
		r := capture.Rule{
			Name: "Charles Map Remote", Type: capture.RuleMapRemote,
			Match: charlesPattern(m.Source, true), MatchMode: "regex",
			Target: target, KeepHost: m.PreserveHost, Disabled: !tool.ToolEnabled || !m.Enabled,
		}
		if sourcePath := strings.TrimSpace(m.Source.Path); sourcePath != "" && dest.Path != "" && !strings.ContainsAny(sourcePath, "*?") {
			r.PathFrom, r.PathTo = "/"+strings.TrimPrefix(sourcePath, "/"), "/"+strings.TrimPrefix(dest.Path, "/")
		} else if dest.Path != "" {
			w.add("Map Remote %s: the destination path was not applied because the source path is a pattern", dest.Host)
		}
		if dest.Query != "" {
			w.add("Map Remote %s: the destination query was not applied", dest.Host)
		}
		rules = append(rules, r)
	}
	return rules
}

func blockListRules(tool charlesBlockList) []capture.Rule {
	var rules []capture.Rule
	for _, m := range tool.Locations {
		block := capture.BlockDrop
		if tool.Action == 1 {
			block = capture.BlockRefuse // a 403 answer; 0 drops the connection
		}
		rules = append(rules, capture.Rule{
			Name: "Charles Block List", Type: capture.RuleBlock,
			Match: charlesPattern(m.Location, true), MatchMode: "regex", Block: block,
			Disabled: !tool.ToolEnabled || !m.Enabled,
		})
	}
	return rules
}

func noCaching(tool charlesSelectedHosts, w *captureWarnings, out *CharlesSettings) {
	if !tool.ToolEnabled {
		return
	}
	if len(tool.Locations) == 0 {
		out.NoCaching = true
		return
	}
	w.add("No Caching applies to listed locations in Charles; this app's No Caching switch is global, so it was not imported")
}

// sslLocations maps SSL proxying. Charles decrypts only when decryptSSL is on,
// and a host-only list with a port is kept by host alone, since this app's
// SSL proxying is per host.
func sslLocations(decrypt bool, locations []charlesLocationMatch, w *captureWarnings, out *CharlesSettings) {
	if !decrypt {
		if len(locations) > 0 {
			w.add("SSL proxying was off in the export; its %d location(s) were not imported", len(locations))
		}
		return
	}
	for _, m := range locations {
		if !m.Enabled {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(m.Location.Host))
		if (host == "" || host == "*") && (m.Location.Port == "" || m.Location.Port == "*") {
			out.SSLAll = true
			continue
		}
		if host == "" || strings.ContainsAny(strings.TrimPrefix(host, "*."), "*?") {
			w.add("SSL proxying location %q was not imported: only host names and *.host patterns are supported", host)
			continue
		}
		out.SSLHosts = append(out.SSLHosts, host)
	}
}

// charlesPattern turns a Charles location into a whole-URL regular expression.
// An absent scheme, host, port, path or query matches anything; Charles
// wildcards (* and ?) keep their meaning. A default port is optional.
func charlesPattern(l charlesLocation, caseSensitive bool) string {
	scheme := `[A-Za-z][A-Za-z0-9+.-]*`
	if p := strings.TrimSpace(l.Protocol); p != "" && p != "*" {
		scheme = charlesGlob(p, `[A-Za-z0-9+.-]*`, `[A-Za-z0-9+.-]`)
	}
	host := `[^/?#]+`
	if h := strings.TrimSpace(l.Host); h != "" {
		host = charlesGlob(h, `[^/?#]*`, `[^/?#]`)
	}
	port := `(?::[0-9]+)?`
	switch p := strings.TrimSpace(l.Port); {
	case p == "" || p == "*":
	case p == "80" || p == "443":
		port = `(?::` + p + `)?`
	default:
		port = `:` + charlesGlob(p, `[0-9]*`, `[0-9]`)
	}
	path := `[^?]*`
	if p := strings.TrimSpace(l.Path); p != "" {
		path = charlesGlob(p, `[^?]*`, `[^?]`)
	}
	query := `(?:\?.*)?`
	if q := strings.TrimSpace(l.Query); q != "" && q != "*" {
		query = `\?` + charlesGlob(q, `.*`, `.`)
	}
	insensitive := ""
	if !caseSensitive {
		insensitive = "(?i)"
	}
	return insensitive + "^" + scheme + "://" + host + port + path + query + "$"
}

// charlesGlob escapes a literal and converts Charles wildcards to regular
// expression fragments.
func charlesGlob(s, star, one string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*':
			b.WriteString(star)
		case '?':
			b.WriteString(one)
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

func defaultPort(scheme, port string) bool {
	return scheme == "http" && port == "80" || scheme == "https" && port == "443"
}
