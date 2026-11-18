// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// Package jsonc is a Go port of VS Code's fault-tolerant JSON scanner, tree
// parser and formatter (microsoft/vscode 972b66fdc541e10689adfd90ade9549dea1b18bc):
// src/vs/base/common/json.ts, jsonFormatter.ts and jsonErrorMessages.ts.
//
// VS Code offsets count UTF-16 code units. This package counts bytes into the
// Go input string instead: every Offset, Length and position passed to the API
// is a byte offset, including inside non-ASCII text. Byte offsets always point
// at rune boundaries for tokens produced by the scanner. A decoded string
// value is a Go string (UTF-8), so its rune count can differ from TokenLength.
//
// Parsing is fault tolerant: a truncated or malformed document still yields
// whatever tree could be read and a list of ParseError values with byte
// offsets. Formatting returns edits rather than a finished document, and
// likewise works on incomplete input.
//
// Some VS Code quirks are deliberately not copied. ParseTree returns the root
// value for an unclosed container (see its Go doc); Scanner keeps comment
// values limited to the comment text, preserves the "\r" of "\r\n" line-break
// trivia, and clamps an unterminated block comment to the end of the input
// instead of advancing past it. GetLocation still reports the key string as
// PreviousNode.Value for a property key.
package jsonc
