package app

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"mime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/inspect"
	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// nodeID is a node's place in its tree. Its parent pointer and child index
// make it unique even for repeated object keys, without allocating a string
// for every node in a large body.
type nodeID struct {
	parent *jsonNode
	index  int
}

// jsonNode is one value of a parsed body, with the children of an object or
// an array. path is an RFC 6901 pointer and is shown to the person reading
// the tree; id keys expansion state and is independent of that visible path.
type jsonNode struct {
	key      string
	value    string
	kind     string
	path     string
	id       nodeID
	depth    int
	children []*jsonNode
}

type jsonRow struct {
	node  *jsonNode
	depth int
}

// jsonTreeResult is what the JSON view keeps for one captured body. stop is
// empty for a complete document (including NDJSON); otherwise it is the single
// line saying where the fault-tolerant parser gave up.
type jsonTreeResult struct {
	roots  []*jsonNode
	ndjson bool
	stop   string
	// rawRoot is the fault-tolerant tree the reader's jsonNodes came from.
	// It is reused for breadcrumbs when the shown text is still the raw body.
	rawRoot *jsonc.Node
}

// jsonBodyResult is the answer for one body: the tree for the JSON reader
// and the Pretty text produced from the same parse. A large broken body is
// parsed and formatted once, off the UI thread.
type jsonBodyResult struct {
	tree     jsonTreeResult
	pretty   string
	prettyOK bool
	// crumbRoot and crumbText belong to the text Pretty shows. The worker
	// makes them together, so the UI never parses a fault-tolerant answer
	// a second time just to draw the breadcrumb bar.
	crumbRoot *jsonc.Node
	crumbText string
}

// jsonReader tracks one side's work. A flow or body change cancels the
// current answer, so a late worker result cannot appear under other bytes.
type jsonReader struct {
	key, body, contentType string
	ready                  bool
	pending                bool
	result                 jsonBodyResult
	answers                chan jsonBodyResult
	cancel                 context.CancelFunc
}

// parseJSON keeps strict JSON on the old decoder, so a valid body builds the
// same tree at the same cost. Everything else goes through VS Code's
// fault-tolerant parser: a truncated document keeps the values read so far,
// JSONC comments and trailing commas become part of the tree, and NDJSON is
// shown as one root per line.
func parseJSON(body string) jsonTreeResult {
	if roots, ok, _ := probeStrictJSON(body); ok {
		return jsonTreeResult{roots: roots}
	}
	return faultTolerantJSON(body)
}

// faultTolerantJSON is the worker half of parseJSON: it assumes the strict
// attempt has already failed and never runs on the UI thread.
func faultTolerantJSON(body string) jsonTreeResult {
	if lines, ok := ndjsonTree(body); ok {
		return jsonTreeResult{roots: lines, ndjson: true}
	}
	root, errs := jsonc.ParseTree(body)
	if root == nil {
		return jsonTreeResult{} // no tree: keep the old "not JSON" reader
	}
	return jsonTreeResult{
		roots:   []*jsonNode{jsoncNode(body, root, "", "", nodeID{}, 0)},
		stop:    jsonStopLine(body, errs),
		rawRoot: root,
	}
}

// probeStrictJSON reports whether a body is complete JSON and, if it is not,
// whether the first decoder error is EOF in the middle of a token or
// container. That distinguishes a capture cut off at the end from ordinary
// text that only happens to begin with a JSON token.
func probeStrictJSON(body string) ([]*jsonNode, bool, bool) {
	dec := jsontext.NewDecoder(strings.NewReader(body))
	root, err := readJSON(dec, "", "", nodeID{}, 0)
	if err != nil {
		return nil, false, errors.Is(err, io.ErrUnexpectedEOF)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, false, errors.Is(err, io.ErrUnexpectedEOF)
	}
	return []*jsonNode{root}, true, false
}

