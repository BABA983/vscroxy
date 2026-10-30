package app

import (
	"cmp"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
)

// jsonNode is one value of a parsed body, with the children of an object or
// an array.
type jsonNode struct {
	key      string
	value    string
	kind     string
	path     string
	depth    int
	children []*jsonNode
}

type jsonRow struct {
	node  *jsonNode
	depth int
}

// parseJSON turns a body into a tree. It reports false for a body that is not
// JSON, which the Body tab then shows as text.
func parseJSON(body string) ([]*jsonNode, bool) {
	dec := jsontext.NewDecoder(strings.NewReader(body))
	root, err := readJSON(dec, "", "$", 0)
	if err != nil {
		return nil, false
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return []*jsonNode{root}, true
}

// Tokens retain object order and the original number spelling, including
// integers larger than float64 can represent exactly.
func readJSON(dec *jsontext.Decoder, key, path string, depth int) (*jsonNode, error) {
	token, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	node := &jsonNode{key: key, path: path, depth: depth}
	switch token.Kind() {
	case '{', '[':
		end := jsontext.Kind('}')
		node.kind = "object"
		if token.Kind() == '[' {
			end, node.kind = ']', "array"
		}
		for dec.PeekKind() != end {
			childKey := strconv.Itoa(len(node.children))
			childPath := path + "[" + childKey + "]"
			if node.kind == "object" {
				name, err := dec.ReadToken()
				if err != nil {
					return nil, err
				}
				childKey = name.String()
				childPath = path + "." + childKey
			}
			child, err := readJSON(dec, childKey, childPath, depth+1)
			if err != nil {
				return nil, err
			}
			node.children = append(node.children, child)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
	case '"':
		node.kind, node.value = "string", strconv.Quote(token.String())
	case '0':
		node.kind, node.value = "number", token.String()
	case 't', 'f':
		node.kind, node.value = "bool", token.String()
	case 'n':
		node.kind, node.value = "null", "null"
	}
	return node, nil
}

// visibleJSON flattens the tree to the rows the current expansion shows.
func (a *app) visibleJSON(side int) []jsonRow {
	var out []jsonRow
	var walk func(nodes []*jsonNode, depth int)
	walk = func(nodes []*jsonNode, depth int) {
		for _, n := range nodes {
			out = append(out, jsonRow{node: n, depth: depth})
			if len(n.children) > 0 && a.jsonExpanded[side][n.path] {
				walk(n.children, depth+1)
			}
		}
	}
	walk(a.jsonRoots[side], 0)
	return out
}

// matchingJSON lists the leaves whose path or value contains the query.
func matchingJSON(nodes []*jsonNode, query string) []jsonRow {
	var out []jsonRow
	var walk func(nodes []*jsonNode, depth int)
	walk = func(nodes []*jsonNode, depth int) {
		for _, n := range nodes {
			if len(n.children) > 0 {
				walk(n.children, depth+1)
				continue
			}
			if strings.Contains(strings.ToLower(n.path+" "+n.value), strings.ToLower(query)) {
				out = append(out, jsonRow{node: n, depth: depth})
			}
		}
	}
	walk(nodes, 0)
	return out
}

// hexLine renders row i of a hex dump: offset, 16 bytes, then the same bytes
// as text.
func hexLine(body string, i int) string {
	start := i * 16
	if start >= len(body) {
		return ""
	}
	chunk := []byte(body[start:min(start+16, len(body))])
	var hex strings.Builder
	for j := range 16 {
		if j < len(chunk) {
			fmt.Fprintf(&hex, "%02x ", chunk[j])
		} else {
			hex.WriteString("   ")
		}
		if j == 7 {
			hex.WriteByte(' ')
		}
	}
	var text strings.Builder
	for _, b := range chunk {
		if b >= 32 && b < 127 {
			text.WriteByte(b)
		} else {
			text.WriteByte('.')
		}
	}
	return fmt.Sprintf("%08x  %s |%s|", start, hex.String(), text.String())
}

// hexRows lists the rows of a hex dump that contain the query.
func hexRows(body, query string) []int {
	rows := (len(body) + 15) / 16
	all := make([]int, 0, rows)
	for i := range rows {
		line := hexLine(body, i)
		if query == "" || strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
			all = append(all, i)
		}
	}
	return all
}

// bodyModes lists how this side's bytes can be read, and caches whether they
// look like protobuf. The cache is per side, so two visible panes do not
// rebuild each other's decision every frame.
func (a *app) bodyModes(f flow, side int) []string {
	body := a.body
	if side == 1 {
		body = a.reqBody
	}
	key := fmt.Sprintf("%d:%d", f.id, side)
	if key != a.protoFor[side] {
		a.protoFor[side] = key
		a.bodyMode[side] = 0
		ct := f.contentType(side == 0)
		a.protoOK[side] = grpcContentType(ct) || strings.Contains(ct, "protobuf")
		if !a.protoOK[side] && len(body) > 0 && len(body) <= 512<<10 {
			_, err := decodeProto([]byte(body))
			a.protoOK[side] = err == nil
		}
	}
	modes := []string{"Auto", "Pretty", "JSON", "Hex", "Preview"}
	if a.protoOK[side] {
		modes = append(modes, "Protobuf")
	}
	return modes
}

// protobufOwner is the side whose protobuf inspector is live: the pane the
// reader chose, then the other one, so two panes never drive one schema and
// one message choice.
func (a *app) protobufOwner(f flow) int {
	for _, side := range []int{a.inspect.focus, 1 - a.inspect.focus} {
		modes := a.bodyModes(f, side)
		if len(modes) < 6 {
			continue
		}
		mode := a.bodyMode[side]
		if mode == 0 {
			body := a.body
			if side == 1 {
				body = a.reqBody
			}
			mode = a.autoBodyMode(f, side, body)
		}
		if mode == 5 {
			return side
		}
	}
	return -1
}

// autoBodyMode picks the representation a body reads best in, without
// trusting the Content-Type alone.
func (a *app) autoBodyMode(f flow, side int, body string) int {
	ct := f.contentType(side == 0)
	switch {
	case grpcContentType(ct) || strings.Contains(ct, "protobuf"):
		return 5
	case strings.HasPrefix(ct, "image/"), strings.Contains(ct, "html"):
		return 4
	case ct == "application/octet-stream", !utf8.ValidString(body), strings.ContainsRune(body, 0):
		return 3
	default:
		return 1
	}
}

// bodySideView shows one side's body: its own representation, its own
// warnings, and the bytes exactly as captured.
func (a *app) bodySideView(c *ui.Context, f flow, side int) {
	t := c.Theme()
	if a.detailFor != f.id {
		// The table took the click after the detail was synced, so the body
		// still belongs to the previous flow: show it next frame, when
		// syncDetail has caught up, and never cache it under this flow.
		return
	}
	body := a.body
	if side == 1 {
		body = a.reqBody
	}
	modes := a.bodyModes(f, side)
	if a.bodyMode[side] >= len(modes) {
		a.bodyMode[side] = 0
	}
	mode := a.bodyMode[side]
	if mode == 0 {
		mode = a.autoBodyMode(f, side, body)
		if mode >= len(modes) {
			mode = 1
		}
	}
	key := fmt.Sprintf("%d:%d", f.id, side)
	if key != a.protoFor[side] {
		c.Invalidate()
		return // rebuild the chosen side before parsing or copying its bytes
	}
	if notice := strings.TrimSpace(a.bodyNotice[side]); notice != "" {
		ui.Text(c, notice).FontSize(textMeta).TextColor(t.Warning).Background(t.Warning.Alpha(0.12)).
			Padding(t.Space(1), t.Space(3)).FillWidth().Tooltip(notice)
	}
	if mode == 5 {
		// One inspector reads one side: when both sides are protobuf, the
		// response owns it and the request says where to read.
		if owner := a.protobufOwner(f); owner != side && owner >= 0 {
			name := "response"
			if owner == 1 {
				name = "request"
			}
			ui.Text(c, "Protobuf is read in the "+name+" pane.").FontSize(textBody).TextColor(t.TextMuted).Padding(16)
			return
		}
		a.protobuf.view(c, a, f, side)
		return
	}
	if body == "" {
		message := "Empty body"
		if a.bodyNotice[side] != "" {
			message = "No body bytes available; see the capture warning above"
		} else if side == 0 && (f.method == "HEAD" || f.status == 204 || f.status == 304) {
			message = "No response body expected for " + f.method + " / " + strconv.Itoa(f.status)
		}
		ui.Text(c, message).FontSize(textBody).TextColor(t.TextMuted).Padding(16)
		return
	}
	if mode == 2 || mode == 3 {
		placeholder := "Filter JSON paths and values…"
		if mode == 3 {
			placeholder = "Filter hex rows (hex bytes or text)…"
		}
		ui.TextInput(c, &a.bodyQuery[side]).Label("Search body").Placeholder(placeholder).Tooltip(placeholder).
			FontSize(textBody).Height(heightControl).Radius(radiusControl).Margin(0, t.Space(3), t.Space(2)).FillWidth()
	}
	switch mode {
	case 2:
		if key != a.jsonFor[side] {
			a.jsonFor[side] = key
			a.jsonRoots[side], a.jsonOK[side] = parseJSON(body)
			a.jsonExpanded[side] = make(map[string]bool)
			for _, n := range a.jsonRoots[side] {
				a.jsonExpanded[side][n.path] = true
				for _, child := range n.children {
					a.jsonExpanded[side][child.path] = true
				}
			}
		}
		if !a.jsonOK[side] {
			ui.Text(c, "Body is not JSON; showing it as text").FontSize(textMeta).TextColor(t.TextMuted).Padding(12)
			a.prettyBody(c, body, side)
			return
		}
		a.jsonBody(c, side)
	case 3:
		a.hexBody(c, body, side)
	case 4:
		a.previewBody(c, f, body, side)
	default:
		a.prettyBody(c, body, side)
	}
}

// previewBody shows a body the way it is meant to be seen: an image at its
// size, or HTML as rich text.
func (a *app) previewBody(c *ui.Context, f flow, body string, side int) {
	t := c.Theme()
	ct := f.contentType(side == 0)
	key := fmt.Sprintf("%d:%d", f.id, side)
	switch {
	case strings.HasPrefix(ct, "image/"):
		a.imagePreview(c, key, body, side)
	case ct == "text/html" || ct == "application/xhtml+xml":
		a.htmlPreview(c, key, body, side)
	default:
		ui.Text(c, "No preview for "+cmp.Or(ct, "this body")).FontSize(textBody).TextColor(t.TextMuted).Padding(16)
		if secondaryButton(c, "Show as text").Margin(0, t.Space(3)).Clicked() {
			a.bodyMode[side] = 1
		}
		if secondaryButton(c, "Show as hex").Margin(0, t.Space(3)).Clicked() {
			a.bodyMode[side] = 3
		}
	}
}

// imagePreview decodes the body once and shows it, scaled to the pane.
func (a *app) imagePreview(c *ui.Context, key, body string, side int) {
	t := c.Theme()
	if a.imageFor[side] != key {
		a.imageFor[side], a.imageErr[side], a.imageBmp[side] = key, "", nil
		bmp, err := ui.DecodeBitmap([]byte(body))
		if err != nil {
			a.imageErr[side] = err.Error()
		} else {
			a.imageBmp[side] = bmp
		}
	}
	if a.imageErr[side] != "" {
		ui.Text(c, "Cannot decode image: "+a.imageErr[side]).FontSize(textBody).TextColor(t.TextMuted).Padding(16)
		return
	}
	if a.imageBmp[side] == nil {
		return
	}
	w, h := a.imageBmp[side].Size()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(t.Space(4)).Gap(t.Space(2)).Children(func() {
			ui.Text(c, fmt.Sprintf("%d × %d px", w, h)).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted)
			ui.Image(c, a.imageBmp[side]).MaxWidthPercent(100)
		})
	})
}

