// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file ports src/vs/base/common/jsonFormatter.ts (and applyEdits from
// src/vs/base/common/jsonEdit.ts) from VS Code.

package jsonc

import (
	"cmp"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"slices"
	"strings"
)

// FormattingOptions is FormattingOptions in VS Code's jsonFormatter.ts.
// A zero TabSize means 4; a zero EOL means "\n" unless the text uses CRLF.
type FormattingOptions struct {
	TabSize      int
	InsertSpaces bool
	EOL          string
}

// Range is Range in VS Code's jsonFormatter.ts: the byte range to format.
type Range struct {
	Offset int
	Length int
}

// Edit is Edit in VS Code's jsonFormatter.ts. Offset and Length are byte
// offsets into the original text; Length 0 inserts and Content "" removes.
type Edit struct {
	Offset  int
	Length  int
	Content string
}

// Format is format in VS Code's jsonFormatter.ts. A nil rg formats the whole
// document. It returns the edits that produce the formatted text; invalid
// tokens and truncated documents still get best-effort indentation from the
// edits emitted before and after the error.
func Format(documentText string, rg *Range, options FormattingOptions) []Edit {
	tabSize := options.TabSize
	if tabSize <= 0 {
		tabSize = 4
	}

	formatText := documentText
	formatTextStart, rangeStart, rangeEnd, initialIndentLevel := 0, 0, len(documentText), 0
	if rg != nil {
		rangeStart = rg.Offset
		rangeEnd = rangeStart + rg.Length
		formatTextStart = min(max(rangeStart, 0), len(documentText))
		for formatTextStart > 0 && !isEOL(documentText, formatTextStart-1) {
			formatTextStart--
		}
		endOffset := min(max(rangeEnd, 0), len(documentText))
		for endOffset < len(documentText) && !isEOL(documentText, endOffset) {
			endOffset++
		}
		formatText = documentText[formatTextStart:endOffset]
		initialIndentLevel = computeIndentLevel(formatText, tabSize)
	}
	eol := GetEOL(options, documentText)

	indentValue := "\t"
	if options.InsertSpaces {
		indentValue = strings.Repeat(" ", tabSize)
	}
	indents := []string{""}
	indent := func(level int) string {
		if level <= 0 {
			return ""
		}
		for len(indents) <= level {
			indents = append(indents, indents[len(indents)-1]+indentValue)
		}
		return indents[level]
	}

	scanner := CreateScanner(formatText, false)
	hasError := false
	lineBreak := false
	indentLevel := 0

	newLineAndIndent := func() string {
		return eol + indent(initialIndentLevel+indentLevel)
	}
	scanNext := func() SyntaxKind {
		token := scanner.Scan()
		lineBreak = false
		for token == SyntaxKindTrivia || token == SyntaxKindLineBreakTrivia {
			lineBreak = lineBreak || token == SyntaxKindLineBreakTrivia
			token = scanner.Scan()
		}
		hasError = token == SyntaxKindUnknown || scanner.GetTokenError() != ScanErrorNone
		return token
	}
	edits := make([]Edit, 0)
	addEdit := func(text string, startOffset, endOffset int) {
		if !hasError && startOffset < rangeEnd && endOffset > rangeStart &&
			documentText[startOffset:endOffset] != text {
			edits = append(edits, Edit{Offset: startOffset, Length: endOffset - startOffset, Content: text})
		}
	}

	firstToken := scanNext()
	if firstToken != SyntaxKindEOF {
		addEdit(indent(initialIndentLevel), formatTextStart, scanner.GetTokenOffset()+formatTextStart)
	}

	for firstToken != SyntaxKindEOF {
		firstTokenEnd := scanner.GetTokenOffset() + scanner.GetTokenLength() + formatTextStart
		secondToken := scanNext()
		replaceContent := ""
		for !lineBreak && (secondToken == SyntaxKindLineCommentTrivia || secondToken == SyntaxKindBlockCommentTrivia) {
			addEdit(" ", firstTokenEnd, scanner.GetTokenOffset()+formatTextStart)
			firstTokenEnd = scanner.GetTokenOffset() + scanner.GetTokenLength() + formatTextStart
			if secondToken == SyntaxKindLineCommentTrivia {
				replaceContent = newLineAndIndent()
			} else {
				replaceContent = ""
			}
			secondToken = scanNext()
		}

		if secondToken == SyntaxKindCloseBraceToken {
			if firstToken != SyntaxKindOpenBraceToken {
				indentLevel--
				replaceContent = newLineAndIndent()
			}
		} else if secondToken == SyntaxKindCloseBracketToken {
			if firstToken != SyntaxKindOpenBracketToken {
				indentLevel--
				replaceContent = newLineAndIndent()
			}
		} else {
			switch firstToken {
			case SyntaxKindOpenBracketToken, SyntaxKindOpenBraceToken:
				indentLevel++
				replaceContent = newLineAndIndent()
			case SyntaxKindCommaToken, SyntaxKindLineCommentTrivia:
				replaceContent = newLineAndIndent()
			case SyntaxKindBlockCommentTrivia:
				if lineBreak {
					replaceContent = newLineAndIndent()
				} else {
					replaceContent = " "
				}
			case SyntaxKindColonToken:
				replaceContent = " "
			case SyntaxKindStringLiteral:
				if secondToken == SyntaxKindColonToken {
					replaceContent = ""
					break
				}
				fallthrough
			case SyntaxKindNullKeyword, SyntaxKindTrueKeyword, SyntaxKindFalseKeyword,
				SyntaxKindNumericLiteral, SyntaxKindCloseBraceToken, SyntaxKindCloseBracketToken:
				if secondToken == SyntaxKindLineCommentTrivia || secondToken == SyntaxKindBlockCommentTrivia {
					replaceContent = " "
				} else if secondToken != SyntaxKindCommaToken && secondToken != SyntaxKindEOF {
					hasError = true
				}
			case SyntaxKindUnknown:
				hasError = true
			}
			if lineBreak && (secondToken == SyntaxKindLineCommentTrivia || secondToken == SyntaxKindBlockCommentTrivia) {
				replaceContent = newLineAndIndent()
			}
		}
		addEdit(replaceContent, firstTokenEnd, scanner.GetTokenOffset()+formatTextStart)
		firstToken = secondToken
	}
	return edits
}

