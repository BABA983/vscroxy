package main

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
func (a *app) visibleJSON() []jsonRow {
	var out []jsonRow
	var walk func(nodes []*jsonNode, depth int)
	walk = func(nodes []*jsonNode, depth int) {
		for _, n := range nodes {
			out = append(out, jsonRow{node: n, depth: depth})
			if len(n.children) > 0 && a.jsonExpanded[n.path] {
				walk(n.children, depth+1)
			}
		}
	}
	walk(a.jsonRoots, 0)
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

// bodyView draws the Body tab: the request or the response, as text, as a
// JSON tree, or as a hex dump, with a search that filters what shows.
func (a *app) bodyView(c *ui.Context, f flow) {
	t := c.Theme()
	if a.detailFor != f.id {
		// The table took the click after the detail was synced, so the body
		// still belongs to the previous flow: show it next frame, when
		// syncDetail has caught up, and never cache it under this flow.
		return
	}
	body := a.body
	if a.bodySide == 1 {
		body = a.reqBody
	}
	// Explicit protocol content types keep the inspector available even for
	// malformed/empty bodies, so framing errors and trailers-only statuses show.
	key := fmt.Sprintf("%d:%d", f.id, a.bodySide)
	if key != a.protoFor {
		a.protoFor = key
		// Display choices belong to this request side; a previous image or
		// binary response must not leave the next JSON response unreadable.
		a.bodyMode = 0
		ct := f.contentType(a.bodySide == 0)
		a.protoOK = grpcContentType(ct) || strings.Contains(ct, "protobuf")
		if !a.protoOK && len(body) > 0 && len(body) <= 512<<10 {
			_, err := decodeProto([]byte(body))
			a.protoOK = err == nil
		}
	}
	modes := []string{"Auto", "Pretty", "JSON", "Hex", "Preview"}
	if a.protoOK {
		modes = append(modes, "Protobuf")
	}
	if a.bodyMode >= len(modes) {
		a.bodyMode = 0
	}
	ui.Row(c).Padding(8, 12).Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
		ui.Segmented(c, &a.bodySide, "Response", "Request")
		ui.Segmented(c, &a.bodyMode, modes...)
	})
	if key != fmt.Sprintf("%d:%d", f.id, a.bodySide) {
		c.Invalidate()
		return // rebuild the chosen side before parsing or copying its bytes
	}
	mode := a.bodyMode
	if mode == 0 {
		ct := f.contentType(a.bodySide == 0)
		switch {
		case grpcContentType(ct) || strings.Contains(ct, "protobuf"):
			mode = 5
		case strings.HasPrefix(ct, "image/"), strings.Contains(ct, "html"):
			mode = 4
		case ct == "application/octet-stream", !utf8.ValidString(body), strings.ContainsRune(body, 0):
			mode = 3
		default:
			mode = 1
		}
	}
	ui.Row(c).Padding(0, 12, 8).Gap(12).Children(func() {
		ui.Text(c, "Showing: "+modes[mode]).FontSize(11).TextColor(t.TextMuted)
		size := humanBytes(len(body))
		if body == "" {
			size = "0 B"
		}
		meta := fmt.Sprintf("%s · %s shown", cmp.Or(f.contentType(a.bodySide == 0), "No Content-Type"), size)
		ui.Text(c, meta).FontSize(10).TextColor(t.TextMuted).SingleLine().Grow(1).Tooltip(meta)
		binary := !utf8.ValidString(body) || strings.ContainsRune(body, 0)
		tip := "Copy captured preview text, without formatting or warning text. A limited preview is not the whole body."
		if binary {
			tip = "Binary bytes cannot be copied to a text clipboard; use HAR or session export to retain them"
		}
		if ui.Button(c, "Copy shown body").FontSize(11).Disabled(body == "" || binary).Tooltip(tip).Clicked() {
			c.Services().WriteClipboard(body)
			a.setMessage("Copied shown body; review credentials before sharing")
		}
	})
	if notice := strings.TrimSpace(a.bodyNotice[a.bodySide]); notice != "" {
		ui.Text(c, notice).FontSize(11).TextColor(t.Warning).Padding(6, 12).Tooltip(notice)
	}
	if mode == 5 {
		a.protobuf.view(c, a, f)
		return
	}
	if body == "" {
		message := "Empty body"
		if a.bodyNotice[a.bodySide] != "" {
			message = "No body bytes available; see the capture warning above"
		} else if a.bodySide == 0 && (f.method == "HEAD" || f.status == 204 || f.status == 304) {
			message = "No response body expected for " + f.method + " / " + strconv.Itoa(f.status)
		}
		ui.Text(c, message).FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}

	if mode == 2 || mode == 3 {
		placeholder := "Filter JSON paths and values…"
		if mode == 3 {
			placeholder = "Filter hex rows (hex bytes or text)…"
		}
		ui.TextInput(c, &a.bodyQuery).Label("Search body").Placeholder(placeholder).Tooltip(placeholder).Margin(0, 12, 8).FillWidth()
	}

	switch mode {
	case 2:
		if key != a.jsonFor {
			a.jsonFor = key
			a.jsonRoots, a.jsonOK = parseJSON(body)
			a.jsonExpanded = make(map[string]bool)
			for _, n := range a.jsonRoots {
				a.jsonExpanded[n.path] = true
				for _, child := range n.children {
					a.jsonExpanded[child.path] = true
				}
			}
		}
		if !a.jsonOK {
			ui.Text(c, "Body is not JSON; showing it as text").FontSize(11).TextColor(t.TextMuted).Padding(12)
			a.prettyBody(c, body)
			return
		}
		a.jsonBody(c)
	case 3:
		a.hexBody(c, body)
	case 4:
		a.previewBody(c, f, body)
	default:
		a.prettyBody(c, body)
	}
}

