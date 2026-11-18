// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file ports src/vs/base/common/jsonErrorMessages.ts from VS Code. The
// messages are the English source strings; VS Code's localize wrapper is not
// part of this package.

package jsonc

// GetParseErrorMessage is getParseErrorMessage in VS Code's json.ts
// (jsonErrorMessages.ts). It returns the empty string for codes that VS Code
// does not map.
func GetParseErrorMessage(code ParseErrorCode) string {
	switch code {
	case ParseErrorCodeInvalidSymbol:
		return "Invalid symbol"
	case ParseErrorCodeInvalidNumberFormat:
		return "Invalid number format"
	case ParseErrorCodePropertyNameExpected:
		return "Property name expected"
	case ParseErrorCodeValueExpected:
		return "Value expected"
	case ParseErrorCodeColonExpected:
		return "Colon expected"
	case ParseErrorCodeCommaExpected:
		return "Comma expected"
	case ParseErrorCodeCloseBraceExpected:
		return "Closing brace expected"
	case ParseErrorCodeCloseBracketExpected:
		return "Closing bracket expected"
	case ParseErrorCodeEndOfFileExpected:
		return "End of file expected"
	default:
		return ""
	}
}
