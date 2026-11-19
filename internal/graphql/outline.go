package graphql

import (
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// SymbolKind names the kind of an executable document symbol.
type SymbolKind string

const (
	SymbolOperation SymbolKind = "operation"
	SymbolFragment  SymbolKind = "fragment"
	SymbolField     SymbolKind = "field"
)

// Symbol is one document symbol, in document order. Start and End are byte
// offsets into the source: a click can select the operation, fragment or field
// the row names. Parent is the index of the enclosing symbol, or -1, so a
// caller can build the outline's tree without parsing selection sets again.
type Symbol struct {
	Kind   SymbolKind
	Label  string
	Type   string // query, mutation or subscription, for operations
	Start  int
	End    int
	Parent int
}

// OutlineSymbols lists operations, fragments and the fields of their selection
// sets. It uses the same parser as the app's other GraphQL tools, including
// when they report an error: a document being typed leaves the operations and
// fields the parser could read. It never needs a schema and never panics on
// partial text.
func OutlineSymbols(source string) []Symbol {
	if source == "" || len(source) > maxSource {
		return nil
	}
	// Templates are not GraphQL, but they keep their rune count, so positions
	// from the masked source still name the same bytes of the original text.
	src := &ast.Source{Name: "query", Input: MaskTemplates(source)}
	doc, _ := parser.ParseQueryWithTokenLimit(src, maxTokens+1)
	if doc == nil {
		return nil
	}
	var out []Symbol
	type frame struct {
		index int
		end   int
	}
	var stack []frame
	at := func(pos *ast.Position) int {
		if pos == nil {
			return 0
		}
		return min(max(byteIndex(source, pos.Start), 0), len(source))
	}
	add := func(kind SymbolKind, label, typ string, pos *ast.Position, selection ast.SelectionSet) bool {
		start := at(pos)
		end := start
		if pos != nil {
			end = min(max(byteIndex(source, pos.End), start), len(source))
		}
		if len(selection) > 0 {
			if block := selectionEnd(source, start); block > end {
				end = block
			}
		}
		for len(stack) > 0 && start >= stack[len(stack)-1].end {
			stack = stack[:len(stack)-1]
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1].index
		}
		index := len(out)
		out = append(out, Symbol{Kind: kind, Label: label, Type: typ, Start: start, End: end, Parent: parent})
		if end > start {
			stack = append(stack, frame{index: index, end: end})
			return true
		}
		return false
	}
	var fields func(ast.SelectionSet)
	fields = func(set ast.SelectionSet) {
		for _, selection := range set {
			switch node := selection.(type) {
			case *ast.Field:
				if node == nil || node.Name == "" {
					continue
				}
				label := node.Name
				if node.Alias != "" && node.Alias != node.Name {
					label = node.Alias + ": " + node.Name
				}
				if add(SymbolField, label, "", node.GetPosition(), node.SelectionSet) {
					fields(node.SelectionSet)
				}
			case *ast.FragmentSpread:
				if node != nil && node.Name != "" {
					add(SymbolField, "..."+node.Name, "", node.GetPosition(), nil)
				}
			case *ast.InlineFragment:
				if node == nil {
					continue
				}
				label := "..."
				if node.TypeCondition != "" {
					label += " on " + node.TypeCondition
				}
				add(SymbolField, label, "", node.GetPosition(), node.SelectionSet)
				fields(node.SelectionSet)
			}
		}
	}
	for _, op := range doc.Operations {
		if op == nil {
			continue
		}
		label := string(op.Operation)
		if op.Name != "" {
			label += " " + op.Name
		}
		add(SymbolOperation, label, string(op.Operation), op.Position, op.SelectionSet)
		fields(op.SelectionSet)
	}
	for _, frag := range doc.Fragments {
		if frag == nil {
			continue
		}
		label := strings.TrimSpace("fragment " + frag.Name + " on " + frag.TypeCondition)
		add(SymbolFragment, label, frag.TypeCondition, frag.Position, frag.SelectionSet)
		fields(frag.SelectionSet)
	}
	return out
}

// selectionEnd finds the closing brace of a selection set after start. GraphQL
// strings and comments can contain braces; a truncated block reaches the end
// of the source. The parser's own lexer has already bounded the nesting.
func selectionEnd(source string, start int) int {
	depth, opened := 0, false
	for i := max(start, 0); i < len(source); {
		switch source[i] {
		case '#':
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case '"':
			triple := strings.HasPrefix(source[i:], `"""`)
			if triple {
				i += 3
			} else {
				i++
			}
			for i < len(source) {
				if source[i] == '\\' {
					i += 2
					continue
				}
				if triple && strings.HasPrefix(source[i:], `"""`) {
					i += 3
					break
				}
				if !triple && source[i] == '"' {
					i++
					break
				}
				i++
			}
		case '{':
			depth++
			opened = true
			i++
		case '}':
			i++
			if opened {
				depth--
				if depth == 0 {
					return i
				}
			}
		default:
			i++
		}
	}
	return len(source)
}
