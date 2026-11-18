// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See [VSCODE_LICENSE.txt] in this directory.
//
// This file ports src/vs/base/common/json.ts from VS Code.

package jsonc

import (
	"errors"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// ScanError is ScanError in VS Code's json.ts.
type ScanError uint8

const (
	// ScanErrorNone is ScanError.None.
	ScanErrorNone ScanError = iota
	// ScanErrorUnexpectedEndOfComment is ScanError.UnexpectedEndOfComment.
	ScanErrorUnexpectedEndOfComment
	// ScanErrorUnexpectedEndOfString is ScanError.UnexpectedEndOfString.
	ScanErrorUnexpectedEndOfString
	// ScanErrorUnexpectedEndOfNumber is ScanError.UnexpectedEndOfNumber.
	ScanErrorUnexpectedEndOfNumber
	// ScanErrorInvalidUnicode is ScanError.InvalidUnicode.
	ScanErrorInvalidUnicode
	// ScanErrorInvalidEscapeCharacter is ScanError.InvalidEscapeCharacter.
	ScanErrorInvalidEscapeCharacter
	// ScanErrorInvalidCharacter is ScanError.InvalidCharacter.
	ScanErrorInvalidCharacter
)

// SyntaxKind is SyntaxKind in VS Code's json.ts.
type SyntaxKind uint8

const (
	// SyntaxKindOpenBraceToken is SyntaxKind.OpenBraceToken.
	SyntaxKindOpenBraceToken SyntaxKind = 1 + iota
	// SyntaxKindCloseBraceToken is SyntaxKind.CloseBraceToken.
	SyntaxKindCloseBraceToken
	// SyntaxKindOpenBracketToken is SyntaxKind.OpenBracketToken.
	SyntaxKindOpenBracketToken
	// SyntaxKindCloseBracketToken is SyntaxKind.CloseBracketToken.
	SyntaxKindCloseBracketToken
	// SyntaxKindCommaToken is SyntaxKind.CommaToken.
	SyntaxKindCommaToken
	// SyntaxKindColonToken is SyntaxKind.ColonToken.
	SyntaxKindColonToken
	// SyntaxKindNullKeyword is SyntaxKind.NullKeyword.
	SyntaxKindNullKeyword
	// SyntaxKindTrueKeyword is SyntaxKind.TrueKeyword.
	SyntaxKindTrueKeyword
	// SyntaxKindFalseKeyword is SyntaxKind.FalseKeyword.
	SyntaxKindFalseKeyword
	// SyntaxKindStringLiteral is SyntaxKind.StringLiteral.
	SyntaxKindStringLiteral
	// SyntaxKindNumericLiteral is SyntaxKind.NumericLiteral.
	SyntaxKindNumericLiteral
	// SyntaxKindLineCommentTrivia is SyntaxKind.LineCommentTrivia.
	SyntaxKindLineCommentTrivia
	// SyntaxKindBlockCommentTrivia is SyntaxKind.BlockCommentTrivia.
	SyntaxKindBlockCommentTrivia
	// SyntaxKindLineBreakTrivia is SyntaxKind.LineBreakTrivia.
	SyntaxKindLineBreakTrivia
	// SyntaxKindTrivia is SyntaxKind.Trivia.
	SyntaxKindTrivia
	// SyntaxKindUnknown is SyntaxKind.Unknown.
	SyntaxKindUnknown
	// SyntaxKindEOF is SyntaxKind.EOF.
	SyntaxKindEOF
)

// ParseErrorCode is ParseErrorCode in VS Code's json.ts.
type ParseErrorCode uint8

const (
	// ParseErrorCodeInvalidSymbol is ParseErrorCode.InvalidSymbol.
	ParseErrorCodeInvalidSymbol ParseErrorCode = 1 + iota
	// ParseErrorCodeInvalidNumberFormat is ParseErrorCode.InvalidNumberFormat.
	ParseErrorCodeInvalidNumberFormat
	// ParseErrorCodePropertyNameExpected is ParseErrorCode.PropertyNameExpected.
	ParseErrorCodePropertyNameExpected
	// ParseErrorCodeValueExpected is ParseErrorCode.ValueExpected.
	ParseErrorCodeValueExpected
	// ParseErrorCodeColonExpected is ParseErrorCode.ColonExpected.
	ParseErrorCodeColonExpected
	// ParseErrorCodeCommaExpected is ParseErrorCode.CommaExpected.
	ParseErrorCodeCommaExpected
	// ParseErrorCodeCloseBraceExpected is ParseErrorCode.CloseBraceExpected.
	ParseErrorCodeCloseBraceExpected
	// ParseErrorCodeCloseBracketExpected is ParseErrorCode.CloseBracketExpected.
	ParseErrorCodeCloseBracketExpected
	// ParseErrorCodeEndOfFileExpected is ParseErrorCode.EndOfFileExpected.
	ParseErrorCodeEndOfFileExpected
	// ParseErrorCodeInvalidCommentToken is ParseErrorCode.InvalidCommentToken.
	ParseErrorCodeInvalidCommentToken
	// ParseErrorCodeUnexpectedEndOfComment is ParseErrorCode.UnexpectedEndOfComment.
	ParseErrorCodeUnexpectedEndOfComment
	// ParseErrorCodeUnexpectedEndOfString is ParseErrorCode.UnexpectedEndOfString.
	ParseErrorCodeUnexpectedEndOfString
	// ParseErrorCodeUnexpectedEndOfNumber is ParseErrorCode.UnexpectedEndOfNumber.
	ParseErrorCodeUnexpectedEndOfNumber
	// ParseErrorCodeInvalidUnicode is ParseErrorCode.InvalidUnicode.
	ParseErrorCodeInvalidUnicode
	// ParseErrorCodeInvalidEscapeCharacter is ParseErrorCode.InvalidEscapeCharacter.
	ParseErrorCodeInvalidEscapeCharacter
	// ParseErrorCodeInvalidCharacter is ParseErrorCode.InvalidCharacter.
	ParseErrorCodeInvalidCharacter
)

// ParseError is ParseError in VS Code's json.ts. Offset and Length are byte
// offsets into the parsed text.
type ParseError struct {
	Error  ParseErrorCode
	Offset int
	Length int
}

// ParseOptions is ParseOptions in VS Code's json.ts. Passing no options uses
// DefaultParseOptions, which allows trailing commas. An explicitly passed
// ParseOptions{} disallows them, like an explicitly passed empty object in
// VS Code.
type ParseOptions struct {
	DisallowComments   bool
	AllowTrailingComma bool
	AllowEmptyContent  bool
}

// DefaultParseOptions is ParseOptions.DEFAULT in VS Code's json.ts.
var DefaultParseOptions = ParseOptions{AllowTrailingComma: true}

func parseOptions(options []ParseOptions) ParseOptions {
	if len(options) == 0 {
		return DefaultParseOptions
	}
	return options[0]
}

// JSONVisitor is JSONVisitor in VS Code's json.ts. Offsets and lengths are
// byte offsets. The callbacks are invoked by Visit in document order.
type JSONVisitor struct {
	OnObjectBegin    func(offset, length int)
	OnObjectProperty func(property string, offset, length int)
	OnObjectEnd      func(offset, length int)
	OnArrayBegin     func(offset, length int)
	OnArrayEnd       func(offset, length int)
	OnLiteralValue   func(value any, offset, length int)
	OnSeparator      func(character string, offset, length int)
	OnComment        func(offset, length int)
	OnError          func(error ParseErrorCode, offset, length int)
}

// Scanner is JSONScanner in VS Code's json.ts: a scanner positioned somewhere
// in the input text. An unescaped token value is a substring of the input;
// only strings with escapes allocate a new value. Values stay valid after
// subsequent scans.
//
// Two VS Code scanner quirks are intentionally not copied: comment token
// values contain only the comment text (VS Code includes the character before
// it), and an unterminated block comment stops at the end of the input (VS
// Code advances one unit past it, making lengths and later offsets invalid).
type Scanner struct {
	text         string
	pos          int
	size         int
	value        string
	scratch      []byte
	tokenOffset  int
	token        SyntaxKind
	tokenError   ScanError
	ignoreTrivia bool
}

// CreateScanner is createScanner in VS Code's json.ts. When ignoreTrivia is
// set, Scan skips whitespace and comments.
func CreateScanner(text string, ignoreTrivia bool) *Scanner {
	return &Scanner{text: text, size: len(text), ignoreTrivia: ignoreTrivia}
}

// SetPosition is JSONScanner.setPosition in VS Code's json.ts. Positions are
// clamped to the input; a scan is needed to read the first token.
func (s *Scanner) SetPosition(pos int) {
	if pos < 0 {
		pos = 0
	} else if pos > s.size {
		pos = s.size
	}
	s.pos = pos
	s.value = ""
	s.tokenOffset = 0
	s.token = SyntaxKindUnknown
	s.tokenError = ScanErrorNone
}

// Scan is JSONScanner.scan in VS Code's json.ts. It returns the SyntaxKind of
// the next token.
func (s *Scanner) Scan() SyntaxKind {
	if s.ignoreTrivia {
		for {
			token := s.scanNext()
			if token < SyntaxKindLineCommentTrivia || token > SyntaxKindTrivia {
				return token
			}
		}
	}
	return s.scanNext()
}

// GetPosition is JSONScanner.getPosition in VS Code's json.ts: the byte offset
// just after the last token.
func (s *Scanner) GetPosition() int { return s.pos }

// GetToken is JSONScanner.getToken in VS Code's json.ts.
func (s *Scanner) GetToken() SyntaxKind { return s.token }

// GetTokenValue is JSONScanner.getTokenValue in VS Code's json.ts. Strings are
// decoded, numbers are the raw numeric text, and trivia is the source text.
// The returned string is a substring for unescaped tokens.
func (s *Scanner) GetTokenValue() string { return s.value }

// GetTokenOffset is JSONScanner.getTokenOffset in VS Code's json.ts: the byte
// offset where the last token starts.
func (s *Scanner) GetTokenOffset() int { return s.tokenOffset }

// GetTokenLength is JSONScanner.getTokenLength in VS Code's json.ts: the byte
// length of the last token.
func (s *Scanner) GetTokenLength() int { return s.pos - s.tokenOffset }

// GetTokenError is JSONScanner.getTokenError in VS Code's json.ts.
func (s *Scanner) GetTokenError() ScanError { return s.tokenError }

// runeAt reads one rune without allocating; ASCII bytes are the common case.
func runeAt(text string, pos int) (rune, int) {
	if b := text[pos]; b < utf8.RuneSelf {
		return rune(b), 1
	}
	r, size := utf8.DecodeRuneInString(text[pos:])
	return r, size
}

func isLineBreakRune(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

func whitespaceSizeAt(text string, pos int) int {
	if b := text[pos]; b < utf8.RuneSelf {
		if isWhitespaceRune(rune(b)) {
			return 1
		}
		return 0
	}
	r, size := runeAt(text, pos)
	if isWhitespaceRune(r) {
		return size
	}
	return 0
}

func isWhitespaceRune(r rune) bool {
	switch r {
	case ' ', '\t', '\v', '\f', 0xA0, 0x1680, 0x2000, 0x2001, 0x2002,
		0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A,
		0x200B, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return false
}

func isUnknownContentRune(r rune) bool {
	if isWhitespaceRune(r) || isLineBreakRune(r) {
		return false
	}
	switch r {
	case '}', ']', '{', '[', '"', ':', ',', '/':
		return false
	}
	return true
}

func (s *Scanner) scanHexDigits(count int) int {
	hexValue := 0
	digits := 0
	for digits < count && s.pos < s.size {
		ch := s.text[s.pos]
		switch {
		case ch >= '0' && ch <= '9':
			hexValue = hexValue*16 + int(ch-'0')
		case ch >= 'A' && ch <= 'F':
			hexValue = hexValue*16 + int(ch-'A') + 10
		case ch >= 'a' && ch <= 'f':
			hexValue = hexValue*16 + int(ch-'a') + 10
		default:
			return -1
		}
		s.pos++
		digits++
	}
	if digits < count {
		return -1
	}
	return hexValue
}

// appendCodePoint appends a \u escape as UTF-8. Adjacent surrogate escapes
// are combined, so a valid surrogate pair produces one rune instead of two
// replacement characters.
func (s *Scanner) appendCodePoint(first int) {
	r := rune(first)
	if utf16.IsSurrogate(r) && s.pos+6 <= s.size && s.text[s.pos] == '\\' && s.text[s.pos+1] == 'u' {
		save := s.pos
		s.pos += 2
		low := s.scanHexDigits(4)
		if low >= 0 && rune(low) >= 0xDC00 && rune(low) <= 0xDFFF {
			s.scratch = utf8.AppendRune(s.scratch, utf16.DecodeRune(r, rune(low)))
			return
		}
		s.pos = save
	}
	s.scratch = utf8.AppendRune(s.scratch, r)
}

func (s *Scanner) scanNumber() string {
	if s.text[s.pos] == '0' {
		s.pos++
	} else {
		s.pos++
		for s.pos < s.size && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
			s.pos++
		}
	}
	if s.pos < s.size && s.text[s.pos] == '.' {
		s.pos++
		if s.pos < s.size && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
			s.pos++
			for s.pos < s.size && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
				s.pos++
			}
		} else {
			s.tokenError = ScanErrorUnexpectedEndOfNumber
			return s.text[s.tokenOffset:s.pos]
		}
	}
	end := s.pos
	if s.pos < s.size && (s.text[s.pos] == 'E' || s.text[s.pos] == 'e') {
		s.pos++
		if s.pos < s.size && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
			s.pos++
		}
		if s.pos < s.size && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
			s.pos++
			for s.pos < s.size && s.text[s.pos] >= '0' && s.text[s.pos] <= '9' {
				s.pos++
			}
			end = s.pos
		} else {
			s.tokenError = ScanErrorUnexpectedEndOfNumber
		}
	}
	return s.text[s.tokenOffset:end]
}

