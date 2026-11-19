// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file builds on VS Code's tolerant JSONC parser and formatter.

package jsonc

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"slices"
	"strings"
)

// NormalizedJSON rewrites one JSON document with sorted object keys and a
// stable two-space indent. Arrays keep their order, and every literal keeps
// its text: decoding into float64 would make ids that differ long after the
// 53rd bit compare equal, and would turn 1.0 into 1. JSONC comments and
// trailing commas are accepted through StripComments. The text is left alone
// when it is not one complete JSON object or array, so a plain text body is
// never rewritten.
func NormalizedJSON(text string) (string, bool) {
	stripped := StripComments(text)
	if !jsontext.Value(stripped).IsValid() {
		return text, false
	}
	decoder := jsontext.NewDecoder(strings.NewReader(stripped))
	first, err := decoder.ReadToken()
	if err != nil || first.Kind() != jsontext.KindBeginObject && first.Kind() != jsontext.KindBeginArray {
		return text, false
	}
	var out strings.Builder
	if err := writeNormalizedValue(&out, decoder, first, 0); err != nil {
		return text, false
	}
	// Value.IsValid already proved this is one complete JSON value, so no
	// trailing-token check is needed; ReadToken's EOF is not an exported
	// sentinel in encoding/json/jsontext.
	return out.String(), true
}

func writeNormalizedValue(out *strings.Builder, decoder *jsontext.Decoder, token jsontext.Token, depth int) error {
	switch token.Kind() {
	case jsontext.KindBeginObject:
		return writeNormalizedObject(out, decoder, depth)
	case jsontext.KindBeginArray:
		return writeNormalizedArray(out, decoder, depth)
	case jsontext.KindEndObject, jsontext.KindEndArray:
		return errors.New("jsonc: unexpected closing delimiter")
	case jsontext.KindString:
		// A string token is the decoded text; quote it again to write JSON.
		quoted, err := jsonv2.Marshal(token.String())
		if err != nil {
			return err
		}
		out.Write(quoted)
		return nil
	default:
		// A number or literal token is its raw JSON spelling. Writing it
		// directly preserves precision and the author's 1.0 over 1.
		out.WriteString(token.String())
		return nil
	}
}

type normalizedMember struct {
	key, rawKey, value string
}

func writeNormalizedObject(out *strings.Builder, decoder *jsontext.Decoder, depth int) error {
	var members []normalizedMember
	for {
		keyToken, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		if keyToken.Kind() == jsontext.KindEndObject {
			break
		}
		if keyToken.Kind() != jsontext.KindString {
			return errors.New("jsonc: object name must be a string")
		}
		key := keyToken.String()
		quotedKey, err := jsonv2.Marshal(key)
		if err != nil {
			return err
		}
		rawKey := string(quotedKey)
		valueToken, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		var value strings.Builder
		if err := writeNormalizedValue(&value, decoder, valueToken, depth+1); err != nil {
			return err
		}
		members = append(members, normalizedMember{key: key, rawKey: rawKey, value: value.String()})
	}
	if len(members) == 0 {
		out.WriteString("{}")
		return nil
	}
	slices.SortFunc(members, func(a, b normalizedMember) int { return strings.Compare(a.key, b.key) })
	out.WriteString("{\n")
	for i, member := range members {
		writeIndent(out, depth+1)
		out.WriteString(member.rawKey)
		out.WriteString(": ")
		out.WriteString(member.value)
		if i < len(members)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	writeIndent(out, depth)
	out.WriteByte('}')
	return nil
}

func writeNormalizedArray(out *strings.Builder, decoder *jsontext.Decoder, depth int) error {
	first, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if first.Kind() == jsontext.KindEndArray {
		out.WriteString("[]")
		return nil
	}
	out.WriteString("[\n")
	for token := first; ; {
		writeIndent(out, depth+1)
		if err := writeNormalizedValue(out, decoder, token, depth+1); err != nil {
			return err
		}
		next, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		if next.Kind() == jsontext.KindEndArray {
			break
		}
		out.WriteString(",\n")
		token = next
	}
	out.WriteByte('\n')
	writeIndent(out, depth)
	out.WriteByte(']')
	return nil
}

func writeIndent(out *strings.Builder, depth int) {
	for range depth {
		out.WriteString("  ")
	}
}
