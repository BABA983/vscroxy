package main

import (
	"cmp"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
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
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return nil, false
	}
	return buildJSON("", v, 0, "$"), true
}

func buildJSON(key string, v any, depth int, path string) []*jsonNode {
	switch t := v.(type) {
	case map[string]any:
		node := &jsonNode{key: key, kind: "object", path: path, depth: depth}
		for _, k := range slices.Sorted(maps.Keys(t)) {
			node.children = append(node.children, buildJSON(k, t[k], depth+1, path+"."+k)...)
		}
		return []*jsonNode{node}
	case []any:
		node := &jsonNode{key: key, kind: "array", path: path, depth: depth}
		for i, item := range t {
			node.children = append(node.children, buildJSON(strconv.Itoa(i), item, depth+1, path+"["+strconv.Itoa(i)+"]")...)
		}
		return []*jsonNode{node}
	case string:
		return []*jsonNode{{key: key, kind: "string", value: strconv.Quote(t), path: path, depth: depth}}
	case float64:
		return []*jsonNode{{key: key, kind: "number", value: strconv.FormatFloat(t, 'f', -1, 64), path: path, depth: depth}}
	case bool:
		return []*jsonNode{{key: key, kind: "bool", value: strconv.FormatBool(t), path: path, depth: depth}}
	case nil:
		return []*jsonNode{{key: key, kind: "null", value: "null", path: path, depth: depth}}
	}
	return []*jsonNode{{key: key, kind: "value", value: fmt.Sprint(v), path: path, depth: depth}}
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

// filterLines keeps the lines that contain the query, with their numbers.
func filterLines(body, query string) string {
	if query == "" {
		return body
	}
	var b strings.Builder
	for i, line := range strings.Split(body, "\n") {
		if strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
			fmt.Fprintf(&b, "%5d  %s\n", i+1, line)
		}
	}
	if b.Len() == 0 {
		return "No matches"
	}
	return b.String()
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
		if mode != 5 {
			ui.TextInput(c, &a.bodyQuery).Label("Search body").Placeholder("Search body…").Grow(1)
		}
	})
	if mode == 5 {
		a.protobuf.view(c, a, f)
		return
	}
	if body == "" {
		ui.Text(c, "Empty body").FontSize(12).TextColor(t.TextMuted).Padding(16)
		return
	}

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

	switch mode {
	case 2:
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
	t := c.Theme()
	text := filterLines(body, a.bodyQuery)
	ui.Scroll(c).TrackScroll(&a.bodyScroll[a.bodySide]).Grow(1).Padding(12).Children(func() {
		ui.Text(c, text).FontSize(12).Font("monospace").TextColor(t.Text)
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