// finishStringValue avoids copying plain strings: they are already a
// substring of the input. Escaped strings are decoded into scratch and only
// converted to a string once.
func (s *Scanner) finishStringValue(start, end int, escaped bool) {
	if escaped {
		s.scratch = append(s.scratch, s.text[start:end]...)
		s.value = string(s.scratch)
	} else {
		s.value = s.text[start:end]
	}
}

func (s *Scanner) scanString() {
	start := s.pos
	s.value = ""
	s.scratch = s.scratch[:0]
	escaped := false
	for s.pos < s.size {
		ch := s.text[s.pos]
		if ch == '"' {
			s.finishStringValue(start, s.pos, escaped)
			s.pos++
			return
		}
		if ch == '\\' {
			s.scratch = append(s.scratch, s.text[start:s.pos]...)
			escaped = true
			s.pos++
			if s.pos >= s.size {
				// The trailing backslash is not part of the value. scratch
				// already holds every decoded char and the raw chunks before
				// this escape; appending text[start:s.pos] would add the
				// earlier prefix again and keep the backslash.
				s.value = string(s.scratch)
				s.tokenError = ScanErrorUnexpectedEndOfString
				return
			}
			ch2 := s.text[s.pos]
			s.pos++
			switch ch2 {
			case '"':
				s.scratch = append(s.scratch, '"')
			case '\\':
				s.scratch = append(s.scratch, '\\')
			case '/':
				s.scratch = append(s.scratch, '/')
			case 'b':
				s.scratch = append(s.scratch, '\b')
			case 'f':
				s.scratch = append(s.scratch, '\f')
			case 'n':
				s.scratch = append(s.scratch, '\n')
			case 'r':
				s.scratch = append(s.scratch, '\r')
			case 't':
				s.scratch = append(s.scratch, '\t')
			case 'u':
				ch3 := s.scanHexDigits(4)
				if ch3 >= 0 {
					s.appendCodePoint(ch3)
				} else {
					s.tokenError = ScanErrorInvalidUnicode
				}
			default:
				s.tokenError = ScanErrorInvalidEscapeCharacter
			}
			start = s.pos
			continue
		}
		r, size := runeAt(s.text, s.pos)
		if r < 0x20 {
			if isLineBreakRune(r) {
				s.finishStringValue(start, s.pos, escaped)
				s.tokenError = ScanErrorUnexpectedEndOfString
				return
			}
			s.tokenError = ScanErrorInvalidCharacter
			// Mark as an error but continue with the string.
		}
		s.pos += size
	}
	s.finishStringValue(start, s.pos, escaped)
	s.tokenError = ScanErrorUnexpectedEndOfString
}