// Tokens retain object order and the original number spelling, including
// integers larger than float64 can represent exactly.
func readJSON(dec *jsontext.Decoder, key, path string, id nodeID, depth int) (*jsonNode, error) {
	token, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	node := &jsonNode{key: key, path: path, id: id, depth: depth}
	switch token.Kind() {
	case '{', '[':
		end := jsontext.Kind('}')
		node.kind = "object"
		if token.Kind() == '[' {
			end, node.kind = ']', "array"
		}
		for dec.PeekKind() != end {
			childKey := strconv.Itoa(len(node.children))
			childPath := path + "/" + childKey
			if node.kind == "object" {
				name, err := dec.ReadToken()
				if err != nil {
					return nil, err
				}
				childKey = name.String()
				childPath = path + "/" + pointerToken(childKey)
			}
			childID := nodeID{parent: node, index: len(node.children)}
			child, err := readJSON(dec, childKey, childPath, childID, depth+1)
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

func pointerToken(key string) string {
	if !strings.ContainsAny(key, "~/") {
		return key
	}
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func nodePath(n *jsonNode) string {
	if n.path == "" {
		return "/" // the root of an RFC 6901 document
	}
	return n.path
}

// jsoncNode maps one fault-tolerant node into the tree the view draws. Number
// spelling comes from the original bytes: VS Code's node value is a float64
// and would round an integer too large for one.
func jsoncNode(source string, node *jsonc.Node, key, path string, id nodeID, depth int) *jsonNode {
	n := &jsonNode{key: key, path: path, id: id, depth: depth}
	switch node.Type {
	case jsonc.NodeTypeObject:
		n.kind = "object"
		for i, property := range node.Children {
			if len(property.Children) == 0 {
				continue
			}
			name, ok := property.Children[0].Value.(string)
			if !ok {
				continue
			}
			childPath := path + "/" + pointerToken(name)
			childID := nodeID{parent: n, index: i}
			if len(property.Children) < 2 {
				n.children = append(n.children, &jsonNode{
					key: name, path: childPath, id: childID, depth: depth + 1, kind: "incomplete",
				})
				continue
			}
			n.children = append(n.children, jsoncNode(source, property.Children[1], name, childPath, childID, depth+1))
		}
	case jsonc.NodeTypeArray:
		n.kind = "array"
		for i, child := range node.Children {
			index := strconv.Itoa(i)
			n.children = append(n.children, jsoncNode(source, child, index,
				path+"/"+index, nodeID{parent: n, index: i}, depth+1))
		}
	case jsonc.NodeTypeString:
		value, _ := node.Value.(string)
		n.kind, n.value = "string", strconv.Quote(value)
	case jsonc.NodeTypeNumber:
		n.kind, n.value = "number", source[node.Offset:node.Offset+node.Length]
	case jsonc.NodeTypeBoolean:
		n.kind, n.value = "bool", source[node.Offset:node.Offset+node.Length]
	case jsonc.NodeTypeNull:
		n.kind, n.value = "null", "null"
	default:
		n.kind = "incomplete"
	}
	return n
}

// ndjsonTree treats a body of complete one-line JSON values as a stream of
// records, each a top-level tree node under /0, /1 and so on. A single
// document spanning several lines is never split this way: every line must
// parse on its own.
func ndjsonTree(body string) ([]*jsonNode, bool) {
	var roots []*jsonNode
	rest, line := body, 0
	for len(rest) > 0 {
		line++
		part, after, more := strings.Cut(rest, "\n")
		rest = after
		text := strings.TrimSuffix(part, "\r")
		if strings.TrimSpace(text) != "" {
			root, errs := jsonc.ParseTree(text)
			if root == nil || len(errs) > 0 {
				return nil, false
			}
			index := strconv.Itoa(len(roots))
			roots = append(roots, jsoncNode(text, root, strconv.Itoa(line),
				"/"+index, nodeID{index: len(roots)}, 0))
		}
		if !more {
			break
		}
	}
	return roots, len(roots) > 1
}

// jsonFamilyType covers ordinary JSON, its +json media types and the common
// newline-delimited names. Anything else is only offered the fault-tolerant
// reader when the bytes themselves prove they are a JSON stream or a capture
// cut off at the end.
func jsonFamilyType(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch media {
	case "application/json", "text/json", "application/x-ndjson", "application/ndjson",
		"ndjson", "application/jsonl", "application/x-jsonl", "jsonl",
		"application/jsonlines", "application/x-jsonlines":
		return true
	}
	return strings.HasSuffix(media, "+json")
}

func faultTolerantWanted(contentType, body string, truncated bool) bool {
	return jsonFamilyType(contentType) || truncated || looksLikeNDJSON(body)
}

// looksLikeNDJSON parses candidate lines only when their first byte is the
// start of a JSON value, so ordinary text, HTML and JavaScript never pay for
// a JSON parse here. Every nonblank line must be a complete value.
func looksLikeNDJSON(body string) bool {
	lines, rest := 0, body
	for len(rest) > 0 {
		part, after, more := strings.Cut(rest, "\n")
		rest = after
		text := strings.TrimSuffix(strings.TrimSpace(part), "\r")
		if text == "" {
			if !more {
				break
			}
			continue
		}
		switch text[0] {
		case '{', '[', '"', '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 't', 'f', 'n':
		default:
			return false
		}
		root, errs := jsonc.ParseTree(text)
		if root == nil || len(errs) > 0 {
			return false
		}
		lines++
		if !more {
			break
		}
	}
	return lines > 1
}

// jsonStopLine reports the earlier parse error as a one-based line and column
// as well as the byte offset VS Code's parser uses.
func jsonStopLine(body string, errs []jsonc.ParseError) string {
	if len(errs) == 0 {
		return ""
	}
	offset := errs[0].Offset
	for _, err := range errs[1:] {
		offset = min(offset, err.Offset)
	}
	offset = min(max(offset, 0), len(body))
	prefix := body[:offset]
	lineStart := 0
	if _, tail, found := strings.CutLast(prefix, "\n"); found {
		lineStart = len(prefix) - len(tail)
	}
	line := strings.Count(prefix, "\n") + 1
	column := utf8.RuneCountInString(prefix[lineStart:]) + 1
	return locales.JsonParsingStopped(line, column, offset)
}

// computeFaultTolerantBody is the unit of work handed to a goroutine: a
// fault-tolerant parse, a format pass, and a tree matched to whatever text
// Pretty will show. None of it runs on the UI thread.
func computeFaultTolerantBody(body string) jsonBodyResult {
	tree := faultTolerantJSON(body)
	pretty, ok := formatJSONBody(body, tree)
	result := jsonBodyResult{tree: tree, pretty: pretty, prettyOK: ok}
	shown := body
	if ok && pretty != body {
		shown = pretty
		result.crumbRoot, _ = jsonc.ParseTree(shown)
	} else {
		// The reader's own tree already names the bytes it shows.
		result.crumbRoot = tree.rawRoot
	}
	if result.crumbRoot != nil && len(shown) <= jsonBreadcrumbScanLimit {
		result.crumbText = shown
	}
	return result
}

// jsonReaderFor returns the state for one side, starting the fault-tolerant
// work only after the cheap strict attempt has failed. Strict bodies are
// ready in this frame; everything else shows the captured text until the
// worker's answer is applied on the UI thread.
func (a *app) jsonReaderFor(c *ui.Context, side int, key, body, contentType string) *jsonReader {
	r := &a.jsonReader[side]
	if r.key != key || r.body != body || r.contentType != contentType {
		if r.cancel != nil {
			r.cancel()
		}
		*r = jsonReader{key: key, body: body, contentType: contentType}
		a.jsonTrees[side] = jsonTreeResult{}
		a.jsonExpanded[side] = nil
		a.bodyText[side] = nil
	}
	if r.pending {
		a.takeJSONAnswer(c, side)
		return r
	}
	if r.ready {
		return r
	}
	roots, strict, truncated := probeStrictJSON(body)
	if strict {
		r.ready, r.result = true, jsonBodyResult{tree: jsonTreeResult{roots: roots}}
		a.storeJSONTree(side, r.result.tree)
		return r
	}
	if !faultTolerantWanted(contentType, body, truncated) {
		// Plain text, HTML and code are never reformatted or parsed as JSON.
		r.ready, r.result = true, jsonBodyResult{}
		a.storeJSONTree(side, r.result.tree)
		return r
	}
	ctx, cancel := context.WithCancel(context.Background())
	answers := make(chan jsonBodyResult, 1)
	r.pending, r.cancel = true, cancel
	if a.win == nil {
		r.answers = answers // Tester handoff; a window uses win.Update
	}
	go guarded(func() {
		result := computeFaultTolerantBody(body)
		if ctx.Err() != nil {
			return
		}
		if a.win != nil {
			a.win.Update(func() { a.finishJSONRead(side, key, body, contentType, result) })
			return
		}
		// In a Tester there is no window to schedule the handoff. Publish
		// through the channel and ask the next build pass to take it, as the
		// protobuf reader does.
		select {
		case answers <- result:
			a.invalidate()
		case <-ctx.Done():
		}
	})
	return r
}

// takeJSONAnswer moves a worker result into app state while building a
// frame. With a window the worker's win.Update callback does this instead.
func (a *app) takeJSONAnswer(c *ui.Context, side int) {
	r := &a.jsonReader[side]
	if r.answers == nil {
		return
	}
	select {
	case result := <-r.answers:
		a.finishJSONRead(side, r.key, r.body, r.contentType, result)
	default:
		c.After(50 * time.Millisecond)
	}
}

func (a *app) finishJSONRead(side int, key, body, contentType string, result jsonBodyResult) {
	r := &a.jsonReader[side]
	if r.key != key || r.body != body || r.contentType != contentType {
		return // the flow or body changed while the worker was reading
	}
	r.pending, r.ready, r.result = false, true, result
	r.answers, r.cancel = nil, nil
	// Change the text inside the reader instead of replacing it: a new
	// editor would close its find bar and lose the query, options and focus
	// on whichever frame the answer arrives. The buffer edit moves the
	// selections and the next build searches the formatted text again.
	if a.bodyText[side] != nil && result.prettyOK && result.pretty != body {
		shown, _ := prettyBodyText(body, r)
		a.bodyText[side].Buffer.Set(shown)
	}
	a.storeJSONTree(side, result.tree)
}

func (a *app) storeJSONTree(side int, tree jsonTreeResult) {
	a.jsonTrees[side] = tree
	a.jsonExpanded[side] = make(map[nodeID]bool)
	for _, n := range tree.roots {
		a.jsonExpanded[side][n.id] = true
		for _, child := range n.children {
			a.jsonExpanded[side][child.id] = true
		}
	}
}

// visibleJSON flattens the tree to the rows the current expansion shows.
func (a *app) visibleJSON(side int) []jsonRow {
	var out []jsonRow
	var walk func(nodes []*jsonNode, depth int)
	walk = func(nodes []*jsonNode, depth int) {
		for _, n := range nodes {
			out = append(out, jsonRow{node: n, depth: depth})
			if len(n.children) > 0 && a.jsonExpanded[side][n.id] {
				walk(n.children, depth+1)
			}
		}
	}
	walk(a.jsonTrees[side].roots, 0)
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
			if strings.Contains(strings.ToLower(nodePath(n)+" "+n.value), strings.ToLower(query)) {
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
	modes := []string{locales.Auto(), locales.Pretty(), "JSON", locales.Hex(), locales.Preview()}
	if a.protoOK[side] {
		modes = append(modes, "Protobuf")
	}
	if format := inspect.BodyFormat(f.bodyContentType(side == 0)); format != "" {
		modes = append(modes, format)
	}
	ct := f.contentType(side == 0)
	switch {
	case isJavaScriptType(ct):
		modes = append(modes, "JavaScript")
	case ct == "text/css":
		modes = append(modes, "CSS")
	case isMarkdownType(ct):
		modes = append(modes, "Markdown")
	}
	if looksLikeStream(ct, body) {
		modes = append(modes, "Merged stream")
	}
	if name := a.bodyFormatByType[ct]; name != "" {
		if i := slices.Index(modes, name); i >= 0 {
			a.bodyMode[side] = i
		}
	}
	return modes
}

func isJavaScriptType(ct string) bool {
	return ct == "text/javascript" || ct == "application/javascript" || ct == "application/x-javascript" ||
		ct == "text/ecmascript" || ct == "application/ecmascript"
}

func isMarkdownType(ct string) bool {
	return ct == "text/markdown" || ct == "text/x-markdown"
}

// looksLikeStream reports whether a body may be an SSE or OpenAI-style chunk
// stream, so the merged view is offered without parsing every body.
func looksLikeStream(ct, body string) bool {
	if strings.Contains(ct, "event-stream") {
		return true
	}
	if !strings.Contains(body, "choices") {
		return false
	}
	lines := 0
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		if !strings.HasPrefix(line, "{") {
			return false
		}
		lines++
	}
	return lines >= 2
}

// protobufOwner is the side whose protobuf inspector is live: the pane the
// reader chose, then the other one, so two panes never drive one schema and
// one message choice.
func (a *app) protobufOwner(f flow) int {
	for _, side := range []int{a.inspect.focus, 1 - a.inspect.focus} {
		if a.capturedShowsProtobuf(f, side) {
			return side
		}
	}
	return -1
}

// capturedShowsProtobuf reports whether one captured side's body view is
// reading its bytes as protobuf.
func (a *app) capturedShowsProtobuf(f flow, side int) bool {
	modes := a.bodyModes(f, side)
	if len(modes) <= 5 || modes[5] != "Protobuf" {
		return false
	}
	mode := a.bodyMode[side]
	if mode == 0 {
		body := a.body
		if side == 1 {
			body = a.reqBody
		}
		mode = a.autoBodyMode(f, side, body)
	}
	return mode == 5
}

// draftShowsProtobuf reports whether the editable request is reading its
// draft bytes as protobuf.
func (a *app) draftShowsProtobuf(d *requestDocument) bool {
	if d == nil {
		return false
	}
	formats := a.draftBodyFormats(d)
	view := min(max(d.bodyRead, 0), len(formats)-1)
	return formats[view] == "Protobuf"
}

// protobufOwnerForDocument picks the one side that may draw the shared
// protobuf reader while a capture is edited. The request's draft body is a
// candidate even though its bytes are not the capture's.
func (a *app) protobufOwnerForDocument(f flow, d *requestDocument) int {
	request := a.draftShowsProtobuf(d)
	response := d != nil && !d.sentSet && !d.composeWaiting && d.response == nil && a.capturedShowsProtobuf(f, 0)
	if a.inspect.focus == 1 {
		if request {
			return 1
		}
		if response {
			return 0
		}
	} else {
		if response {
			return 0
		}
		if request {
			return 1
		}
	}
	return -1
}

// protobufReadElsewhere says this side must not draw the shared reader. The
// pane that owns it is named, so the other side still says where to read.
func (a *app) protobufReadElsewhere(c *ui.Context, side int) bool {
	if a.protoOwner == side {
		return false
	}
	if a.protoOwner >= 0 {
		name := "response"
		if a.protoOwner == 1 {
			name = "request"
		}
		uikit.Hint(c, locales.ProtobufReadInPane(name)).Padding(c.Theme().Space(uikit.SpaceXL))
	}
	return true
}

// autoBodyMode uses explicit formats where possible. A failed structured
// decode stays visible as an error, with Pretty/Hex available for the evidence.
func (a *app) autoBodyMode(f flow, side int, body string) int {
	ct := f.contentType(side == 0)
	switch {
	case grpcContentType(ct) || strings.Contains(ct, "protobuf"):
		return 5
	case strings.Contains(ct, "event-stream"):
		if i := slices.Index(a.bodyModes(f, side), "Merged stream"); i >= 0 {
			return i
		}
		return 1
	case strings.HasPrefix(ct, "image/"), strings.Contains(ct, "html"):
		return 4
	case inspect.BodyFormat(f.bodyContentType(side == 0)) != "":
		return slices.Index(a.bodyModes(f, side), inspect.BodyFormat(f.bodyContentType(side == 0)))
	case inspect.BinaryContentType(ct), !utf8.ValidString(body), strings.ContainsRune(body, 0):
		return 3
	default:
		return 1
	}
}

// bodySideView shows one side's body: its own representation, its own
// warnings, and the bytes exactly as captured.
// protobufReader scrolls one side's reader: the message picker and the copy
// action stay reachable even when the pane leaves little height beneath the
// pane tabs, and a long body scrolls instead of clipping its messages.
func (a *app) protobufReader(c *ui.Context, f flow, side int) {
	// The reader's content is as tall as the pane's viewport, so its split
	// and copy footer end at the pane's bottom edge rather than under the
	// window. A pane too short for the controls and messages still scrolls
	// the whole reader.
	p := &a.protobuf
	h := p.readerHeight
	scroll := ui.Scroll(c).Label(locales.ProtobufReader()).Grow(1).MinHeight(0).FillWidth()
	scroll.Children(func() {
		ui.Column(c).Height(max(h, 320)).Padding(0, 0, c.Theme().Space(uikit.SpaceM), 0).Children(func() {
			a.protobuf.view(c, a, f, side)
		})
	})
	if bh := scroll.Bounds().H; bh > 0 {
		if bh != h {
			p.readerHeight = bh
			c.Invalidate()
		}
	} else if h == 0 && !p.readerAsked {
		p.readerAsked = true
		c.Invalidate()
	}
}

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
	// shown is a read-only display form. The captured bytes still flow to
	// copy, save, replay and the hex view.
	shown := body
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		if text, ok := a.displayText(f, side, body); ok {
			shown = text
		}
	}
	modes := a.bodyModes(f, side)
	if a.bodyMode[side] >= len(modes) {
		a.bodyMode[side] = 0
	}
	mode := a.bodyMode[side]
	if mode == 0 {
		mode = a.autoBodyMode(f, side, shown)
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
		uikit.Tip(c, uikit.Caption(c, notice).TextColor(t.Warning).Background(t.Warning.Alpha(0.12)).
			Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).FillWidth(), notice)
	}
	if modes[mode] == inspect.BodyFormat(f.bodyContentType(side == 0)) {
		a.structured[side].view(c, a, capturedInspectionBody(f, side), f.bodyContentType(side == 0), !a.bodyNoWrap[side])
		return
	}
	if modes[mode] == "Protobuf" {
		// One inspector reads one side: the other says where to read.
		if a.protobufReadElsewhere(c, side) {
			return
		}
		a.protobufReader(c, f, side)
		return
	}
	if shown != body && mode != 3 {
		a.encodingLabel(c, side)
	}
	if body == "" {
		message := locales.EmptyBody()
		if a.bodyNotice[side] != "" {
			message = locales.NoBodyBytes()
		} else if side == 0 && (f.method == "HEAD" || f.status == 204 || f.status == 304) {
			message = locales.NoResponseBodyExpected(f.method, strconv.Itoa(f.status))
		}
		uikit.Hint(c, message).Padding(t.Space(uikit.SpaceXL))
		return
	}
	if mode == 2 || mode == 3 {
		placeholder := locales.FilterJSONPaths()
		if mode == 3 {
			placeholder = locales.FilterHexRows()
		}
		uikit.Tip(c, uikit.Input(c, &a.bodyQuery[side]).Label(locales.SearchBody()).Placeholder(placeholder), placeholder).
			Height(uikit.HeightRow).Radius(uikit.RadiusControl).Margin(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM)).FillWidth()
	}
	switch mode {
	case 2:
		reader := a.jsonReaderFor(c, side, key, shown, f.contentType(side == 0))
		if !reader.ready {
			a.prettyBody(c, &a.jsonCrumbs[side], key, shown, side, reader)
			return
		}
		if len(reader.result.tree.roots) == 0 {
			uikit.Meta(c, locales.BodyIsNotJsonShowing()).Padding(t.Space(uikit.SpaceL))
			a.prettyBody(c, &a.jsonCrumbs[side], key, shown, side, reader)
			return
		}
		a.jsonBody(c, side)
	case 3:
		a.hexBody(c, body, side)
	case 4:
		a.previewBody(c, f, shown, side)
	default:
		switch modes[mode] {
		case "JavaScript", "CSS":
			a.codeBody(c, f, shown, side, modes[mode])
		case "Markdown":
			a.markdownBody(c, f, shown, side)
		case "Merged stream":
			a.mergedBody(c, f, shown, side)
		default:
			a.prettyBody(c, &a.jsonCrumbs[side], key, shown, side, a.jsonReaderFor(c, side, key, shown, f.contentType(side == 0)))
		}
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
		uikit.Hint(c, locales.NoPreviewFor(cmp.Or(ct, "this body"))).Padding(t.Space(uikit.SpaceXL))
		if uikit.SecondaryButton(c, locales.ShowAsText()).Margin(0, t.Space(uikit.SpaceL)).Clicked() {
			a.bodyMode[side] = 1
		}
		if uikit.SecondaryButton(c, locales.ShowAsHex()).Margin(0, t.Space(uikit.SpaceL)).Clicked() {
			a.bodyMode[side] = 3
		}
	}
}