// htmlPreview renders the body as rich text, read-only, keeping the parsed
// document so the pane keeps its scroll.
func (a *app) htmlPreview(c *ui.Context, key, body string, side int) {
	if a.htmlFor[side] != key {
		a.htmlFor[side] = key
		a.htmlDoc[side] = richtext.NewDocument(richtext.ParseHTML(body))
	}
	richtext.EditorBase(c, a.htmlDoc[side], richtext.EditorOptions{ReadOnly: true}).Grow(1)
}

func (a *app) prettyBody(c *ui.Context, body string, side int) {
	reader := a.bodyText[side]
	if reader == nil {
		value := jsontext.Value(body)
		isJSON := value.Indent(jsontext.WithIndent("  ")) == nil
		if isJSON {
			body = string(value)
		}
		buf := editor.NewBuffer(body)
		if isJSON {
			buf.SetLanguage(editor.LanguageNamed("json"))
		}
		reader = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: "Body text", RenderFindBar: bodyFindBar}
		a.bodyText[side] = reader
	}
	reader.Font = ui.Font{Family: fontMono, Size: textBody}
	reader.Theme = bodyEditorTheme(c.Theme())
	editor.View(c, reader).Bind(&a.bodyReader[side]).Grow(1)
}

// Keep the library's search semantics, with controls that fit a side inspector.
func bodyFindBar(c *ui.Context, bar ui.Element, find *editor.FindBar) {
	t := c.Theme()
	bar.Row().Gap(6).Padding(6).AlignItems(ui.Center).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
	bar.Children(func() {
		find.QueryInput(ui.TextInput(c, find.Query()).Label("Search body").Placeholder("Find in body…").Width(140).FontSize(textBody))
		ui.Text(c, find.Status()).FontSize(textMeta).FontFeatures("tnum").TextColor(t.TextMuted).MinWidth(40).Role(ui.RoleStatus)
		if ui.Button(c, "↑").Label("Previous body match").Tooltip("Previous match · Shift+Enter").KeepFocus().Clicked() {
			find.Previous()
		}
		if ui.Button(c, "↓").Label("Next body match").Tooltip("Next match · Enter").KeepFocus().Clicked() {
			find.Next()
		}
		if ui.Button(c, "×").Label("Close body search").Tooltip("Close search · Esc").KeepFocus().Clicked() {
			find.Close()
		}
	})
}