func (s *Scanner) scanNext() SyntaxKind {
	s.value = ""
	s.tokenError = ScanErrorNone
	s.tokenOffset = s.pos

	if s.pos >= s.size {
		s.tokenOffset = s.size
		return s.setToken(SyntaxKindEOF)
	}

	code := s.text[s.pos]
	if n := whitespaceSizeAt(s.text, s.pos); n > 0 {
		start := s.pos
		s.pos += n
		for s.pos < s.size {
			n = whitespaceSizeAt(s.text, s.pos)
			if n == 0 {
				break
			}
			s.pos += n
		}
		s.value = s.text[start:s.pos]
		return s.setToken(SyntaxKindTrivia)
	}
	if r, size := runeAt(s.text, s.pos); isLineBreakRune(r) {
		start := s.pos
		s.pos += size
		if r == '\r' && s.pos < s.size && s.text[s.pos] == '\n' {
			s.pos++
		}
		s.value = s.text[start:s.pos]
		return s.setToken(SyntaxKindLineBreakTrivia)
	}

	switch code {
	case '{':
		s.pos++
		return s.setToken(SyntaxKindOpenBraceToken)
	case '}':
		s.pos++
		return s.setToken(SyntaxKindCloseBraceToken)
	case '[':
		s.pos++
		return s.setToken(SyntaxKindOpenBracketToken)
	case ']':
		s.pos++
		return s.setToken(SyntaxKindCloseBracketToken)
	case ':':
		s.pos++
		return s.setToken(SyntaxKindColonToken)
	case ',':
		s.pos++
		return s.setToken(SyntaxKindCommaToken)
	case '"':
		s.pos++
		s.scanString()
		return s.setToken(SyntaxKindStringLiteral)
	case '/':
		start := s.pos
		if s.pos+1 < s.size && s.text[s.pos+1] == '/' {
			s.pos += 2
			for s.pos < s.size {
				r, size := runeAt(s.text, s.pos)
				if isLineBreakRune(r) {
					break
				}
				s.pos += size
			}
			s.value = s.text[start:s.pos]
			return s.setToken(SyntaxKindLineCommentTrivia)
		}
		if s.pos+1 < s.size && s.text[s.pos+1] == '*' {
			s.pos += 2
			closed := false
			for s.pos+1 < s.size {
				if s.text[s.pos] == '*' && s.text[s.pos+1] == '/' {
					s.pos += 2
					closed = true
					break
				}
				s.pos++
			}
			if !closed {
				s.pos = s.size
				s.tokenError = ScanErrorUnexpectedEndOfComment
			}
			s.value = s.text[start:s.pos]
			return s.setToken(SyntaxKindBlockCommentTrivia)
		}
		s.value = "/"
		s.pos++
		return s.setToken(SyntaxKindUnknown)
	case '-':
		s.value = "-"
		s.pos++
		if s.pos == s.size || s.text[s.pos] < '0' || s.text[s.pos] > '9' {
			return s.setToken(SyntaxKindUnknown)
		}
		fallthrough
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		s.value = s.scanNumber()
		return s.setToken(SyntaxKindNumericLiteral)
	default:
		for s.pos < s.size {
			r, size := runeAt(s.text, s.pos)
			if !isUnknownContentRune(r) {
				break
			}
			s.pos += size
		}
		if s.pos != s.tokenOffset {
			s.value = s.text[s.tokenOffset:s.pos]
			switch s.value {
			case "true":
				return s.setToken(SyntaxKindTrueKeyword)
			case "false":
				return s.setToken(SyntaxKindFalseKeyword)
			case "null":
				return s.setToken(SyntaxKindNullKeyword)
			}
			return s.setToken(SyntaxKindUnknown)
		}
		_, size := runeAt(s.text, s.pos)
		s.value = s.text[s.pos : s.pos+size]
		s.pos += size
		return s.setToken(SyntaxKindUnknown)
	}
}

