// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file ports the tree, path and location helpers from VS Code's
// src/vs/base/common/json.ts.

package jsonc

import (
	"slices"
)

// NodeType is NodeType in VS Code's json.ts.
type NodeType string

const (
	// NodeTypeObject is the 'object' node type.
	NodeTypeObject NodeType = "object"
	// NodeTypeArray is the 'array' node type.
	NodeTypeArray NodeType = "array"
	// NodeTypeProperty is the 'property' node type.
	NodeTypeProperty NodeType = "property"
	// NodeTypeString is the 'string' node type.
	NodeTypeString NodeType = "string"
	// NodeTypeNumber is the 'number' node type.
	NodeTypeNumber NodeType = "number"
	// NodeTypeBoolean is the 'boolean' node type.
	NodeTypeBoolean NodeType = "boolean"
	// NodeTypeNull is the 'null' node type.
	NodeTypeNull NodeType = "null"
)

// Node is Node in VS Code's json.ts. Offset and Length are byte offsets.
// Parent is nil for the root of a returned tree. ColonOffset is -1 when the
// node is not a property with a colon. Children[0] of a property is its key
// string, Children[1] its value. Value holds decoded JSON values only.
type Node struct {
	Type        NodeType
	Value       any
	Offset      int
	Length      int
	ColonOffset int
	Parent      *Node
	Children    []*Node
}

// Segment is one Segment of a JSONPath: either a property key or an array
// index. It models VS Code's string | number union as a typed struct.
type Segment struct {
	Key     string
	Index   int
	IsIndex bool
}

// JSONPath is JSONPath in VS Code's json.ts: a sequence of property keys and
// array indices.
type JSONPath []Segment

// KeySegment returns a Segment for an object property key.
func KeySegment(key string) Segment { return Segment{Key: key} }

// IndexSegment returns a Segment for an array index.
func IndexSegment(index int) Segment { return Segment{Index: index, IsIndex: true} }

// Location is Location in VS Code's json.ts. Offsets are byte offsets.
type Location struct {
	PreviousNode    *Node
	Path            JSONPath
	IsAtPropertyKey bool
}

// Matches is Location.matches in VS Code's json.ts. '*' matches one segment;
// '**' skips any number of segments, exactly as in VS Code.
func (l Location) Matches(pattern JSONPath) bool {
	k := 0
	for i := 0; k < len(pattern) && i < len(l.Path); i++ {
		want := pattern[k]
		if want.IsIndex {
			if segmentsEqual(want, l.Path[i]) {
				k++
			} else {
				return false
			}
			continue
		}
		switch want.Key {
		case "*":
			k++
		case "**":
		default:
			if segmentsEqual(want, l.Path[i]) {
				k++
			} else {
				return false
			}
		}
	}
	return k == len(pattern)
}

func segmentsEqual(a, b Segment) bool {
	if a.IsIndex != b.IsIndex {
		return false
	}
	if a.IsIndex {
		return a.Index == b.Index
	}
	return a.Key == b.Key
}

// GetNodeType is getNodeType in VS Code's json.ts.
func GetNodeType(value any) NodeType {
	switch value.(type) {
	case bool:
		return NodeTypeBoolean
	case float64, float32, int, int64, uint64:
		return NodeTypeNumber
	case string:
		return NodeTypeString
	case nil:
		return NodeTypeNull
	case []any, []*Node:
		return NodeTypeArray
	case map[string]any:
		return NodeTypeObject
	default:
		return NodeTypeNull
	}
}