// imagePreview decodes the body once and shows it, scaled to the pane.
func (a *app) imagePreview(c *ui.Context, key, body string, side int) {
	t := c.Theme()
	if a.imageFor[side] != key {
		a.imageFor[side], a.imageErr[side], a.imageBmp[side] = key, "", nil
		bmp, err := decodePreviewImage([]byte(body))
		if err != nil {
			var tooLarge imageTooLargeError
			if errors.As(err, &tooLarge) {
				a.imageErr[side] = locales.ImagePreviewTooLarge(tooLarge.width, tooLarge.height, previewImagePixels>>20)
			} else {
				a.imageErr[side] = locales.CannotDecodeImage(err.Error())
			}
		} else {
			a.imageBmp[side] = bmp
		}
	}
	if a.imageErr[side] != "" {
		uikit.Hint(c, a.imageErr[side]).Padding(t.Space(uikit.SpaceXL))
		if uikit.SecondaryButton(c, locales.ShowAsText()).Margin(0, t.Space(uikit.SpaceL)).Clicked() {
			a.bodyMode[side] = 1
		}
		if uikit.SecondaryButton(c, locales.ShowAsHex()).Margin(0, t.Space(uikit.SpaceL)).Clicked() {
			a.bodyMode[side] = 3
		}
		return
	}
	if a.imageBmp[side] == nil {
		return
	}
	w, h := a.imageBmp[side].Size()
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(t.Space(uikit.SpaceXL)).Gap(t.Space(uikit.SpaceM)).Children(func() {
			uikit.Meta(c, fmt.Sprintf("%d × %d px", w, h)).FontFeatures("tnum")
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
	// The preview keeps the page's own type, but not its edges: it sits in the
	// panel with the padding every other view has. EditorBase draws its text at
	// its own origin and ignores the element's padding, so the room comes from
	// the box around it.
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
		richtext.EditorBase(c, a.htmlDoc[side], richtext.EditorOptions{ReadOnly: true,
			OnLink: func(raw string) { a.openCapturedLink(c, raw) }}).Grow(1)
	})
}