func (s *Scanner) setToken(token SyntaxKind) SyntaxKind {
	s.token = token
	return token
}

// visitObserver receives the parse events. Returning false stops the visit,
// the Go equivalent of VS Code's private early-return exception in getLocation.
type visitObserver interface {
	onObjectBegin(offset, length int) bool
	onObjectProperty(property string, offset, length int) bool
	onObjectEnd(offset, length int) bool
	onArrayBegin(offset, length int) bool
	onArrayEnd(offset, length int) bool
	onLiteralValue(value any, offset, length int) bool
	onSeparator(character string, offset, length int) bool
	onComment(offset, length int) bool
	onError(code ParseErrorCode, offset, length int) bool
}

// noopObserver supplies the events an observer does not care about.
type noopObserver struct{}

func (noopObserver) onObjectBegin(int, int) bool            { return true }
func (noopObserver) onObjectProperty(string, int, int) bool { return true }
func (noopObserver) onObjectEnd(int, int) bool              { return true }
func (noopObserver) onArrayBegin(int, int) bool             { return true }
func (noopObserver) onArrayEnd(int, int) bool               { return true }
func (noopObserver) onLiteralValue(any, int, int) bool      { return true }
func (noopObserver) onSeparator(string, int, int) bool      { return true }
func (noopObserver) onComment(int, int) bool                { return true }
func (noopObserver) onError(ParseErrorCode, int, int) bool  { return true }