// ParseTree is parseTree in VS Code's json.ts. It returns a partial tree and
// the parse errors; unlike the JS version it returns the errors instead of
// appending to a caller-owned slice. Broken input still produces the nodes
// that could be read, including unclosed objects and arrays.
func ParseTree(text string, options ...ParseOptions) (*Node, []ParseError) {
	opts := parseOptions(options)
	root := &Node{Type: NodeTypeArray, Offset: -1, Length: -1, ColonOffset: -1}
	current := root

	onValue := func(node *Node) *Node {
		node.Parent = current
		current.Children = append(current.Children, node)
		return node
	}
	ensurePropertyComplete := func(endOffset int) {
		if current.Type == NodeTypeProperty {
			current.Length = endOffset - current.Offset
			current = current.Parent
		}
	}
	errs := make([]ParseError, 0)
	hooks := visitorHooks{
		onObjectBegin: func(offset, _ int) bool {
			current = onValue(&Node{Type: NodeTypeObject, Offset: offset, Length: -1, ColonOffset: -1})
			return true
		},
		onObjectProperty: func(name string, offset, length int) bool {
			current = onValue(&Node{Type: NodeTypeProperty, Offset: offset, Length: -1, ColonOffset: -1})
			current.Children = append(current.Children, &Node{
				Type: NodeTypeString, Value: name, Offset: offset, Length: length, ColonOffset: -1, Parent: current,
			})
			return true
		},
		onObjectEnd: func(offset, length int) bool {
			end := offset + length
			current.Length = end - current.Offset
			current = current.Parent
			ensurePropertyComplete(end)
			return true
		},
		onArrayBegin: func(offset, _ int) bool {
			current = onValue(&Node{Type: NodeTypeArray, Offset: offset, Length: -1, ColonOffset: -1})
			return true
		},
		onArrayEnd: func(offset, length int) bool {
			end := offset + length
			current.Length = end - current.Offset
			current = current.Parent
			ensurePropertyComplete(end)
			return true
		},
		onLiteralValue: func(value any, offset, length int) bool {
			onValue(&Node{Type: GetNodeType(value), Value: value, Offset: offset, Length: length, ColonOffset: -1})
			ensurePropertyComplete(offset + length)
			return true
		},
		onSeparator: func(separator string, offset, _ int) bool {
			if current.Type == NodeTypeProperty {
				switch separator {
				case ":":
					current.ColonOffset = offset
				case ",":
					ensurePropertyComplete(offset)
				}
			}
			return true
		},
		onError: func(code ParseErrorCode, offset, length int) bool {
			errs = append(errs, ParseError{Error: code, Offset: offset, Length: length})
			return true
		},
	}
	newParser(text, hooks, opts).run()
	if len(root.Children) == 0 {
		return nil, errs
	}
	result := root.Children[0]
	result.Parent = nil
	return result, errs
}

// FindNodeAtLocation is findNodeAtLocation in VS Code's json.ts.
func FindNodeAtLocation(root *Node, path JSONPath) *Node {
	if root == nil {
		return nil
	}
	node := root
	for _, segment := range path {
		if node == nil {
			return nil
		}
		if segment.IsIndex {
			if node.Type != NodeTypeArray || segment.Index < 0 || segment.Index >= len(node.Children) {
				return nil
			}
			node = node.Children[segment.Index]
			continue
		}
		if node.Type != NodeTypeObject {
			return nil
		}
		found := false
		for _, property := range node.Children {
			if len(property.Children) == 0 {
				continue
			}
			key, ok := property.Children[0].Value.(string)
			if !ok || key != segment.Key {
				continue
			}
			if len(property.Children) < 2 {
				return nil
			}
			node = property.Children[1]
			found = true
			break
		}
		if !found {
			return nil
		}
	}
	return node
}

// GetNodePath is getNodePath in VS Code's json.ts.
func GetNodePath(node *Node) JSONPath {
	if node == nil || node.Parent == nil || len(node.Parent.Children) == 0 {
		return nil
	}
	path := GetNodePath(node.Parent)
	switch node.Parent.Type {
	case NodeTypeProperty:
		if len(node.Parent.Children) > 0 {
			if key, ok := node.Parent.Children[0].Value.(string); ok {
				path = append(path, KeySegment(key))
			}
		}
	case NodeTypeArray:
		if index := slices.Index(node.Parent.Children, node); index != -1 {
			path = append(path, IndexSegment(index))
		}
	}
	return path
}

