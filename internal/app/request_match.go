package app

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// requestMatchState belongs to the capture header, not to its editable draft.
type requestMatchState struct {
	open   bool
	forID  int
	anchor ui.Handle
	gap    float32
}

// matchingRequests only reads saved templates and ordinary variable scopes.
// In particular it must not prepare/send requests, run scripts or open the vault.
func (a *app) matchingRequests(f flow) []savedRequest {
	return a.matchingRequestsWithVars(f, a.activeVars())
}

func (a *app) matchingRequestsWithVars(f flow, environment map[string]string) []savedRequest {
	if f.kind != "" || f.method == "CONNECT" {
		return nil
	}
	target, err := url.Parse(cmp.Or(f.url, f.scheme+"://"+f.host+cmp.Or(f.path, "/")))
	if err != nil || target.Hostname() == "" {
		return nil
	}
	var matches []savedRequest
	for _, r := range a.requests {
		vars := a.inheritance(r.Collection).Values(environment)
		method := strings.ToUpper(strings.TrimSpace(cmp.Or(request.ExpandVars(r.Method, vars), "GET")))
		if method != f.method {
			continue
		}
		template, path := matchTemplateURL(r.URL, vars)
		if template == nil || !strings.EqualFold(template.Scheme, target.Scheme) ||
			!strings.EqualFold(template.Hostname(), target.Hostname()) || matchPort(template) != matchPort(target) {
			continue
		}
		if matchPath(path, target.EscapedPath()) {
			matches = append(matches, r)
		}
	}
	return matches
}

// Resolve the origin (including a leading {{baseURL}} with a path prefix),
// but retain full path-segment parameters even if the environment defines id.
// Unknown origin variables are never host wildcards. Query and fragment do not
// participate; embedded path variables expand literally, not as partial globs.
func matchTemplateURL(raw string, vars map[string]string) (*url.URL, string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{{") {
		if end := strings.Index(raw, "}}"); end >= 0 {
			prefix := raw[:end+2]
			raw = request.ExpandVars(prefix, vars) + raw[end+2:]
		}
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, ""
	}
	authority, path := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	origin := request.ExpandVars(scheme+"://"+authority, vars)
	if strings.ContainsAny(origin, "{}") {
		return nil, ""
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return nil, ""
	}
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if !matchParameter(part) {
			parts[i] = request.ExpandVars(part, vars)
		}
	}
	return u, strings.Join(parts, "/")
}

func matchPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func matchParameter(segment string) bool {
	name := ""
	switch {
	case strings.HasPrefix(segment, "{{") && strings.HasSuffix(segment, "}}"):
		name = strings.TrimSpace(segment[2 : len(segment)-2])
	case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
		name = segment[1 : len(segment)-1]
	case strings.HasPrefix(segment, ":"):
		name = segment[1:]
	}
	return name != "" && strings.IndexFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.'
	}) < 0
}

func matchPath(template, actual string) bool {
	want, got := strings.Split(cmp.Or(template, "/"), "/"), strings.Split(cmp.Or(actual, "/"), "/")
	if len(want) != len(got) {
		return false
	}
	for i, part := range want {
		// Split before unescaping: an encoded slash is data in one segment,
		// never permission for a parameter to span multiple segments.
		parameter := matchParameter(part)
		part, err := url.PathUnescape(part)
		value, valueErr := url.PathUnescape(got[i])
		if err != nil || valueErr != nil || (parameter && value == "") || (!parameter && part != value) {
			return false
		}
	}
	return true
}

