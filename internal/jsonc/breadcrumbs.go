// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file builds on VS Code's fault-tolerant JSON tree parser, ported in
// json_tree.go, to describe the node under a text position.

package jsonc

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// BreadcrumbSiblingLimit bounds a segment's dropdown. A body with thousands
// of keys still gets a bar: the menu lists the first ones and says that more
// exist.
const BreadcrumbSiblingLimit = 60

// BreadcrumbSibling is one place a dropdown can jump to. Start and End are
// byte offsets of the key and value together, for a property, or of the
// element, for an array index.
type BreadcrumbSibling struct {
	Label      string
	Start, End int
	Segment    Segment
}

// BreadcrumbSegment is one step of the path, from the document root to the
// node at the caret. The root segment is "$" and has no siblings.
type BreadcrumbSegment struct {
	Label      string
	Segment    Segment
	Start, End int
	Siblings   []BreadcrumbSibling
	More       bool // the body has more siblings than the menu lists
}

// BreadcrumbPath is the path at a position and the JSONPath naming the same
// node. An empty path means the text is not an object or an array.
type BreadcrumbPath struct {
	Segments []BreadcrumbSegment
	JSONPath string
}

// BreadcrumbsAt parses text and reports the container path at offset. It is
// the convenience form for callers with no tree; callers that already parsed
// the shown text should use BreadcrumbsIn so a caret move costs no parse.
func BreadcrumbsAt(text string, offset int) BreadcrumbPath {
	head := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(head, "{") && !strings.HasPrefix(head, "[") {
		return BreadcrumbPath{}
	}
	root, _ := ParseTree(text)
	if root == nil {
		return BreadcrumbPath{}
	}
	return BreadcrumbsIn(root, text, offset)
}

// BreadcrumbsIn reports the container path at offset using root, which must
// have been parsed from text. Bytes, not runes, are the unit: editor carets
// and nodes use the same offsets. A partial document is enough; the tree
// keeps unclosed containers and keys whose value is missing.
func BreadcrumbsIn(root *Node, text string, offset int) BreadcrumbPath {
	if root == nil {
		return BreadcrumbPath{}
	}
	head := strings.TrimLeft(text, " \t\r\n")
	if !strings.HasPrefix(head, "{") && !strings.HasPrefix(head, "[") {
		return BreadcrumbPath{}
	}
	offset = min(max(offset, 0), len(text))
	target := breadcrumbNodeAt(root, offset, len(text))
	if target == nil {
		target = root
	}
	// A caret in a key's quotes is the property, not the key's string value.
	// Both sides of the colon then name the same document symbol.
	if target.Parent != nil && target.Parent.Type == NodeTypeProperty &&
		len(target.Parent.Children) > 0 && target.Parent.Children[0] == target {
		target = target.Parent
	}
	path := BreadcrumbPath{JSONPath: "$"}
	path.Segments = append(path.Segments, BreadcrumbSegment{Label: "$", Start: 0, End: 0})

	type step struct {
		node   *Node
		parent *Node
		seg    Segment
		label  string
	}
	var steps []step
	for n := target; n != nil && n.Parent != nil; n = n.Parent {
		parent := n.Parent
		switch {
		case n.Type == NodeTypeProperty:
			key := breadcrumbKey(n)
			steps = append(steps, step{node: n, parent: parent, seg: KeySegment(key), label: key})
		case parent.Type == NodeTypeArray:
			index := slices.Index(parent.Children, n)
			if index >= 0 {
				steps = append(steps, step{node: n, parent: parent, seg: IndexSegment(index), label: "[" + strconv.Itoa(index) + "]"})
			}
		}
	}
	slices.Reverse(steps)
	for _, s := range steps {
		start, end := breadcrumbSpan(s.node, len(text))
		crumb := BreadcrumbSegment{Label: s.label, Segment: s.seg, Start: start, End: end}
		crumb.Siblings, crumb.More = breadcrumbSiblings(s.parent, s.seg, len(text))
		path.Segments = append(path.Segments, crumb)
		if s.seg.IsIndex {
			path.JSONPath += "[" + strconv.Itoa(s.seg.Index) + "]"
		} else if jsonPathIdentifier(s.seg.Key) {
			path.JSONPath += "." + s.seg.Key
		} else {
			path.JSONPath += jsonPathBracket(s.seg.Key)
		}
	}
	return path
}

// breadcrumbNodeAt descends to the innermost node containing offset, treating
// an unterminated node as reaching the end of the text. ParseTree gives nodes
// that FindNodeAtOffset skips when their Length is still -1, so this is the
// same search with that case clamped.
func breadcrumbNodeAt(root *Node, offset, size int) *Node {
	cur := root
	for cur != nil {
		var next *Node
		for _, child := range cur.Children {
			if child.Offset <= offset && breadcrumbContains(child, offset, size) {
				next = child
				break
			}
		}
		if next == nil {
			return cur
		}
		cur = next
	}
	return nil
}

func breadcrumbContains(node *Node, offset, size int) bool {
	if node == nil {
		return false
	}
	_, end := breadcrumbSpan(node, size)
	return offset <= end
}

// breadcrumbSpan clamps a node's byte span to the text. A parser node with no
// end yet reaches the bytes that follow it.
func breadcrumbSpan(node *Node, size int) (int, int) {
	if node == nil {
		return 0, 0
	}
	start := min(max(node.Offset, 0), size)
	end := size
	if node.Length >= 0 {
		end = min(max(node.Offset+node.Length, start), size)
	}
	return start, end
}

func breadcrumbKey(property *Node) string {
	if len(property.Children) == 0 {
		return ""
	}
	key, _ := property.Children[0].Value.(string)
	return key
}

// breadcrumbSiblings lists a segment's peers, keeping their jump spans and
// stopping at BreadcrumbSiblingLimit so one oversized body cannot fill a menu.
func breadcrumbSiblings(parent *Node, seg Segment, size int) ([]BreadcrumbSibling, bool) {
	if parent == nil {
		return nil, false
	}
	children := parent.Children
	out := make([]BreadcrumbSibling, 0, min(len(children), BreadcrumbSiblingLimit))
	for i, child := range children {
		if i >= BreadcrumbSiblingLimit {
			return out, true
		}
		sibling := BreadcrumbSibling{}
		switch parent.Type {
		case NodeTypeObject:
			if child.Type != NodeTypeProperty {
				continue
			}
			key := breadcrumbKey(child)
			sibling.Label, sibling.Segment = key, KeySegment(key)
		case NodeTypeArray:
			sibling.Label, sibling.Segment = "["+strconv.Itoa(i)+"]", IndexSegment(i)
		default:
			return out, false
		}
		sibling.Start, sibling.End = breadcrumbSpan(child, size)
		out = append(out, sibling)
	}
	return out, false
}

// jsonPathIdentifier follows the usual rule for an unquoted JSONPath member:
// a letter, "_" or "$" first, then letters, digits, "_" or "$".
func jsonPathIdentifier(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case unicode.IsLetter(r), r == '_', r == '$':
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}

// jsonPathBracket quotes a key that cannot stand alone after a dot, escaping
// the backslash, the quote and control characters.
func jsonPathBracket(key string) string {
	var b strings.Builder
	b.Grow(len(key) + 4)
	b.WriteString("['")
	for _, r := range key {
		switch r {
		case '\\', '\'':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(strconv.QuoteRune(r))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteString("']")
	return b.String()
}
