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

// Options bounds one document. A zero MaxNodes or MaxBytes uses the default.
type Options struct {
	// RejectAliases refuses any AliasNode before expanding it.
	RejectAliases bool
	// MaxNodes bounds the expanded node count.
	MaxNodes int
	// MaxBytes bounds the expanded scalar bytes.
	MaxBytes int
}

const (
	defaultMaxNodes = 1_000_000
	defaultMaxBytes = 64 << 20
	maxDepth        = 100
)

// ToJSON decodes YAML and returns its JSON form. It rejects aliases when
// asked, and otherwise stops as soon as the expanded document exceeds the
// node or byte budget.
func ToJSON(data []byte, opts Options) ([]byte, error) {
	var node yaml3.Node
	if err := yaml3.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if opts.RejectAliases && hasAlias(&node) {
		return nil, ErrAlias
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = defaultMaxNodes
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	counter := &expansion{maxNodes: opts.MaxNodes, maxBytes: opts.MaxBytes, nodes: opts.MaxNodes, bytes: opts.MaxBytes, visiting: map[*yaml3.Node]bool{}}
	if err := counter.walk(&node, 0); err != nil {
		return nil, err
	}
	expanded, err := yaml3.Marshal(&node)
	if err != nil {
		return nil, err
	}
	return yaml.YAMLToJSON(expanded)
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

// expansion counts the document as it would be after every alias is replaced
// by its target. It stops at the first limit and detects recursive aliases.
type expansion struct {
	maxNodes, maxBytes int
	nodes, bytes       int
	visiting           map[*yaml3.Node]bool
}

func (e *expansion) walk(node *yaml3.Node, depth int) error {
	if node == nil {
		return nil
	}
	if depth > maxDepth {
		return fmt.Errorf("%w %d levels", ErrExpansion, maxDepth)
	}
	if e.visiting[node] {
		return fmt.Errorf("%w: recursive alias", ErrExpansion)
	}
	e.visiting[node] = true
	defer delete(e.visiting, node)
	if node.Kind == yaml3.AliasNode {
		return e.walk(node.Alias, depth+1)
	}
	e.nodes--
	if e.nodes < 0 {
		return fmt.Errorf("%w %d nodes", ErrExpansion, e.maxNodes)
	}
	e.bytes -= len(node.Value)
	if e.bytes < 0 {
		return fmt.Errorf("%w %d bytes", ErrExpansion, e.maxBytes)
	}
	for _, child := range node.Content {
		if err := e.walk(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}