// visitorObserver forwards events to an exported JSONVisitor.
type visitorObserver struct{ visitor JSONVisitor }

func (o *visitorObserver) onObjectBegin(offset, length int) bool {
	if f := o.visitor.OnObjectBegin; f != nil {
		f(offset, length)
	}
	return true
}

func (o *visitorObserver) onObjectProperty(property string, offset, length int) bool {
	if f := o.visitor.OnObjectProperty; f != nil {
		f(property, offset, length)
	}
	return true
}

func (o *visitorObserver) onObjectEnd(offset, length int) bool {
	if f := o.visitor.OnObjectEnd; f != nil {
		f(offset, length)
	}
	return true
}

func (o *visitorObserver) onArrayBegin(offset, length int) bool {
	if f := o.visitor.OnArrayBegin; f != nil {
		f(offset, length)
	}
	return true
}

func (o *visitorObserver) onArrayEnd(offset, length int) bool {
	if f := o.visitor.OnArrayEnd; f != nil {
		f(offset, length)
	}
	return true
}

func (o *visitorObserver) onLiteralValue(value any, offset, length int) bool {
	if f := o.visitor.OnLiteralValue; f != nil {
		f(value, offset, length)
	}
	return true
}

func (o *visitorObserver) onSeparator(character string, offset, length int) bool {
	if f := o.visitor.OnSeparator; f != nil {
		f(character, offset, length)
	}
	return true
}

func (o *visitorObserver) onComment(offset, length int) bool {
	if f := o.visitor.OnComment; f != nil {
		f(offset, length)
	}
	return true
}

func (o *visitorObserver) onError(code ParseErrorCode, offset, length int) bool {
	if f := o.visitor.OnError; f != nil {
		f(code, offset, length)
	}
	return true
}