func (a *app) capturedRequestMatch(c *ui.Context, t *ui.Theme, f flow) {
	if a.matchUI.forID != f.id {
		a.matchUI = requestMatchState{forID: f.id}
	}
	matches := a.matchingRequests(f)
	if len(matches) == 0 {
		a.matchUI.open = false
		return
	}
	label := locales.MatchesPrefix(matches[0].Name)
	if len(matches) > 1 {
		label = locales.MatchesCount(len(matches))
	}
	anchor := ui.Link(c.Key("captured-request-match"), label, "").FontSize(uikit.TextMeta).
		Bind(&a.matchUI.anchor).SingleLine().MinWidth(0).Tooltip(locales.SavedRequestsMatchingThisMethod())
	anchor.OnClick(func() {
		a.matchUI.open = !a.matchUI.open
		a.matchUI.gap = t.Space(1)
		// If MyGo must flip below the header, leave the existing URL/Send
		// row exposed. Read committed geometry in the action, not an element
		// from the previous build. The same gap is harmless above the header.
		if d := a.requestDocument; d != nil {
			bar := d.urlInput.Bounds(c)
			link := a.matchUI.anchor.Bounds(c)
			if bar.H > 0 && link.H > 0 {
				a.matchUI.gap = max(a.matchUI.gap, bar.Y+bar.H-link.Y-link.H+t.Space(3))
			}
		}
	})
	ui.PopoverBase(c, anchor, &a.matchUI.open, func(panel ui.Element) {
		// Above, left-aligned: the URL and Send controls below remain visible.
		// MyGo flips only when there is insufficient room above the anchor.
		width, height := c.Size()
		uikit.PopoverPanel(c, panel).AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorBottomLeft).
			Margin(0, 0, a.matchUI.gap, 0).Padding(t.Space(3)).Width(min(400, width-32)).MaxHeight(height - 32).Gap(t.Space(2))
		uikit.Meta(c, locales.MatchesMethodFullPathSegments())
		ui.Scroll(c).MaxHeight(min(280, height-120)).MinHeight(0).Children(func() {
			ui.Column(c).Gap(t.Space(3)).Children(func() {
				for _, r := range matches {
					ui.Column(c.Key(r.key())).Gap(t.Space(1)).Children(func() {
						uikit.Body(c, r.Name)
						uikit.Meta(c, locales.CollectionPath(cmp.Or(r.Collection, locales.Unfiled()))).SingleLine().Tooltip(r.Collection)
						uikit.Mono(c, cmp.Or(r.Method, "GET")+" "+r.URL).SingleLine().Tooltip(r.URL)
						ui.Row(c).Gap(t.Space(2)).Wrap().Children(func() {
							uikit.SecondaryButton(c, locales.OpenRequest()).Label("Open request: " + r.Name).OnClick(func() {
								a.matchUI.open = false
								a.openSaved(r.key())
							})
							uikit.SecondaryButton(c, locales.SaveResponseAsExample()).Label("Save response as example: " + r.Name).
								Disabled(f.status == 0 || f.resError != "").OnClick(func() {
								if a.saveCapturedExample(f, r.key()) {
									a.matchUI.open = false
								}
							})
						})
					})
				}
			})
		})
	})
}

// saveCapturedExample never edits/sends the captured draft. It attaches the
// captured response to the saved template, keeping expanded capture secrets out
// of Example.Request, and uses the same body rules and guarded write as Save.
func (a *app) saveCapturedExample(f flow, key string) bool {
	if f.status == 0 {
		a.warn(locales.NoCapturedResponseToSave())
		return false
	}
	// Recheck against the current environment and library at action time.
	if !slices.ContainsFunc(a.matchingRequests(f), func(r savedRequest) bool { return r.key() == key }) {
		a.warn(locales.ThisRequestNoLongerMatches())
		return false
	}
	i := slices.IndexFunc(a.requests, func(r savedRequest) bool { return r.key() == key })
	r := a.requests[i]
	header := f.respHeader
	if header == nil {
		header = request.ParseHeaderBlock(f.resHeaders)
	}
	example, err := newExample(r, &capture.Flow{
		Status: f.status, ResponseHeader: header, ResponseBody: f.resBody,
		ResponseBodySize: f.resSize, ResponseBodyFile: f.resFile, ResponseBodyError: f.resError,
	}, r.Examples)
	if err != nil {
		a.fail(locales.SaveResponseAsExample(), err)
		return false
	}
	r.Examples = append(slices.Clone(r.Examples), example)
	list := slices.Clone(a.requests)
	list[i] = r
	if err := a.persistRequests(list); err != nil {
		a.fail(locales.SaveResponseAsExample(), err)
		return false
	}
	// Keep open editors in step without saving or overwriting unrelated edits.
	for _, d := range a.documents {
		if d.savedKey == key {
			d.examples = append(slices.Clone(d.examples), example)
			d.baseline.Examples = slices.Clone(r.Examples)
			d.exampleEditor = nil
		}
	}
	a.done(locales.SavedExample(example.Name))
	return true
}
