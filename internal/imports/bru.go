package imports

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// bruBlock is one top-level .bru block. Text blocks keep their content;
// dictionary and list blocks keep the parsed rows.
type bruBlock struct {
	tag    string
	text   string
	pairs  []bruPair
	items  []string
	opened byte
}

type bruPair struct {
	key         string
	value       string
	disabled    bool
	contentType string
	multiline   bool
}

// parseBru reads a .bru file. The grammar is line-oriented: a tag starts at
// column 0, a dictionary closes with } at column 0, a list closes with ] at
// column 0, and a text block keeps every line between them.
func parseBru(data []byte) ([]bruBlock, map[string]string, error) {
	if len(data) > 16<<20 {
		return nil, nil, errors.New(".bru files are limited to 16 MiB")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var blocks []bruBlock
	scalars := map[string]string{}
	for i := 0; i < len(lines); {
		line := strings.TrimSuffix(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return nil, nil, fmt.Errorf("line %d: a top-level block must start at column 0", i+1)
		}
		open := strings.IndexAny(line, "{[")
		if open < 0 {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				return nil, nil, fmt.Errorf("line %d: expected a block or key: value", i+1)
			}
			scalars[strings.TrimSpace(key)] = strings.TrimSpace(value)
			i++
			continue
		}
		tag := strings.TrimSpace(line[:open])
		opener := line[open]
		if tag == "" || strings.TrimSpace(line[open+1:]) != "" {
			return nil, nil, fmt.Errorf("line %d: malformed block header", i+1)
		}
		closer := byte('}')
		if opener == '[' {
			closer = ']'
		}
		start := i + 1
		end := start
		for end < len(lines) && strings.TrimSuffix(lines[end], "\r") != string(closer) {
			end++
		}
		if end >= len(lines) {
			return nil, nil, fmt.Errorf("block %q is not closed", tag)
		}
		content := strings.Join(lines[start:end], "\n")
		block := bruBlock{tag: tag, opened: opener}
		switch opener {
		case '[':
			for _, item := range strings.Split(content, ",") {
				item = strings.TrimSpace(item)
				if item == "" || strings.HasPrefix(item, "~") {
					continue
				}
				block.items = append(block.items, item)
			}
		case '{':
			if isBruTextTag(tag) {
				block.text = bruOutdent(content)
			} else {
				pairs, err := parseBruPairs(content)
				if err != nil {
					return nil, nil, fmt.Errorf("block %q: %w", tag, err)
				}
				block.pairs = pairs
			}
		}
		blocks = append(blocks, block)
		i = end + 1
	}
	return blocks, scalars, nil
}

// isBruTextTag says whether a block's content is free text rather than
// key: value rows.
func isBruTextTag(tag string) bool {
	switch tag {
	case "body", "body:json", "body:text", "body:xml", "body:sparql", "body:graphql", "body:graphql:vars",
		"script:pre-request", "script:post-response", "tests", "docs",
		"script:grpc:before-call-start", "script:grpc:before-message-send",
		"script:grpc:after-message-receive", "script:grpc:after-call-end":
		return true
	}
	return false
}

// bruOutdent removes up to two leading spaces from each line, as Bruno's
// text-block writer adds them.
func bruOutdent(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "  ") {
			lines[i] = line[2:]
		}
	}
	return strings.TrimSuffix(strings.Join(lines, "\n"), "\n")
}