// GetNodeValue is getNodeValue in VS Code's json.ts. Objects become
// map[string]any rather than JS null-prototype objects.
func GetNodeValue(node *Node) any {
	if node == nil {
		return nil
	}
	switch node.Type {
	case NodeTypeArray, NodeTypeObject:
		if node.Type == NodeTypeArray {
			result := make([]any, 0, len(node.Children))
			for _, child := range node.Children {
				result = append(result, GetNodeValue(child))
			}
			return result
		}
		result := make(map[string]any, len(node.Children))
		for _, property := range node.Children {
			if len(property.Children) < 2 {
				continue
			}
			if key, ok := property.Children[0].Value.(string); ok {
				result[key] = GetNodeValue(property.Children[1])
			}
		}
		return result
	case NodeTypeNull, NodeTypeString, NodeTypeNumber, NodeTypeBoolean:
		return node.Value
	default:
		return nil
	}
}

// Contains is contains in VS Code's json.ts. With includeRightBound, an offset
// exactly at the end of the node also counts as inside it.
func Contains(node *Node, offset int, includeRightBound bool) bool {
	if node == nil {
		return false
	}
	return offset >= node.Offset && offset < node.Offset+node.Length ||
		includeRightBound && offset == node.Offset+node.Length
}

// FindNodeAtOffset is findNodeAtOffset in VS Code's json.ts: the innermost
// node containing the byte offset.
func FindNodeAtOffset(node *Node, offset int, includeRightBound bool) *Node {
	if !Contains(node, offset, includeRightBound) {
		return nil
	}
	for _, child := range node.Children {
		if child.Offset > offset {
			continue
		}
		if found := FindNodeAtOffset(child, offset, includeRightBound); found != nil {
			return found
		}
	}
	return node
}

// GetLocation is getLocation in VS Code's json.ts: it reports the path at the
// byte position and the nearest preceding property key or literal value.
func GetLocation(text string, position int) Location {
	segments := make(JSONPath, 0)
	var previousNode *Node
	setPreviousNode := func(value any, offset, length int, nodeType NodeType) {
		previousNode = &Node{Value: value, Offset: offset, Length: length, Type: nodeType, ColonOffset: -1}
	}
	isAtPropertyKey := false
	hooks := visitorHooks{
		onObjectBegin: func(offset, _ int) bool {
			if position <= offset {
				return false
			}
			previousNode = nil
			isAtPropertyKey = position > offset
			segments = append(segments, Segment{})
			return true
		},
		onObjectProperty: func(name string, offset, length int) bool {
			if position < offset {
				return false
			}
			setPreviousNode(name, offset, length, NodeTypeProperty)
			segments[len(segments)-1] = KeySegment(name)
			return position > offset+length
		},
		onObjectEnd: func(offset, _ int) bool {
			if position <= offset {
				return false
			}
			previousNode = nil
			segments = segments[:len(segments)-1]
			return true
		},
		onArrayBegin: func(offset, _ int) bool {
			if position <= offset {
				return false
			}
			previousNode = nil
			segments = append(segments, IndexSegment(0))
			return true
		},
		onArrayEnd: func(offset, _ int) bool {
			if position <= offset {
				return false
			}
			previousNode = nil
			segments = segments[:len(segments)-1]
			return true
		},
		onLiteralValue: func(value any, offset, length int) bool {
			if position < offset {
				return false
			}
			setPreviousNode(value, offset, length, GetNodeType(value))
			return position > offset+length
		},
		onSeparator: func(separator string, offset, _ int) bool {
			if position <= offset {
				return false
			}
			switch separator {
			case ":":
				if previousNode != nil && previousNode.Type == NodeTypeProperty {
					previousNode.ColonOffset = offset
					isAtPropertyKey = false
					previousNode = nil
				}
			case ",":
				if len(segments) > 0 {
					last := segments[len(segments)-1]
					if last.IsIndex {
						segments[len(segments)-1] = IndexSegment(last.Index + 1)
					} else {
						isAtPropertyKey = true
						segments[len(segments)-1] = Segment{}
					}
				}
				previousNode = nil
			}
			return true
		},
	}
	newParser(text, hooks, DefaultParseOptions).run()
	return Location{Path: segments, PreviousNode: previousNode, IsAtPropertyKey: isAtPropertyKey}
}