// parser owns the scanner and the observer for one Visit call.
type parser struct {
	scanner            *Scanner
	observer           visitObserver
	disallowComments   bool
	allowTrailingComma bool
	allowEmptyContent  bool
	aborted            bool
}

func newParser(text string, observer visitObserver, options ParseOptions) *parser {
	if observer == nil {
		observer = noopObserver{}
	}
	return &parser{
		scanner:            CreateScanner(text, false),
		observer:           observer,
		disallowComments:   options.DisallowComments,
		allowTrailingComma: options.AllowTrailingComma,
		allowEmptyContent:  options.AllowEmptyContent,
	}
}

func (p *parser) objectBegin(offset, length int) {
	if !p.observer.onObjectBegin(offset, length) {
		p.aborted = true
	}
}

func (p *parser) objectProperty(property string, offset, length int) {
	if !p.observer.onObjectProperty(property, offset, length) {
		p.aborted = true
	}
}

func (p *parser) objectEnd(offset, length int) {
	if !p.observer.onObjectEnd(offset, length) {
		p.aborted = true
	}
}

func (p *parser) arrayBegin(offset, length int) {
	if !p.observer.onArrayBegin(offset, length) {
		p.aborted = true
	}
}

func (p *parser) arrayEnd(offset, length int) {
	if !p.observer.onArrayEnd(offset, length) {
		p.aborted = true
	}
}

func (p *parser) literalValue(value any, offset, length int) {
	if !p.observer.onLiteralValue(value, offset, length) {
		p.aborted = true
	}
}

func (p *parser) separator(character string, offset, length int) {
	if !p.observer.onSeparator(character, offset, length) {
		p.aborted = true
	}
}

func (p *parser) comment(offset, length int) {
	if !p.observer.onComment(offset, length) {
		p.aborted = true
	}
}

func (p *parser) handleError(code ParseErrorCode, skipUntilAfter, skipUntil []SyntaxKind) {
	if !p.observer.onError(code, p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()) {
		p.aborted = true
		return
	}
	if p.aborted || len(skipUntilAfter)+len(skipUntil) == 0 {
		return
	}
	for token := p.scanner.GetToken(); token != SyntaxKindEOF && !p.aborted; {
		if containsKind(skipUntilAfter, token) {
			p.scanNext()
			break
		}
		if containsKind(skipUntil, token) {
			break
		}
		token = p.scanNext()
	}
}

func containsKind(kinds []SyntaxKind, token SyntaxKind) bool {
	for _, kind := range kinds {
		if kind == token {
			return true
		}
	}
	return false
}

func (p *parser) scanNext() SyntaxKind {
	for !p.aborted {
		token := p.scanner.Scan()
		switch p.scanner.GetTokenError() {
		case ScanErrorInvalidUnicode:
			p.handleError(ParseErrorCodeInvalidUnicode, nil, nil)
		case ScanErrorInvalidEscapeCharacter:
			p.handleError(ParseErrorCodeInvalidEscapeCharacter, nil, nil)
		case ScanErrorUnexpectedEndOfNumber:
			p.handleError(ParseErrorCodeUnexpectedEndOfNumber, nil, nil)
		case ScanErrorUnexpectedEndOfComment:
			if !p.disallowComments {
				p.handleError(ParseErrorCodeUnexpectedEndOfComment, nil, nil)
			}
		case ScanErrorUnexpectedEndOfString:
			p.handleError(ParseErrorCodeUnexpectedEndOfString, nil, nil)
		case ScanErrorInvalidCharacter:
			p.handleError(ParseErrorCodeInvalidCharacter, nil, nil)
		}
		switch token {
		case SyntaxKindLineCommentTrivia, SyntaxKindBlockCommentTrivia:
			if p.disallowComments {
				p.handleError(ParseErrorCodeInvalidCommentToken, nil, nil)
			} else {
				offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
				p.comment(offset, length)
			}
		case SyntaxKindUnknown:
			p.handleError(ParseErrorCodeInvalidSymbol, nil, nil)
		case SyntaxKindTrivia, SyntaxKindLineBreakTrivia:
			continue
		default:
			return token
		}
	}
	return SyntaxKindEOF
}

func (p *parser) parseString(isValue bool) bool {
	value := p.scanner.GetTokenValue()
	offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	if isValue {
		p.literalValue(value, offset, length)
	} else {
		p.objectProperty(value, offset, length)
	}
	if p.aborted {
		return true
	}
	p.scanNext()
	return true
}

