package app

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"maps"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"uuid"

	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/ui"
	"github.com/getkin/kin-openapi/openapi3"
)

// maxHAREntries bounds how many entries one HAR import turns into requests.
const maxHAREntries = 1000

// isHAR says whether data is a HAR document: an object with log.entries.
func isHAR(data []byte) bool {
	var top struct {
		Log struct {
			Entries jsontext.Value `json:"entries"`
		} `json:"log"`
	}
	return json.Unmarshal(data, &top) == nil && top.Log.Entries.Kind() == '['
}

var openAPIKey = regexp.MustCompile(`(?m)^["']?openapi["']?[ \t]*:`)

// isOpenAPI says whether data is an OpenAPI document: JSON or YAML with a
// top-level openapi field. Parsing decides which versions are supported.
func isOpenAPI(data []byte) bool {
	var top struct {
		OpenAPI jsontext.Value `json:"openapi"`
	}
	if json.Unmarshal(data, &top) == nil {
		return len(top.OpenAPI) > 0
	}
	return openAPIKey.Match(data)
}

// collectionSegment makes a name one level of a collection path, as the
// Postman importer does: a slash in it becomes a division slash.
func collectionSegment(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(name, "/", "∕"))
}

// collectionNames are the library's collections: the first level of every
// request's path and of every collection's or folder's settings.
func (a *app) collectionNames() []string {
	names := map[string]bool{}
	for _, r := range a.requests {
		if root := collectionRoot(r.Collection); root != "" {
			names[root] = true
		}
	}
	for _, f := range a.folders {
		if root := collectionRoot(f.Path); root != "" {
			names[root] = true
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// newCollectionName is name, or name with a count after it when the
// library already has a collection of that name.
func (a *app) newCollectionName(name string) string {
	taken := a.collectionNames()
	if !slices.Contains(taken, name) {
		return name
	}
	for n := 2; ; n++ {
		if candidate := fmt.Sprintf("%s %d", name, n); !slices.Contains(taken, candidate) {
			return candidate
		}
	}
}

// fileRequest appends r to list, its name made unique within its folder.
func fileRequest(list []savedRequest, r savedRequest) []savedRequest {
	peers := slices.DeleteFunc(slices.Clone(list), func(other savedRequest) bool { return other.Collection != r.Collection })
	r.Name = uniqueRequestName(peers, r.Name)
	return append(list, r)
}

// importSkips counts what an import left out, by reason, in the order the
// reasons first came up.
type importSkips struct {
	reasons []string
	counts  map[string]int
}

func (s *importSkips) add(reason string) {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	if s.counts[reason] == 0 {
		s.reasons = append(s.reasons, reason)
	}
	s.counts[reason]++
}

func (s *importSkips) lines(one, many string) []string {
	var out []string
	for _, reason := range s.reasons {
		n := s.counts[reason]
		out = append(out, fmt.Sprintf("%d %s skipped: %s", n, plural(n, one, many), reason))
	}
	return out
}

// collectionRequest converts an exchange for a collection the way Save as
// request does, or says why it has no place in one.
func collectionRequest(f flow) (savedRequest, string) {
	u, err := url.Parse(f.url)
	switch {
	case f.kind == "connect" || f.method == "CONNECT":
		return savedRequest{}, "CONNECT tunnel, no HTTP request to save"
	case f.kind == "ws" || err == nil && (u.Scheme == "ws" || u.Scheme == "wss"):
		return savedRequest{}, "WebSocket connection"
	case err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "":
		return savedRequest{}, "not an HTTP(S) URL"
	}
	r, err := f.requestDraft()
	if err != nil {
		reason, _, _ := strings.Cut(err.Error(), ": ")
		return savedRequest{}, reason
	}
	path, _, _ := strings.Cut(f.path, "?")
	r.ID = uuid.New().String()
	r.Name = strings.TrimSpace(r.Method + " " + f.host + path)
	return r, ""
}

// addToLibrary saves incoming, already named, and the settings of new
// collections, in one write.
func (a *app) addToLibrary(incoming []savedRequest, folders []request.Folder) error {
	merged := mergeRequests(a.requests, incoming)
	if len(folders) == 0 {
		if err := a.persistRequests(merged); err != nil {
			return err
		}
		a.requests = merged
		return nil
	}
	return a.writeLibrary(merged, append(slices.Clone(a.folders), folders...))
}

// importHARCollection makes a new collection of a HAR file's HTTP(S)
// requests, in capture order, named after the file.
func (a *app) importHARCollection(data []byte, path string) {
	var doc harFile
	if err := json.Unmarshal(data, &doc); err != nil {
		a.setMessage("Import HAR: " + err.Error())
		return
	}
	name := a.newCollectionName(cmp.Or(collectionSegment(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), "HAR"))
	entries := doc.Log.Entries
	var warnings []string
	if len(entries) > maxHAREntries {
		warnings = append(warnings, fmt.Sprintf("%d entries beyond the first %d were not imported", len(entries)-maxHAREntries, maxHAREntries))
		entries = entries[:maxHAREntries]
	}
	var skips importSkips
	var incoming []savedRequest
	all := slices.Clone(a.requests)
	for _, entry := range entries {
		// An entry without a URL to read still has a method and a URL that
		// say why it is skipped.
		f, _ := harFlow(entry)
		// HTTP/2 pseudo-headers belong to the wire, not to a request to send.
		maps.DeleteFunc(f.reqHeader, func(name string, _ []string) bool { return strings.HasPrefix(name, ":") })
		r, skipped := collectionRequest(f)
		if skipped != "" {
			skips.add(skipped)
			continue
		}
		r.Collection = name
		all = fileRequest(all, r)
		incoming = append(incoming, all[len(all)-1])
	}
	warnings = append(skips.lines("entry", "entries"), warnings...)
	if len(incoming) == 0 {
		a.setMessage("Import HAR: no HTTP(S) request to import; " + cmp.Or(strings.Join(warnings, "; "), "the file has no entries"))
		return
	}
	if err := a.addToLibrary(incoming, nil); err != nil {
		a.setMessage("Import HAR: " + err.Error())
		return
	}
	a.workspace, a.requestsSection = 1, 0
	a.reportImport(fmt.Sprintf("Imported “%s” · %d %s", name, len(incoming), plural(len(incoming), "request", "requests")), warnings)
}

// openAPIRequests are a document's operations as requests in collection,
// each in a folder named after its first tag. base says whether any of
// them reads {{base_url}}: the document has no absolute server for it.
func openAPIRequests(doc *openapi3.T, collection string, list []savedRequest) (incoming []savedRequest, base bool) {
	all := slices.Clone(list)
	for _, endpoint := range apiOperations(doc) {
		r := requestFromOperation(doc, endpoint)
		r.ID = uuid.New().String()
		r.Collection = collection
		if tags := endpoint.op.Tags; len(tags) > 0 && collectionSegment(tags[0]) != "" {
			r.Collection += "/" + collectionSegment(tags[0])
		}
		base = base || strings.Contains(r.URL, "{{base_url}}")
		all = fileRequest(all, r)
		incoming = append(incoming, all[len(all)-1])
	}
	return incoming, base
}

// importOpenAPICollection makes a new collection of a validated document's
// operations, named after its title.
func (a *app) importOpenAPICollection(doc *openapi3.T, fallback string) {
	name := a.newCollectionName(cmp.Or(collectionSegment(doc.Info.Title), collectionSegment(fallback), "OpenAPI"))
	incoming, base := openAPIRequests(doc, name, a.requests)
	if len(incoming) == 0 {
		a.setMessage("OpenAPI collection: the document has no operations")
		return
	}
	var folders []request.Folder
	title := fmt.Sprintf("Imported “%s” · %d %s", name, len(incoming), plural(len(incoming), "request", "requests"))
	if base {
		folders = []request.Folder{{Path: name, Variables: []request.Field{{Name: "base_url"}}}}
		title += " · set base_url in its settings"
	}
	if err := a.addToLibrary(incoming, folders); err != nil {
		a.setMessage("OpenAPI collection: " + err.Error())
		return
	}
	a.workspace, a.requestsSection = 1, 0
	a.reportImport(title, nil)
}

// collectionSave is the dialog that saves the chosen captured rows into a
// collection.
type collectionSave struct {
	open    bool
	flows   []flow
	name    string
	perHost bool
}

func (a *app) openCollectionSave(flows []flow) {
	hosts := map[string]bool{}
	for _, f := range flows {
		hosts[f.host] = true
	}
	a.bulkSaveUI = collectionSave{open: true, flows: flows, perHost: len(hosts) > 1}
	if len(flows) > 0 {
		a.bulkSaveUI.name = "Captured " + flows[0].host
	}
}

// saveFlowsToCollection adds the flows' requests to the collection, a new
// one or the end of an existing one, without sending them.
func (a *app) saveFlowsToCollection(flows []flow, name string, perHost bool) {
	name = collectionSegment(name)
	if name == "" {
		a.setMessage("Save to collection: name the collection")
		return
	}
	var skips importSkips
	var incoming []savedRequest
	all := slices.Clone(a.requests)
	for _, f := range flows {
		r, skipped := collectionRequest(f)
		if skipped != "" {
			skips.add(skipped)
			continue
		}
		r.Source = newDebugSource(f)
		r.Collection = name
		if perHost && f.host != "" {
			r.Collection += "/" + collectionSegment(f.host)
		}
		all = fileRequest(all, r)
		incoming = append(incoming, all[len(all)-1])
	}
	warnings := skips.lines("row", "rows")
	if len(incoming) == 0 {
		a.setMessage("Save to collection: nothing saved; " + strings.Join(warnings, "; "))
		return
	}
	if err := a.addToLibrary(incoming, nil); err != nil {
		a.setMessage("Save to collection: " + err.Error())
		return
	}
	a.reportImport(fmt.Sprintf("Saved %d %s to “%s”; not sent", len(incoming), plural(len(incoming), "request", "requests"), name), warnings)
}

func (a *app) collectionSaveModal(c *ui.Context, t *ui.Theme) {
	s := &a.bulkSaveUI
	if !s.open {
		return
	}
	uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, fmt.Sprintf("Save %d requests to collection", len(s.flows)))
			names := a.collectionNames()
			name := collectionSegment(s.name)
			hint := "Creates a new collection."
			if slices.Contains(names, name) {
				hint = "Adds to the end of this collection."
			}
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Children(func() {
				ui.Text(c, "Saved in capture order, not sent. Tunnels, WebSocket connections and incomplete or non-UTF-8 bodies are skipped.").FontSize(uikit.TextBody).TextColor(t.TextMuted)
				ui.Form(c, func() {
					ui.Field(c, "Collection", func() {
						ui.Autocomplete(c, &s.name, names).Label("Collection name").Height(uikit.HeightControl).FontSize(uikit.TextBody)
					}).Description(hint)
					ui.Field(c, "Folders", func() {
						ui.Checkbox(c, &s.perHost, "One folder per host").FontSize(uikit.TextBody)
					})
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					s.open = false
				}
				if uikit.PrimaryButton(c, "Save requests").Disabled(name == "").Clicked() {
					s.open = false
					a.saveFlowsToCollection(s.flows, s.name, s.perHost)
				}
			})
		})
	})
}