// ApplyEdits is applyEdits in VS Code's jsonEdit.ts. It sorts a copy of the
// edits and writes them in one pass, so the input slice is not changed and
// earlier edits keep their original byte offsets.
func ApplyEdits(text string, edits []Edit) (string, error) {
	sorted := slices.Clone(edits)
	slices.SortFunc(sorted, func(a, b Edit) int {
		return cmp.Or(cmp.Compare(a.Offset, b.Offset), cmp.Compare(a.Length, b.Length))
	})
	var result strings.Builder
	result.Grow(len(text))
	cursor := 0
	for _, edit := range sorted {
		if edit.Offset < 0 || edit.Length < 0 || edit.Offset > len(text) || edit.Length > len(text)-edit.Offset {
			return "", errors.New("invalid edit")
		}
		if edit.Offset < cursor {
			return "", errors.New("Overlapping edit")
		}
		result.WriteString(text[cursor:edit.Offset])
		result.WriteString(edit.Content)
		cursor = edit.Offset + edit.Length
	}
	result.WriteString(text[cursor:])
	return result.String(), nil
}

// ToFormattedString is toFormattedString in VS Code's jsonFormatter.ts: it
// marshals a decoded value and applies the requested indentation and EOL.
func ToFormattedString(value any, options FormattingOptions) (string, error) {
	tabSize := options.TabSize
	if tabSize <= 0 {
		tabSize = 4
	}
	indent := "\t"
	if options.InsertSpaces {
		indent = strings.Repeat(" ", tabSize)
	}
	data, err := jsonv2.Marshal(value, jsontext.WithIndent(indent))
	if err != nil {
		return "", err
	}
	if options.EOL == "" || options.EOL == "\n" {
		return string(data), nil
	}
	return strings.NewReplacer("\r\n", options.EOL, "\r", options.EOL, "\n", options.EOL).Replace(string(data)), nil
}

// GetEOL is getEOL in VS Code's jsonFormatter.ts: the first line break in the
// text wins, otherwise the option value or "\n".
func GetEOL(options FormattingOptions, text string) string {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		case '\n':
			return "\n"
		}
	}
	if options.EOL != "" {
		return options.EOL
	}
	return "\n"
}

func isEOL(text string, offset int) bool {
	return text[offset] == '\r' || text[offset] == '\n'
}

func computeIndentLevel(content string, tabSize int) int {
	nChars := 0
	for i := 0; i < len(content); i++ {
		switch content[i] {
		case ' ':
			nChars++
		case '\t':
			nChars += tabSize
		default:
			return nChars / tabSize
		}
	}
	return nChars / tabSize
}