// isValidJSONNumber applies the JSON number grammar that VS Code's JSON.parse
// check enforces. strconv.ParseFloat alone is more lenient: it accepts "1.".
func isValidJSONNumber(text string) bool {
	i := 0
	if i < len(text) && text[i] == '-' {
		i++
	}
	if i == len(text) {
		return false
	}
	if text[i] == '0' {
		i++
	} else if text[i] >= '1' && text[i] <= '9' {
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	} else {
		return false
	}
	if i < len(text) && text[i] == '.' {
		i++
		if i == len(text) || text[i] < '0' || text[i] > '9' {
			return false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if i == len(text) || text[i] < '0' || text[i] > '9' {
			return false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	return i == len(text)
}

func (p *parser) parseLiteral() bool {
	offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	switch p.scanner.GetToken() {
	case SyntaxKindNumericLiteral:
		raw := p.scanner.GetTokenValue()
		value, err := strconv.ParseFloat(raw, 64)
		if !isValidJSONNumber(raw) || (err != nil && !errors.Is(err, strconv.ErrRange)) {
			p.handleError(ParseErrorCodeInvalidNumberFormat, nil, nil)
			value = 0
		}
		p.literalValue(value, offset, length)
	case SyntaxKindNullKeyword:
		p.literalValue(nil, offset, length)
	case SyntaxKindTrueKeyword:
		p.literalValue(true, offset, length)
	case SyntaxKindFalseKeyword:
		p.literalValue(false, offset, length)
	default:
		return false
	}
	if p.aborted {
		return true
	}
	p.scanNext()
	return true
}

func (p *parser) parseProperty() bool {
	if p.scanner.GetToken() != SyntaxKindStringLiteral {
		p.handleError(ParseErrorCodePropertyNameExpected, nil, []SyntaxKind{SyntaxKindCloseBraceToken, SyntaxKindCommaToken})
		return false
	}
	p.parseString(false)
	if p.aborted {
		return true
	}
	if p.scanner.GetToken() == SyntaxKindColonToken {
		offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
		p.separator(":", offset, length)
		if p.aborted {
			return true
		}
		p.scanNext()
		if p.aborted {
			return true
		}
		if !p.parseValue() {
			p.handleError(ParseErrorCodeValueExpected, nil, []SyntaxKind{SyntaxKindCloseBraceToken, SyntaxKindCommaToken})
		}
	} else {
		p.handleError(ParseErrorCodeColonExpected, nil, []SyntaxKind{SyntaxKindCloseBraceToken, SyntaxKindCommaToken})
	}
	return true
}

func (p *parser) parseObject() bool {
	offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	p.objectBegin(offset, length)
	if p.aborted {
		return true
	}
	p.scanNext()

	needsComma := false
	for !p.aborted && p.scanner.GetToken() != SyntaxKindCloseBraceToken && p.scanner.GetToken() != SyntaxKindEOF {
		if p.scanner.GetToken() == SyntaxKindCommaToken {
			if !needsComma {
				p.handleError(ParseErrorCodeValueExpected, nil, nil)
			}
			if p.aborted {
				break
			}
			offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
			p.separator(",", offset, length)
			if p.aborted {
				break
			}
			p.scanNext()
			if p.aborted {
				break
			}
			if p.scanner.GetToken() == SyntaxKindCloseBraceToken && p.allowTrailingComma {
				break
			}
		} else if needsComma {
			p.handleError(ParseErrorCodeCommaExpected, nil, nil)
			if p.aborted {
				break
			}
		}
		if !p.parseProperty() {
			p.handleError(ParseErrorCodeValueExpected, nil, []SyntaxKind{SyntaxKindCloseBraceToken, SyntaxKindCommaToken})
		}
		if p.aborted {
			break
		}
		needsComma = true
	}
	if p.aborted {
		return true
	}
	offset, length = p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	p.objectEnd(offset, length)
	if p.aborted {
		return true
	}
	if p.scanner.GetToken() != SyntaxKindCloseBraceToken {
		p.handleError(ParseErrorCodeCloseBraceExpected, []SyntaxKind{SyntaxKindCloseBraceToken}, nil)
	} else {
		p.scanNext()
	}
	return true
}

func (p *parser) parseArray() bool {
	offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	p.arrayBegin(offset, length)
	if p.aborted {
		return true
	}
	p.scanNext()

	needsComma := false
	for !p.aborted && p.scanner.GetToken() != SyntaxKindCloseBracketToken && p.scanner.GetToken() != SyntaxKindEOF {
		if p.scanner.GetToken() == SyntaxKindCommaToken {
			if !needsComma {
				p.handleError(ParseErrorCodeValueExpected, nil, nil)
			}
			if p.aborted {
				break
			}
			offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
			p.separator(",", offset, length)
			if p.aborted {
				break
			}
			p.scanNext()
			if p.aborted {
				break
			}
			if p.scanner.GetToken() == SyntaxKindCloseBracketToken && p.allowTrailingComma {
				break
			}
		} else if needsComma {
			p.handleError(ParseErrorCodeCommaExpected, nil, nil)
			if p.aborted {
				break
			}
		}
		if !p.parseValue() {
			p.handleError(ParseErrorCodeValueExpected, nil, []SyntaxKind{SyntaxKindCloseBracketToken, SyntaxKindCommaToken})
		}
		if p.aborted {
			break
		}
		needsComma = true
	}
	if p.aborted {
		return true
	}
	offset, length = p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	p.arrayEnd(offset, length)
	if p.aborted {
		return true
	}
	if p.scanner.GetToken() != SyntaxKindCloseBracketToken {
		p.handleError(ParseErrorCodeCloseBracketExpected, []SyntaxKind{SyntaxKindCloseBracketToken}, nil)
	} else {
		p.scanNext()
	}
	return true
}

func (p *parser) parseValue() bool {
	if p.aborted {
		return true
	}
	switch p.scanner.GetToken() {
	case SyntaxKindOpenBracketToken:
		return p.parseArray()
	case SyntaxKindOpenBraceToken:
		return p.parseObject()
	case SyntaxKindStringLiteral:
		return p.parseString(true)
	default:
		return p.parseLiteral()
	}
}

func (p *parser) run() {
	p.scanNext()
	if p.aborted {
		return
	}
	if p.scanner.GetToken() == SyntaxKindEOF {
		if !p.allowEmptyContent {
			p.handleError(ParseErrorCodeValueExpected, nil, nil)
		}
		return
	}
	if !p.parseValue() {
		p.handleError(ParseErrorCodeValueExpected, nil, nil)
		return
	}
	if p.aborted {
		return
	}
	if p.scanner.GetToken() != SyntaxKindEOF {
		p.handleError(ParseErrorCodeEndOfFileExpected, nil, nil)
	}
}

// Visit is visit in VS Code's json.ts: it invokes the visitor's callbacks for
// each object, array, property, literal, separator and comment it reaches.
func Visit(text string, visitor JSONVisitor, options ...ParseOptions) {
	newParser(text, &visitorObserver{visitor: visitor}, parseOptions(options)).run()
}

// Parse is parse in VS Code's json.ts. Unlike the JS version it returns the
// errors instead of appending to a caller-owned slice. Decoded values use
// map[string]any for objects, []any for arrays and float64 for numbers.
type parseParent struct {
	array       *[]any
	object      map[string]any
	property    string
	hasProperty bool
}

// parseObserver builds a decoded value. It owns its state instead of using
// per-callback closures.
type parseObserver struct {
	noopObserver
	rootArray       *[]any
	current         parseParent
	previousParents []parseParent
	errs            []ParseError
}

func (o *parseObserver) addValue(value any) {
	if o.current.array != nil {
		*o.current.array = append(*o.current.array, value)
	} else if o.current.object != nil && o.current.hasProperty {
		o.current.object[o.current.property] = value
	}
}

func (o *parseObserver) onObjectBegin(int, int) bool {
	object := make(map[string]any)
	o.addValue(object)
	o.previousParents = append(o.previousParents, o.current)
	o.current = parseParent{object: object}
	return true
}

func (o *parseObserver) onObjectProperty(property string, _, _ int) bool {
	o.current.property = property
	o.current.hasProperty = true
	return true
}

func (o *parseObserver) onObjectEnd(int, int) bool {
	o.current = o.previousParents[len(o.previousParents)-1]
	o.previousParents = o.previousParents[:len(o.previousParents)-1]
	return true
}

func (o *parseObserver) onArrayBegin(int, int) bool {
	array := new([]any)
	o.addValue(array)
	o.previousParents = append(o.previousParents, o.current)
	o.current = parseParent{array: array}
	return true
}

func (o *parseObserver) onArrayEnd(int, int) bool {
	o.current = o.previousParents[len(o.previousParents)-1]
	o.previousParents = o.previousParents[:len(o.previousParents)-1]
	return true
}

func (o *parseObserver) onLiteralValue(value any, _, _ int) bool {
	o.addValue(value)
	return true
}

func (o *parseObserver) onError(code ParseErrorCode, offset, length int) bool {
	o.errs = append(o.errs, ParseError{Error: code, Offset: offset, Length: length})
	return true
}

func Parse(text string, options ...ParseOptions) (any, []ParseError) {
	observer := parseObserver{rootArray: new([]any)}
	observer.current = parseParent{array: observer.rootArray}
	newParser(text, &observer, parseOptions(options)).run()
	if len(*observer.rootArray) == 0 {
		return nil, observer.errs
	}
	return finalizeValue((*observer.rootArray)[0]), observer.errs
}

// finalizeValue unwraps the *[]any placeholders that give JS-style arrays
// reference identity while the visitor builds the result.
func finalizeValue(value any) any {
	switch typed := value.(type) {
	case *[]any:
		result := make([]any, len(*typed))
		for i, item := range *typed {
			result[i] = finalizeValue(item)
		}
		return result
	case map[string]any:
		for key, item := range typed {
			typed[key] = finalizeValue(item)
		}
		return typed
	default:
		return value
	}
}
