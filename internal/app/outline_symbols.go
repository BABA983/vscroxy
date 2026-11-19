package app

import (
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/graphql"
	"github.com/baba983/vscroxy/internal/jsonc"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// outlineChildLimit is how many children one row lists before the last row
// summarizes the rest. The symbols still keep every child: a filter can find
// one beyond the limit, as it does in VS Code's outline.
const outlineChildLimit = 60

type outlineKind uint8

const (
	outlineJSON outlineKind = iota
	outlineGraphQL
	outlineScript
)

type outlineIcon uint8

const (
	outlineIconValue outlineIcon = iota
	outlineIconBranch
	outlineIconOperation
	outlineIconFunction
	outlineIconTest
	outlineIconBlock
)

// outlineSymbol is one row of the outline. Start and End are byte offsets
// into the text the editor shows; clicking the row selects that range. The
// key is stable across rebuilds of the same tree, so the open rows and the
// chosen row survive an edit that keeps a symbol in place.
type outlineSymbol struct {
	key      string
	parent   string
	label    string
	hint     string
	start    int
	end      int
	icon     outlineIcon
	children []*outlineSymbol
	more     int
}

func (s *outlineSymbol) branch() bool { return s != nil && (len(s.children) > 0 || s.more > 0) }

// outlineNodeSpan clamps a parser node to the text the editor shows. A node
// still open at the end of a truncated body reaches the last byte.
func outlineNodeSpan(node *jsonc.Node, size int) (int, int) {
	start := min(max(node.Offset, 0), size)
	end := size
	if node.Length >= 0 {
		end = min(max(node.Offset+node.Length, start), size)
	}
	return start, end
}

func outlineNodeKey(property *jsonc.Node) string {
	if len(property.Children) == 0 {
		return ""
	}
	name, _ := property.Children[0].Value.(string)
	return name
}

// outlineParseInput is the immutable input for one symbol computation. The
// JSON tree is the breadcrumb cache's tree, not a second parse of the text.
type outlineParseInput struct {
	kind outlineKind
	text string
	tree *jsonc.Node
}

func parseOutlineSymbols(in outlineParseInput) []*outlineSymbol {
	switch in.kind {
	case outlineJSON:
		return outlineJSONSymbols(in.tree, in.text)
	case outlineGraphQL:
		return outlineGraphQLSymbols(in.text)
	default:
		return outlineScriptSymbols(in.text)
	}
}

func assignOutlineKeys(roots []*outlineSymbol) {
	var walk func([]*outlineSymbol, string)
	walk = func(nodes []*outlineSymbol, parent string) {
		for i, node := range nodes {
			node.key = parent + strconv.Itoa(i)
			node.parent = strings.TrimSuffix(parent, "/")
			walk(node.children, node.key+"/")
		}
	}
	walk(roots, "")
}

// outlineJSONSymbols turns the tree the JSON breadcrumbs already parsed into
// outline rows. Container properties expand into their value's children; an
// array's children are its indices.
func outlineJSONSymbols(root *jsonc.Node, text string) []*outlineSymbol {
	if root == nil {
		return nil
	}
	if root.Type == jsonc.NodeTypeObject || root.Type == jsonc.NodeTypeArray {
		roots := outlineJSONChildren(root, text)
		assignOutlineKeys(roots)
		return roots
	}
	start, end := outlineNodeSpan(root, len(text))
	if end <= start {
		return nil
	}
	node := &outlineSymbol{label: "$", hint: outlineJSONHint(root, text), start: start, end: end}
	node.children = outlineJSONChildren(root, text)
	assignOutlineKeys([]*outlineSymbol{node})
	return []*outlineSymbol{node}
}

func outlineJSONChildren(container *jsonc.Node, text string) []*outlineSymbol {
	var out []*outlineSymbol
	for i, child := range container.Children {
		if child == nil {
			continue
		}
		value := child
		label := "[" + strconv.Itoa(i) + "]"
		if container.Type == jsonc.NodeTypeObject {
			if child.Type != jsonc.NodeTypeProperty {
				continue
			}
			label = outlineNodeKey(child)
			if len(child.Children) > 1 {
				value = child.Children[1]
			} else {
				value = nil
			}
		}
		start, end := outlineNodeSpan(child, len(text))
		if end <= start {
			continue
		}
		icon := outlineIconValue
		node := &outlineSymbol{label: label, hint: outlineJSONHint(value, text), start: start, end: end, icon: icon}
		if value != nil {
			node.children = outlineJSONChildren(value, text)
			if value.Type == jsonc.NodeTypeObject || value.Type == jsonc.NodeTypeArray {
				node.icon = outlineIconBranch
			}
		}
		out = append(out, node)
	}
	return out
}

// outlineJSONHint keeps the hint short enough for a narrow side section:
// container sizes, a clipped scalar, or a word for a value the parser never
// finished. A source slice is always clamped to the text's bounds.
func outlineJSONHint(value *jsonc.Node, text string) string {
	if value == nil {
		return locales.OutlineIncomplete()
	}
	switch value.Type {
	case jsonc.NodeTypeObject:
		return "{" + strconv.Itoa(len(value.Children)) + "}"
	case jsonc.NodeTypeArray:
		return "[" + strconv.Itoa(len(value.Children)) + "]"
	case jsonc.NodeTypeString:
		name, _ := value.Value.(string)
		return outlineClip(strconv.Quote(name), 26)
	case jsonc.NodeTypeNumber, jsonc.NodeTypeBoolean, jsonc.NodeTypeNull:
		start, end := outlineNodeSpan(value, len(text))
		if end > start && start >= 0 && end <= len(text) {
			return outlineClip(strings.TrimSpace(text[start:end]), 26)
		}
	}
	return locales.OutlineIncomplete()
}

func outlineClip(s string, limit int) string {
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

// outlineGraphQLSymbols maps the graphql package's AST symbols onto the
// outline's tree. The parser keeps what it could read from a half-typed query.
func outlineGraphQLSymbols(source string) []*outlineSymbol {
	parsed := graphql.OutlineSymbols(source)
	if len(parsed) == 0 {
		return nil
	}
	nodes := make([]*outlineSymbol, len(parsed))
	for i, sym := range parsed {
		nodes[i] = &outlineSymbol{label: sym.Label, start: sym.Start, end: sym.End}
		switch sym.Kind {
		case graphql.SymbolOperation:
			nodes[i].hint = sym.Type
			nodes[i].icon = outlineIconOperation
		case graphql.SymbolFragment:
			nodes[i].hint = sym.Type
			nodes[i].icon = outlineIconBranch
		default:
			nodes[i].icon = outlineIconValue
		}
	}
	var roots []*outlineSymbol
	for i, sym := range parsed {
		if sym.Parent >= 0 && sym.Parent < len(nodes) {
			nodes[sym.Parent].children = append(nodes[sym.Parent].children, nodes[i])
		} else {
			roots = append(roots, nodes[i])
		}
	}
	assignOutlineKeys(roots)
	return roots
}

// outlineScriptRecord is the flat result of the tolerant script scan. The
// scanner knows a record's span and its parent, so nested describe/it blocks
// and tests inside them become a tree without a JavaScript engine.
type outlineScriptRecord struct {
	label, hint string
	start, end  int
	parent      int
	icon        outlineIcon
}

func outlineScriptSymbols(source string) []*outlineSymbol {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	var records []outlineScriptRecord
	var stack []int
	add := func(label, hint string, start, end int, icon outlineIcon) {
		start, end = min(max(start, 0), len(source)), min(max(end, 0), len(source))
		if start < 0 || end <= start || label == "" {
			return
		}
		for len(stack) > 0 && start >= records[stack[len(stack)-1]].end {
			stack = stack[:len(stack)-1]
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		index := len(records)
		records = append(records, outlineScriptRecord{label: label, hint: hint, start: start, end: end, parent: parent, icon: icon})
		stack = append(stack, index)
	}
	// A `const f = function () {}` is found once at its function keyword;
	// remember the spans so the variable scan cannot add it as an arrow too.
	var claimed []int
	insideClaimed := func(at int) bool {
		for _, end := range claimed {
			if at < end {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(source); {
		j := scriptTrivia(source, i)
		if j != i {
			i = j
			continue
		}
		if !scriptIdentStart(source[i]) {
			i++
			continue
		}
		start := i
		for i < len(source) && scriptIdentPart(source[i]) {
			i++
		}
		word := source[start:i]
		switch word {
		case "function":
			if _, name, end, ok := scriptFunction(source, i); ok {
				// An unnamed function passed to describe/it/pm.test is part
				// of that call's row, not a separate top-level symbol.
				if name == "" && len(stack) > 0 {
					continue
				}
				label := name
				if label == "" {
					label = locales.OutlineAnonymousFunction()
				}
				add(label, "function", start, end, outlineIconFunction)
				claimed = append(claimed, end)
			}
		case "pm":
			if name, end, ok := scriptPMTest(source, i); ok {
				label := locales.OutlineTest()
				if name != "" {
					label += ": " + outlineClip(name, 48)
				}
				add(label, "pm.test", start, end, outlineIconTest)
			}
		case "describe", "it":
			if name, end, ok := scriptBlockCall(source, i); ok {
				label := name
				if label == "" {
					label = word + "()"
				}
				add(label, word, start, end, outlineIconBlock)
			}
		case "const", "let", "var":
			if name, end, ok := scriptArrow(source, start, i); ok && !insideClaimed(start) {
				add(name, "function", start, end, outlineIconFunction)
			}
		}
	}
	if len(records) == 0 {
		return nil
	}
	nodes := make([]*outlineSymbol, len(records))
	var roots []*outlineSymbol
	for i, record := range records {
		nodes[i] = &outlineSymbol{label: record.label, hint: record.hint, start: record.start,
			end: record.end, icon: record.icon}
		if record.parent >= 0 {
			nodes[record.parent].children = append(nodes[record.parent].children, nodes[i])
		} else {
			roots = append(roots, nodes[i])
		}
	}
	assignOutlineKeys(roots)
	return roots
}

func scriptIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= utf8RuneSelf
}

func scriptIdentPart(c byte) bool {
	return scriptIdentStart(c) || c >= '0' && c <= '9'
}

// utf8RuneSelf keeps multi-byte identifiers (and a clipboard's {{templates}})
// from being split into pseudo-words by the byte scanner.
const utf8RuneSelf = 0x80

// scriptTrivia steps over whitespace and comments. An unterminated block
// comment reaches the end of the source instead of looping.
func scriptTrivia(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n':
			i++
		case i+1 < len(s) && s[i] == '/' && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case i+1 < len(s) && s[i] == '/' && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i = min(i+2, len(s))
		default:
			return i
		}
	}
	return i
}

// scriptQuoted reads a JS string or template without interpreting it. It
// returns only the inner text and the offset just after the closing quote.
func scriptQuoted(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	quote := s[i]
	if quote != '"' && quote != '\'' && quote != '`' {
		return "", i, false
	}
	i++
	start := i
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == quote {
			value := s[start:i]
			value = strings.ReplaceAll(value, `\"`, `"`)
			value = strings.ReplaceAll(value, `\'`, `'`)
			value = strings.ReplaceAll(value, `\n`, "\n")
			return value, i + 1, true
		}
		i++
	}
	return s[start:len(s)], len(s), true
}

// scriptMatching finds the closing delimiter, skipping strings and comments
// so braces in quotes never change the nesting.
func scriptMatching(s string, at int, open, close byte) int {
	if at >= len(s) || s[at] != open {
		return len(s)
	}
	depth := 0
	for i := at; i < len(s); {
		switch s[i] {
		case '"', '\'', '`':
			_, after, _ := scriptQuoted(s, i)
			i = after
		case '/':
			next := scriptTrivia(s, i)
			if next == i {
				i++
			} else {
				i = next
			}
		default:
			if s[i] == open {
				depth++
			} else if s[i] == close {
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
	}
	return len(s)
}

func scriptCall(s string, at int) (open, close int, ok bool) {
	open = scriptTrivia(s, at)
	if open >= len(s) || s[open] != '(' {
		return 0, 0, false
	}
	close = scriptMatching(s, open, '(', ')')
	if close <= open {
		return 0, 0, false
	}
	return open, close, true
}

func scriptFirstArg(s string, open, close int) string {
	at := scriptTrivia(s, open+1)
	if at >= close || at >= len(s) {
		return ""
	}
	if s[at] == '"' || s[at] == '\'' || s[at] == '`' {
		value, after, ok := scriptQuoted(s, at)
		if !ok || after > close+1 {
			return ""
		}
		return value
	}
	return ""
}

func scriptFunction(s string, at int) (next int, name string, end int, ok bool) {
	at = scriptTrivia(s, at)
	if at < len(s) && s[at] == '*' {
		at = scriptTrivia(s, at+1)
	}
	if at < len(s) && scriptIdentStart(s[at]) {
		start := at
		for at < len(s) && scriptIdentPart(s[at]) {
			at++
		}
		name = s[start:at]
		at = scriptTrivia(s, at)
	}
	if at >= len(s) || s[at] != '(' {
		return at, "", 0, false
	}
	_, close, valid := scriptCall(s, at)
	if !valid {
		return at, "", 0, false
	}
	end = close
	if block := scriptTrivia(s, close); block < len(s) && s[block] == '{' {
		end = scriptMatching(s, block, '{', '}')
	}
	return at, name, end, true
}

func scriptPMTest(s string, at int) (name string, end int, ok bool) {
	at = scriptTrivia(s, at)
	if at >= len(s) || s[at] != '.' {
		return "", 0, false
	}
	at = scriptTrivia(s, at+1)
	if at+4 > len(s) || s[at:at+4] != "test" {
		return "", 0, false
	}
	open, close, valid := scriptCall(s, at+4)
	if !valid {
		return "", 0, false
	}
	return scriptFirstArg(s, open, close), close, true
}

func scriptBlockCall(s string, at int) (name string, end int, ok bool) {
	open, close, valid := scriptCall(s, at)
	if !valid {
		return "", 0, false
	}
	name = scriptFirstArg(s, open, close)
	end = close
	if block := scriptTrivia(s, close); block < len(s) && s[block] == '{' {
		end = scriptMatching(s, block, '{', '}')
	} else {
		// An arrow callback's body can start after `=>`, and a function
		// callback after its parameter list.
		for i := close; i < min(close+160, len(s)); i++ {
			if s[i] == '{' {
				end = scriptMatching(s, i, '{', '}')
				break
			}
			if s[i] == ';' || s[i] == '\n' {
				break
			}
		}
	}
	return name, end, true
}

// scriptArrow recognizes `const name = () => { ... }` at the same level as a
// function declaration. It stops at the first statement boundary so an arrow
// in a later line cannot be taken as this variable's value.
func scriptArrow(s string, start, at int) (name string, end int, ok bool) {
	at = scriptTrivia(s, at)
	if at >= len(s) || !scriptIdentStart(s[at]) {
		return "", 0, false
	}
	begin := at
	for at < len(s) && scriptIdentPart(s[at]) {
		at++
	}
	name = s[begin:at]
	for i := at; i < min(at+240, len(s)); i++ {
		switch {
		case s[i] == ';', s[i] == '\n':
			return "", 0, false
		case i+1 < len(s) && s[i] == '=' && s[i+1] == '>':
			block := scriptTrivia(s, i+2)
			end = block
			if block < len(s) && s[block] == '{' {
				end = scriptMatching(s, block, '{', '}')
			}
			return name, end, true
		case s[i] == '"', s[i] == '\'', s[i] == '`':
			_, after, _ := scriptQuoted(s, i)
			i = after
		}
	}
	return "", 0, false
}

func outlineIconSVG(kind outlineKind, sym *outlineSymbol) *ui.SVG {
	switch kind {
	case outlineJSON:
		if sym.branch() {
			return lucide.Braces()
		}
		return lucide.Hash()
	case outlineGraphQL:
		return lucide.GitBranch()
	default:
		switch sym.icon {
		case outlineIconTest:
			return lucide.Check()
		case outlineIconBlock:
			return lucide.Folder()
		case outlineIconFunction:
			return lucide.Wrench()
		}
		return lucide.FileCode()
	}
}
