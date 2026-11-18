// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file ports src/vs/base/common/jsonc.ts from VS Code.

package jsonc

// StripComments is stripComments in VS Code's jsonc.ts. It removes block and
// line comments and trailing commas from JSONC. All other bytes, including
// whitespace and line breaks, are copied unchanged.
//
// Unlike the VS Code regex, this uses the JSONC scanner, so a comment marker
// or an escaped quote inside a string is never removed. The whole input must
// also parse as JSONC; otherwise it is returned unchanged. That keeps a
// partially received or non-JSON body from being silently altered before it
// is sent.
func StripComments(text string) string {
	if _, errs := ParseTree(text); len(errs) != 0 {
		return text
	}
	scanner := CreateScanner(text, false)
	drops := make([]Edit, 0)
	commaOffset, commaLength := -1, 0
	for token := scanner.Scan(); token != SyntaxKindEOF; token = scanner.Scan() {
		offset, length := scanner.GetTokenOffset(), scanner.GetTokenLength()
		switch token {
		case SyntaxKindLineCommentTrivia, SyntaxKindBlockCommentTrivia:
			// A line comment's token stops before its line break, which is
			// trivia we keep. Deleting only the token preserves the EOL.
			drops = append(drops, Edit{Offset: offset, Length: length})
		case SyntaxKindTrivia, SyntaxKindLineBreakTrivia:
			// Whitespace never decides whether a comma is trailing.
		case SyntaxKindCommaToken:
			commaOffset, commaLength = offset, length
		case SyntaxKindCloseBraceToken, SyntaxKindCloseBracketToken:
			if commaOffset >= 0 {
				drops = append(drops, Edit{Offset: commaOffset, Length: commaLength})
				commaOffset = -1
			}
		default:
			commaOffset = -1
		}
	}
	if len(drops) == 0 {
		return text
	}
	stripped, err := ApplyEdits(text, drops)
	if err != nil {
		return text
	}
	return stripped
}