// parseBruPairs reads dictionary rows, including quoted keys, ~ disabled
// rows, ”' multiline values and @description annotations.
func parseBruPairs(content string) ([]bruPair, error) {
	lines := strings.Split(content, "\n")
	var pairs []bruPair
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "@") {
			annotation := strings.TrimPrefix(line, "@")
			isAnnotation := strings.HasPrefix(annotation, "description(") || annotation == "number" ||
				annotation == "boolean" || annotation == "object" || strings.HasPrefix(annotation, "contentType(")
			if isAnnotation {
				// @description(''' ... ''') or a typed-variable annotation. The
				// annotation does not change the value for an importer.
				if strings.Contains(line, "'''") && !strings.HasSuffix(line, "''')") {
					for i++; i < len(lines) && !strings.Contains(lines[i], "''')"); i++ {
					}
				}
				continue
			}
		}
		disabled := false
		if strings.HasPrefix(line, "~") {
			disabled = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "~"))
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("line %q is not key: value", line)
		}
		key = strings.TrimSpace(key)
		if strings.HasPrefix(key, "\"") {
			unquoted, err := strconv.Unquote(key)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted key %q", key)
			}
			key = unquoted
		}
		rest = strings.TrimSpace(rest)
		pair := bruPair{key: key, disabled: disabled}
		if rest == "'''" {
			var value []string
			closed := false
			for i++; i < len(lines); i++ {
				trimmed := strings.TrimSpace(lines[i])
				if strings.HasPrefix(trimmed, "'''") {
					if at := strings.Index(trimmed, "@contentType("); at >= 0 {
						pair.contentType = strings.TrimSuffix(strings.TrimPrefix(trimmed[at+len("@contentType("):], "("), ")")
					}
					closed = true
					break
				}
				value = append(value, strings.TrimPrefix(lines[i], "    "))
			}
			if !closed {
				return nil, fmt.Errorf("multiline value for %q is not closed", key)
			}
			pair.value = strings.TrimSpace(strings.Join(value, "\n"))
			pair.multiline = true
		} else {
			pair.value = rest
			if at := strings.Index(rest, "@contentType("); at >= 0 {
				end := strings.Index(rest[at:], ")")
				if end >= 0 {
					pair.contentType = rest[at+len("@contentType(") : at+end]
					pair.value = strings.TrimSpace(rest[:at])
				}
			}
		}
		pairs = append(pairs, pair)
	}
	return pairs, nil
}

// bruBlocks indexes blocks by tag, keeping repeats.
type bruBlocks map[string][]bruBlock

func indexBru(blocks []bruBlock) bruBlocks {
	out := bruBlocks{}
	for _, block := range blocks {
		out[block.tag] = append(out[block.tag], block)
	}
	return out
}

func (b bruBlocks) first(tag string) (bruBlock, bool) {
	list := b[tag]
	if len(list) == 0 {
		return bruBlock{}, false
	}
	return list[0], true
}

func (b bruBlocks) text(tag string) string {
	block, _ := b.first(tag)
	return block.text
}

func (b bruBlocks) pairs(tag string) []bruPair {
	block, _ := b.first(tag)
	return block.pairs
}

func (b bruBlocks) has(tag string) bool { return len(b[tag]) > 0 }

// bruMeta is the meta block of a request or folder.
type bruMeta struct {
	name string
	kind string
	seq  int
}

func readBruMeta(blocks bruBlocks) bruMeta {
	meta := bruMeta{kind: "http", seq: 0}
	for _, pair := range blocks.pairs("meta") {
		switch pair.key {
		case "name":
			meta.name = pair.value
		case "type":
			meta.kind = strings.ToLower(pair.value)
		case "seq":
			meta.seq, _ = strconv.Atoi(pair.value)
		}
	}
	return meta
}

// bruMethod finds the method block: get/post/put/... or http with a method
// key. It returns the verb and the block's key: value rows.
func bruMethod(blocks bruBlocks) (string, map[string]string, bool) {
	for _, tag := range []string{"get", "post", "put", "patch", "delete", "head", "options", "connect", "trace"} {
		if block, ok := blocks.first(tag); ok {
			return strings.ToUpper(tag), bruMap(block.pairs), true
		}
	}
	if block, ok := blocks.first("http"); ok {
		values := bruMap(block.pairs)
		return strings.ToUpper(cmpOr(values["method"], "GET")), values, true
	}
	return "", nil, false
}

func bruMap(pairs []bruPair) map[string]string {
	out := map[string]string{}
	for _, pair := range pairs {
		if !pair.disabled {
			out[pair.key] = pair.value
		}
	}
	return out
}

func cmpOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