// prettyBodyText keeps the strict formatter's exact output for valid JSON.
// Invalid bodies use the answer already computed off the UI thread; until it
// arrives the captured text is shown as received.
func prettyBodyText(body string, json *jsonReader) (string, bool) {
	value := jsontext.Value(body)
	if value.Indent(jsontext.WithIndent("  ")) == nil {
		return string(value), true
	}
	if json != nil && json.ready && json.result.prettyOK {
		return json.result.pretty, true
	}
	return body, false
}

// formatJSONBody is VS Code's formatter applied to one fault-tolerant tree:
// comments, trailing commas, truncation, trailing text and NDJSON all keep
// their original bytes and gain indentation. It runs beside the tree parse,
// never in a build pass.
func formatJSONBody(body string, tree jsonTreeResult) (string, bool) {
	if len(tree.roots) == 0 {
		return body, false
	}
	options := jsonc.FormattingOptions{TabSize: 2, InsertSpaces: true, EOL: jsonc.GetEOL(jsonc.FormattingOptions{}, body)}
	if tree.ndjson {
		return formatNDJSON(body, options), true
	}
	formatted, err := jsonc.ApplyEdits(body, jsonc.Format(body, nil, options))
	if err != nil {
		return body, true
	}
	return formatted, true
}

// formatNDJSON formats each record on its own. A single formatter pass over
// the stream stops strengthening the indentation after its first top-level
// value, so separate passes give every line the same treatment while keeping
// the original line breaks and line endings.
func formatNDJSON(body string, options jsonc.FormattingOptions) string {
	var out strings.Builder
	out.Grow(len(body))
	for len(body) > 0 {
		part, after, more := strings.Cut(body, "\n")
		body = after
		text, crlf := strings.CutSuffix(part, "\r")
		if strings.TrimSpace(text) != "" {
			if formatted, err := jsonc.ApplyEdits(text, jsonc.Format(text, nil, options)); err == nil {
				text = formatted
			}
		}
		out.WriteString(text)
		if crlf {
			out.WriteByte('\r')
		}
		if more {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (a *app) prettyBody(c *ui.Context, crumbs *jsonBreadcrumbs, key, body string, side int, json *jsonReader) {
	if json != nil && json.ready && json.result.tree.stop != "" {
		t := c.Theme()
		uikit.Meta(c, json.result.tree.stop).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM))
	}
	view := a.bodyText[side]
	if view == nil {
		text, isJSON := prettyBodyText(body, json)
		buf := editor.NewBuffer(text)
		if isJSON {
			buf.SetLanguage(editor.LanguageNamed("json"))
		}
		view = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.BodyText(),
			RenderFindBar: func(c *ui.Context, bar ui.Element, find *editor.FindBar) { a.bodyFindBar(c, bar, find, side) }}
		a.bodyText[side] = view
	}
	view.Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	view.SoftWrap = !a.bodyNoWrap[side]
	view.Theme = uikit.EditorThemeFor(c.Theme(), view.Focused())
	var provided jsonCrumbSource
	if json != nil && json.ready && json.result.crumbRoot != nil {
		provided = jsonCrumbSource{root: json.result.crumbRoot, text: json.result.crumbText}
	}
	a.bodyMenu(c, view, crumbs, key, provided)
	a.jsonPathBar(c, crumbs, key, view, func() { a.bodyReader[side].Focus(c) }, provided)
	a.outlineEditor(c, view, outlineTarget{key: key, kind: outlineJSON,
		crumbs: crumbs, crumb: provided, focus: func() { a.bodyReader[side].Focus(c) }}, func() {
		editor.View(c, view).Bind(&a.bodyReader[side]).FillWidth().Grow(1)
	})
}