// previewBody shows a body the way it is meant to be seen: an image at its
// size, or HTML as rich text.
func (a *app) previewBody(c *ui.Context, f flow, body string) {
	t := c.Theme()
	ct := f.contentType(a.bodySide == 0)
	key := fmt.Sprintf("%d:%d", f.id, a.bodySide)
	switch {
	case strings.HasPrefix(ct, "image/"):
		a.imagePreview(c, key, body)
	case ct == "text/html" || ct == "application/xhtml+xml":
		a.htmlPreview(c, key, body)
	default:
		ui.Text(c, "No preview for "+cmp.Or(ct, "this body")).FontSize(12).TextColor(t.TextMuted).Padding(16)
		if ui.Button(c, "Show as text").Margin(0, 12).Clicked() {
			a.bodyMode = 1
		}
		if ui.Button(c, "Show as hex").Margin(0, 12).Clicked() {
			a.bodyMode = 3
		}
	}
}

// imagePreview decodes the body once and shows it, scaled to the pane.
func (a *app) imagePreview(c *ui.Context, key, body string) {
	t := c.Theme()
	if a.imageFor != key {
		a.imageFor, a.imageErr, a.imageBmp = key, "", nil
		bmp, err := ui.DecodeBitmap([]byte(body))
		if err != nil {
			a.imageErr = err.Error()
		} else {
			a.imageBmp = bmp
		}
	}
	if a.imageErr != "" {
		ui.Text(c, "Cannot decode image: "+a.imageErr).FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	if a.imageBmp == nil {
		return
	}
	w, h := a.imageBmp.Size()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(16).Gap(6).Children(func() {
			ui.Text(c, fmt.Sprintf("%d × %d px", w, h)).FontSize(11).TextColor(t.TextMuted)
			ui.Image(c, a.imageBmp).MaxWidthPercent(100)
		})
	})
}

// htmlPreview renders the body as rich text, read-only, keeping the parsed
// document so the pane keeps its scroll.
func (a *app) htmlPreview(c *ui.Context, key, body string) {
	if a.htmlFor != key {
		a.htmlFor = key
		a.htmlDoc = richtext.NewDocument(richtext.ParseHTML(body))
	}
	richtext.EditorBase(c, a.htmlDoc, richtext.EditorOptions{ReadOnly: true}).Grow(1)
}

func (a *app) prettyBody(c *ui.Context, body string) {
	reader := a.bodyText[a.bodySide]
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
		a.bodyText[a.bodySide] = reader
	}
	if ui.Button(c, "Find in text…").FontSize(11).Margin(0, 12, 6).Tooltip("Search this body without hiding surrounding lines · Ctrl/Cmd+F in the reader").Clicked() {
		reader.Run(editor.Find)
	}
	editor.View(c, reader).Bind(&a.bodyReader[a.bodySide]).Grow(1)
}

// Keep the library's search semantics, with controls that fit a side inspector.
func bodyFindBar(c *ui.Context, bar ui.Element, find *editor.FindBar) {
	t := c.Theme()
	bar.Row().Gap(6).Padding(6).AlignItems(ui.Center).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
	bar.Children(func() {
		find.QueryInput(ui.TextInput(c, find.Query()).Label("Search body").Placeholder("Find in body…").Width(140).FontSize(11))
		ui.Text(c, find.Status()).FontSize(10).TextColor(t.TextMuted).MinWidth(40).Role(ui.RoleStatus)
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

func (a *app) jsonBody(c *ui.Context) {
	t := c.Theme()
	var rows []jsonRow
	if a.bodyQuery != "" {
		rows = matchingJSON(a.jsonRoots, a.bodyQuery)
	} else {
		rows = a.visibleJSON()
	}
	if len(rows) == 0 {
		ui.Text(c, "No matches").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.List(c, &a.jsonScroll[a.bodySide], len(rows), func(i int) {
		row := rows[i]
		ui.Row(c).Gap(6).Padding(3, 12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(float32(row.depth) * 16)
			if len(row.node.children) > 0 && a.bodyQuery == "" {
				arrow := "▸"
				if a.jsonExpanded[row.node.path] {
					arrow = "▾"
				}
				if ui.Button(c, arrow).Width(20).Height(20).Padding(0).Clicked() {
					a.jsonExpanded[row.node.path] = !a.jsonExpanded[row.node.path]
				}
			} else {
				ui.Box(c).Width(20)
			}
			if row.node.key != "" {
				ui.Text(c, row.node.key).FontSize(12).TextColor(ui.Hex("#7c3aed")).SingleLine()
				ui.Text(c, ":").FontSize(12).TextColor(t.TextMuted)
			}
			switch row.node.kind {
			case "object":
				ui.Text(c, fmt.Sprintf("{%d}", len(row.node.children))).FontSize(12).TextColor(t.TextMuted)
			case "array":
				ui.Text(c, fmt.Sprintf("[%d]", len(row.node.children))).FontSize(12).TextColor(t.TextMuted)
			default:
				ui.Text(c, row.node.value).FontSize(12).Font("monospace").TextColor(t.Text).SingleLine()
			}
		})
	}).Grow(1)
}

func (a *app) hexBody(c *ui.Context, body string) {
	t := c.Theme()
	rows := hexRows(body, a.bodyQuery)
	if len(rows) == 0 {
		ui.Text(c, "No matches").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}
	ui.List(c, &a.hexScroll[a.bodySide], len(rows), func(i int) {
		ui.Text(c, hexLine(body, rows[i])).FontSize(11).Font("monospace").TextColor(t.Text).Padding(3, 12).SingleLine()
	}).Grow(1)
}