func (a *app) jsonBody(c *ui.Context, side int) {
	t := c.Theme()
	keyColor := bodyEditorTheme(t).Tokens[editor.TokenProperty].Color
	var rows []jsonRow
	if a.bodyQuery[side] != "" {
		rows = matchingJSON(a.jsonRoots[side], a.bodyQuery[side])
	} else {
		rows = a.visibleJSON(side)
	}
	if len(rows) == 0 {
		ui.Text(c, "No matches").FontSize(textBody).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.List(c, &a.jsonScroll[side], len(rows), func(i int) {
		row := rows[i]
		ui.Row(c).Gap(t.Space(2)).Padding(0, t.Space(3)).MinHeight(heightControl).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(float32(row.depth) * 16)
			if len(row.node.children) > 0 && a.bodyQuery[side] == "" {
				arrow := "▸"
				if a.jsonExpanded[side][row.node.path] {
					arrow = "▾"
				}
				if secondaryButton(c, arrow).Size(heightCompact, heightCompact).Padding(0).Clicked() {
					a.jsonExpanded[side][row.node.path] = !a.jsonExpanded[side][row.node.path]
				}
			} else {
				ui.Box(c).Width(heightCompact)
			}
			if row.node.key != "" {
				ui.Text(c, row.node.key).FontSize(textBody).Font(fontMono).TextColor(keyColor).SingleLine().Tooltip(row.node.path)
				ui.Text(c, ":").FontSize(textBody).TextColor(t.TextMuted)
			}
			switch row.node.kind {
			case "object":
				ui.Text(c, fmt.Sprintf("{%d}", len(row.node.children))).FontSize(textBody).FontFeatures("tnum").TextColor(t.TextMuted)
			case "array":
				ui.Text(c, fmt.Sprintf("[%d]", len(row.node.children))).FontSize(textBody).FontFeatures("tnum").TextColor(t.TextMuted)
			default:
				ui.Text(c, row.node.value).FontSize(textBody).Font(fontMono).TextColor(t.Text).SingleLine()
			}
		})
	}).Grow(1)
}

func (a *app) hexBody(c *ui.Context, body string, side int) {
	t := c.Theme()
	rows := hexRows(body, a.bodyQuery[side])
	if len(rows) == 0 {
		ui.Text(c, "No matches").FontSize(textBody).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.List(c, &a.hexScroll[side], len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(textMeta).Font(fontMono).TextColor(t.Text).
			Padding(t.Space(1), t.Space(3)).SingleLine()
	}).Grow(1)
}