// Keep the library's search semantics, with controls that fit a side inspector.
func (a *app) bodyFindBar(c *ui.Context, bar ui.Element, find *editor.FindBar, side int) {
	state := &a.bodyFind[side]
	bar.Bind(&state.handle)
	state.find = find
	t := c.Theme()
	bar.Row().Gap(t.Space(uikit.SpaceS)).Padding(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Background(uikit.Colors(t).Window).Border(1, t.Border).Radius(uikit.RadiusControl)
	bar.Children(func() {
		// The query and its three options share one field, as in VS Code.
		queryField, query := uikit.FindField(c, find.Query(), locales.SearchBody(), locales.FindInBody(), findToggles(state)...)
		queryField.Width(uikit.WidthWide).Shrink(1)
		bindFindOptionKeys(query, state)
		find.QueryInput(query)
		uikit.Meta(c, find.Status()).FontFeatures("tnum").MinWidth(40).Role(ui.RoleStatus)
		if uikit.Tip(c, uikit.IconButton(c, locales.PreviousBodyMatch(), locales.PreviousMatchShiftEnter(), lucide.ArrowUp()), locales.PreviousMatchShiftEnter()).KeepFocus().Clicked() {
			find.Previous()
		}
		if uikit.Tip(c, uikit.IconButton(c, locales.NextBodyMatch(), locales.NextMatchEnter(), lucide.ArrowDown()), locales.NextMatchEnter()).KeepFocus().Clicked() {
			find.Next()
		}
		if uikit.Tip(c, uikit.IconButton(c, locales.CloseBodySearch(), locales.CloseSearchEsc(), lucide.X()), locales.CloseSearchEsc()).KeepFocus().Clicked() {
			find.Close()
		}
		if err := find.Err(); err != nil {
			uikit.ErrorText(c, err.Error()).Shrink(1)
		}
	})
}

// codeBody shows JavaScript or CSS re-indented, keeping the captured bytes
// for the other formats and exports.
func (a *app) codeBody(c *ui.Context, f flow, body string, side int, kind string) {
	key := fmt.Sprintf("%d:%d:%s", f.id, side, kind)
	if a.codeFor[side] != key {
		a.codeFor[side] = key
		text, language := inspect.BeautifyJS(body), "javascript"
		if kind == "CSS" {
			text, language = inspect.BeautifyCSS(body), "css"
		}
		buf := editor.NewBuffer(text)
		buf.SetLanguage(editor.LanguageNamed(language))
		a.formattedCode[side] = &editor.State{Buffer: buf, ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.FormattedBody()}
	}
	a.formattedCode[side].Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	a.formattedCode[side].Theme = uikit.EditorThemeFor(c.Theme(), a.formattedCode[side].Focused())
	editor.View(c, a.formattedCode[side]).Grow(1)
}

// markdownBody renders a Markdown body as rich text.
func (a *app) markdownBody(c *ui.Context, f flow, body string, side int) {
	key := fmt.Sprintf("%d:%d", f.id, side)
	if a.markdownFor[side] != key {
		a.markdownFor[side] = key
		a.markdownDoc[side] = richtext.NewDocument(richtext.ParseMarkdown(body))
	}
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).Children(func() {
		richtext.EditorBase(c, a.markdownDoc[side], richtext.EditorOptions{ReadOnly: true,
			OnLink: func(raw string) { a.openCapturedLink(c, raw) }}).Grow(1)
	})
}

