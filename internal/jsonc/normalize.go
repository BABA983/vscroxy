// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file builds on VS Code's tolerant JSONC parser and formatter.

package jsonc

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

// NormalizedJSON rewrites one JSON document with sorted object keys and a
// stable two-space indent. Arrays keep their order: only the map keys that
// JSON objects become are sorted. JSONC comments and trailing commas are
// accepted through StripComments. The text is left alone when it is not one
// complete JSON object or array, so a plain text body is never rewritten.
func NormalizedJSON(text string) (string, bool) {
	stripped := StripComments(text)
	switch firstNonSpace(stripped) {
	case '{', '[':
	default:
		return text, false
	}
	var value any
	if err := jsonv2.Unmarshal([]byte(stripped), &value); err != nil || value == nil {
		return text, false
	}
	data, err := jsonv2.Marshal(value, jsontext.WithIndent("  "), jsonv2.Deterministic(true))
	if err != nil {
		return text, false
	}
	return string(data), true
}

func firstNonSpace(text string) byte {
	for i := range len(text) {
		switch text[i] {
		case ' ', '\t', '\r', '\n':
		default:
			return text[i]
		}
	}
	return 0
}
