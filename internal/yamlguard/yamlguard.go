// Package yamlguard converts YAML to JSON with a bound on alias expansion.
//
// YAML aliases are cheap in the file and expensive after expansion: a few
// hundred KiB can describe gigabytes of repeated nodes. Postman Collection v3
// never writes anchors, so its reader rejects aliases outright. Hand-written
// Swagger, OpenAPI and Insomnia files may use a few anchors legitimately, so
// those readers allow them but cap the expanded node count and byte size.
package yamlguard

import (
	"errors"
	"fmt"

	"github.com/oasdiff/yaml"
	yaml3 "github.com/oasdiff/yaml3"
)

// ErrAlias is returned when a caller forbids YAML aliases.
var ErrAlias = errors.New("YAML aliases are not supported")

// ErrExpansion marks a document whose aliases expand beyond the configured
// node, byte or depth budget. Callers that sniff a format can surface it
// instead of treating the document as an unknown file.
var ErrExpansion = errors.New("YAML expands beyond")

// Options bounds one document. Limits apply to content reached through an
// alias; the original document is bounded by its input size.
type Options struct {
	// RejectAliases refuses any AliasNode before expanding it.
	RejectAliases bool
	// MaxNodes bounds the alias-expanded node count.
	MaxNodes int
	// MaxBytes bounds the JSON-escaped size of alias-expanded scalars. The
	// count is conservative: every scalar byte is charged as six JSON bytes,
	// the worst case for characters such as '<'.
	MaxBytes int
}

const (
	defaultMaxNodes = 1_000_000
	defaultMaxBytes = 16 << 20
	maxDepth        = 100
	// jsonEscapeFactor is the largest growth of one YAML scalar byte in JSON:
	// '<' becomes the six bytes `\u003c`.
	jsonEscapeFactor = 6
)

// ToJSON decodes YAML and returns its JSON form. It rejects aliases when
// asked. When aliases are present, content reached through each alias counts
// toward the node, byte and depth budgets; the original document does not.
// A document without aliases skips those checks because there is nothing to
// expand.
//
// The original bytes are converted, never a re-marshalled node: marshalling a
// yaml.Node and converting that loses block-scalar details such as a leading
// empty line or folding indentation.
func ToJSON(data []byte, opts Options) ([]byte, error) {
	aliases, err := hasAliasDocument(data)
	if err != nil {
		return nil, err
	}
	if opts.RejectAliases && aliases {
		return nil, ErrAlias
	}
	if aliases {
		if opts.MaxNodes <= 0 {
			opts.MaxNodes = defaultMaxNodes
		}
		if opts.MaxBytes <= 0 {
			opts.MaxBytes = defaultMaxBytes
		}
		var node yaml3.Node
		if err := yaml3.Unmarshal(data, &node); err != nil {
			return nil, err
		}
		counter := &expansion{maxNodes: opts.MaxNodes, maxBytes: opts.MaxBytes, nodes: opts.MaxNodes, bytes: opts.MaxBytes, visiting: map[*yaml3.Node]bool{}}
		if err := counter.walk(&node, 0, false); err != nil {
			return nil, err
		}
	}
	return yaml.YAMLToJSON(data)
}

// hasAliasDocument parses just enough to tell whether the document contains
// an alias. The node tree is local to this function, so a plain document can
// release it before the real conversion runs.
func hasAliasDocument(data []byte) (bool, error) {
	var node yaml3.Node
	if err := yaml3.Unmarshal(data, &node); err != nil {
		return false, err
	}
	return hasAlias(&node), nil
}

func hasAlias(node *yaml3.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml3.AliasNode {
		return true
	}
	for _, child := range node.Content {
		if hasAlias(child) {
			return true
		}
	}
	return false
}

// expansion counts only the content an alias expands to. The original
// document is already bounded by its input size, so its nodes and scalars are
// free; each alias target and its descendants consume the budget.
type expansion struct {
	maxNodes, maxBytes int
	nodes, bytes       int
	visiting           map[*yaml3.Node]bool
}

func (e *expansion) walk(node *yaml3.Node, aliasDepth int, expanded bool) error {
	if node == nil {
		return nil
	}
	if e.visiting[node] {
		return fmt.Errorf("%w: recursive alias", ErrExpansion)
	}
	e.visiting[node] = true
	defer delete(e.visiting, node)
	if node.Kind == yaml3.AliasNode {
		return e.walk(node.Alias, aliasDepth+1, true)
	}
	if expanded {
		if aliasDepth > maxDepth {
			return fmt.Errorf("%w %d levels", ErrExpansion, maxDepth)
		}
		e.nodes--
		if e.nodes < 0 {
			return fmt.Errorf("%w %d nodes", ErrExpansion, e.maxNodes)
		}
		e.bytes -= jsonEscapeFactor * len(node.Value)
		if e.bytes < 0 {
			return fmt.Errorf("%w %d bytes", ErrExpansion, e.maxBytes)
		}
	}
	for _, child := range node.Content {
		childDepth := 0
		if expanded {
			childDepth = aliasDepth + 1
		}
		if err := e.walk(child, childDepth, expanded); err != nil {
			return err
		}
	}
	return nil
}