// mergedBody joins an SSE or OpenAI-style chunk stream into one message.
func (a *app) mergedBody(c *ui.Context, f flow, body string, side int) {
	key := fmt.Sprintf("%d:%d", f.id, side)
	if a.mergedFor[side] != key {
		a.mergedFor[side] = key
		text, ok := inspect.MergeStream(f.bodyContentType(side == 0), body)
		if !ok {
			a.mergedText[side] = nil
		} else {
			a.mergedText[side] = &editor.State{Buffer: editor.NewBuffer(text), ReadOnly: true, NoScrollPastEnd: true, SoftWrap: true, Label: locales.MergedStream()}
		}
	}
	if a.mergedText[side] == nil {
		uikit.Hint(c, locales.NoMergeableStream()).Padding(c.Theme().Space(uikit.SpaceXL))
		return
	}
	a.mergedText[side].Font = ui.Font{Family: uikit.FontMono, Size: uikit.TextBody}
	a.mergedText[side].Theme = uikit.EditorThemeFor(c.Theme(), a.mergedText[side].Focused())
	editor.View(c, a.mergedText[side]).Grow(1)
}

func (a *app) jsonBody(c *ui.Context, side int) {
	t := c.Theme()
	theme := uikit.EditorTheme(t)
	if stop := a.jsonTrees[side].stop; stop != "" {
		uikit.Meta(c, stop).Padding(0, t.Space(uikit.SpaceL), t.Space(uikit.SpaceM))
	}
	var rows []jsonRow
	if a.bodyQuery[side] != "" {
		rows = matchingJSON(a.jsonTrees[side].roots, a.bodyQuery[side])
	} else {
		rows = a.visibleJSON(side)
	}
	if len(rows) == 0 {
		uikit.Hint(c, locales.NoMatches()).Padding(t.Space(uikit.SpaceXL))
		return
	}
	if a.paneWidth(c) < 620 {
		ui.Column(c).Grow(1).Children(func() {
			ui.Column(c).Grow(1).MinHeight(80).Children(func() { a.jsonTree(c, side, rows, theme, t) })
			ui.Box(c).Height(200).FillWidth().Children(func() { a.jsonQueryPanel(c, side) })
		})
		return
	}
	ui.Row(c).Grow(1).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.jsonTree(c, side, rows, theme, t)
		})
		a.jsonQueryPanel(c, side)
	})
}

// jsonTree draws the visible JSON rows, using the same editor token colors
// that Pretty gives the same values: strings in the string ink, numbers,
// booleans and null in the constant ink.
func (a *app) jsonTree(c *ui.Context, side int, rows []jsonRow, theme *editor.Theme, t *ui.Theme) {
	keyColor := theme.Tokens[editor.TokenProperty].Color
	stringColor := theme.Tokens[editor.TokenString].Color
	constantColor := theme.Tokens[editor.TokenConstant].Color
	ui.List(c, &a.jsonScroll[side], len(rows), func(i int) {
		row := rows[i]
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).Padding(0, t.Space(uikit.SpaceL)).MinHeight(uikit.HeightRow).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(float32(row.depth) * 16)
			if len(row.node.children) > 0 && a.bodyQuery[side] == "" {
				expanded := a.jsonExpanded[side][row.node.id]
				arrow, action := "▸", locales.ExpandAction()
				if expanded {
					arrow, action = "▾", locales.CollapseAction()
				}
				label := locales.ToggleJSON(action, []string{locales.SideResponse(), locales.SideRequest()}[side], nodePath(row.node))
				if uikit.Tip(c, uikit.SecondaryButton(c, arrow).Label(label), label).Expanded(expanded).Size(uikit.HeightRow, uikit.HeightRow).Padding(0).Clicked() {
					a.jsonExpanded[side][row.node.id] = !expanded
				}
			} else {
				ui.Box(c).Width(uikit.HeightRow)
			}
			if row.node.key != "" {
				name := row.node.key
				if a.jsonTrees[side].ndjson && row.depth == 0 {
					if line, err := strconv.Atoi(name); err == nil {
						name = locales.JsonLineRecord(line)
					}
				}
				uikit.Tip(c, uikit.DataBody(c, name).TextColor(keyColor).SingleLine(), nodePath(row.node))
				uikit.Hint(c, ":")
			}
			switch row.node.kind {
			case "object":
				uikit.Hint(c, fmt.Sprintf("{%d}", len(row.node.children))).FontFeatures("tnum")
			case "array":
				uikit.Hint(c, fmt.Sprintf("[%d]", len(row.node.children))).FontFeatures("tnum")
			default:
				valueColor := constantColor
				if row.node.kind == "string" {
					valueColor = stringColor
				}
				uikit.DataBody(c, row.node.value).TextColor(valueColor).SingleLine()
			}
		})
	}).Grow(1)
}

func (a *app) hexBody(c *ui.Context, body string, side int) {
	t := c.Theme()
	rows := hexRows(body, a.bodyQuery[side])
	if len(rows) == 0 {
		uikit.Hint(c, locales.NoMatches()).Padding(t.Space(uikit.SpaceXL))
		return
	}
	ui.List(c, &a.hexScroll[side], len(rows), func(i int) {
		uikit.DataMeta(c, hexLine(body, rows[i])).TextColor(t.Text).
			Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceL)).SingleLine()
	}).Grow(1)
}
