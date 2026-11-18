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
// in the input text. It reuses one buffer for token values; GetTokenValue
// returns a copy, so keeping that string is safe after the next scan.
type Scanner struct {
	text         string
	pos          int
	size         int
	value        []byte
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
	s.value = s.value[:0]
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
func (s *Scanner) GetTokenValue() string { return string(s.value) }

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
			s.value = utf8.AppendRune(s.value, utf16.DecodeRune(r, rune(low)))
			return
		}
		s.pos = save
	}
	s.value = utf8.AppendRune(s.value, r)
}

func (s *Scanner) scanNumber() string {
	start := s.pos
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
			return s.text[start:s.pos]
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
	return s.text[start:end]
}

func (s *Scanner) scanString() {
	start := s.pos
	s.value = s.value[:0]
	for s.pos < s.size {
		ch := s.text[s.pos]
		if ch == '"' {
			s.value = append(s.value, s.text[start:s.pos]...)
			s.pos++
			return
		}
		if ch == '\\' {
			s.value = append(s.value, s.text[start:s.pos]...)
			s.pos++
			if s.pos >= s.size {
				s.tokenError = ScanErrorUnexpectedEndOfString
				return
			}
			ch2 := s.text[s.pos]
			s.pos++
			switch ch2 {
			case '"':
				s.value = append(s.value, '"')
			case '\\':
				s.value = append(s.value, '\\')
			case '/':
				s.value = append(s.value, '/')
			case 'b':
				s.value = append(s.value, '\b')
			case 'f':
				s.value = append(s.value, '\f')
			case 'n':
				s.value = append(s.value, '\n')
			case 'r':
				s.value = append(s.value, '\r')
			case 't':
				s.value = append(s.value, '\t')
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
			s.value = append(s.value, s.text[start:s.pos]...)
			if isLineBreakRune(r) {
				s.tokenError = ScanErrorUnexpectedEndOfString
				return
			}
			s.tokenError = ScanErrorInvalidCharacter
			// Mark as an error but continue with the string.
		}
		s.pos += size
	}
	s.value = append(s.value, s.text[start:s.pos]...)
	s.tokenError = ScanErrorUnexpectedEndOfString
}

func (s *Scanner) scanNext() SyntaxKind {
	s.value = s.value[:0]
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
		s.value = append(s.value, s.text[start:s.pos]...)
		return s.setToken(SyntaxKindTrivia)
	}
	if r, size := runeAt(s.text, s.pos); isLineBreakRune(r) {
		start := s.pos
		s.pos += size
		s.value = append(s.value, s.text[start:s.pos]...)
		if r == '\r' && s.pos < s.size && s.text[s.pos] == '\n' {
			s.pos++
			s.value = append(s.value[:0], '\n')
		}
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
			s.value = append(s.value, s.text[start:s.pos]...)
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
			s.value = append(s.value, s.text[start:s.pos]...)
			return s.setToken(SyntaxKindBlockCommentTrivia)
		}
		s.value = append(s.value, '/')
		s.pos++
		return s.setToken(SyntaxKindUnknown)
	case '-':
		s.value = append(s.value, '-')
		s.pos++
		if s.pos == s.size || s.text[s.pos] < '0' || s.text[s.pos] > '9' {
			return s.setToken(SyntaxKindUnknown)
		}
		fallthrough
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		s.value = append(s.value, s.scanNumber()...)
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
			s.value = append(s.value, s.text[s.tokenOffset:s.pos]...)
			switch string(s.value) {
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
		s.value = append(s.value, s.text[s.pos:s.pos+size]...)
		s.pos += size
		return s.setToken(SyntaxKindUnknown)
	}
}

func (s *Scanner) setToken(token SyntaxKind) SyntaxKind {
	s.token = token
	return token
}

type visitorHooks struct {
	onObjectBegin    func(offset, length int) bool
	onObjectProperty func(property string, offset, length int) bool
	onObjectEnd      func(offset, length int) bool
	onArrayBegin     func(offset, length int) bool
	onArrayEnd       func(offset, length int) bool
	onLiteralValue   func(value any, offset, length int) bool
	onSeparator      func(character string, offset, length int) bool
	onComment        func(offset, length int) bool
	onError          func(error ParseErrorCode, offset, length int) bool
}

// parser owns the scanner and the hooks for one Visit call. Hook callbacks
// return false to stop the visit, the Go equivalent of VS Code's private
// early-return exception in getLocation.
type parser struct {
	scanner            *Scanner
	hooks              visitorHooks
	disallowComments   bool
	allowTrailingComma bool
	allowEmptyContent  bool
	aborted            bool
}

func newParser(text string, hooks visitorHooks, options ParseOptions) *parser {
	return &parser{
		scanner:            CreateScanner(text, false),
		hooks:              hooks,
		disallowComments:   options.DisallowComments,
		allowTrailingComma: options.AllowTrailingComma,
		allowEmptyContent:  options.AllowEmptyContent,
	}
}

func (p *parser) objectBegin(offset, length int) {
	if h := p.hooks.onObjectBegin; h != nil && !h(offset, length) {
		p.aborted = true
	}
}

func (p *parser) objectProperty(property string, offset, length int) {
	if h := p.hooks.onObjectProperty; h != nil && !h(property, offset, length) {
		p.aborted = true
	}
}

func (p *parser) objectEnd(offset, length int) {
	if h := p.hooks.onObjectEnd; h != nil && !h(offset, length) {
		p.aborted = true
	}
}

func (p *parser) arrayBegin(offset, length int) {
	if h := p.hooks.onArrayBegin; h != nil && !h(offset, length) {
		p.aborted = true
	}
}

func (p *parser) arrayEnd(offset, length int) {
	if h := p.hooks.onArrayEnd; h != nil && !h(offset, length) {
		p.aborted = true
	}
}

func (p *parser) literalValue(value any, offset, length int) {
	if h := p.hooks.onLiteralValue; h != nil && !h(value, offset, length) {
		p.aborted = true
	}
}

func (p *parser) separator(character string, offset, length int) {
	if h := p.hooks.onSeparator; h != nil && !h(character, offset, length) {
		p.aborted = true
	}
}

func (p *parser) comment(offset, length int) {
	if h := p.hooks.onComment; h != nil && !h(offset, length) {
		p.aborted = true
	}
}

func (p *parser) handleError(code ParseErrorCode, skipUntilAfter, skipUntil []SyntaxKind) {
	if h := p.hooks.onError; h != nil && !h(code, p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()) {
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

func (p *parser) parseLiteral() bool {
	offset, length := p.scanner.GetTokenOffset(), p.scanner.GetTokenLength()
	switch p.scanner.GetToken() {
	case SyntaxKindNumericLiteral:
		value, err := strconv.ParseFloat(p.scanner.GetTokenValue(), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
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
	hooks := visitorHooks{}
	if visitor.OnObjectBegin != nil {
		hooks.onObjectBegin = func(offset, length int) bool {
			visitor.OnObjectBegin(offset, length)
			return true
		}
	}
	if visitor.OnObjectProperty != nil {
		hooks.onObjectProperty = func(property string, offset, length int) bool {
			visitor.OnObjectProperty(property, offset, length)
			return true
		}
	}
	if visitor.OnObjectEnd != nil {
		hooks.onObjectEnd = func(offset, length int) bool {
			visitor.OnObjectEnd(offset, length)
			return true
		}
	}
	if visitor.OnArrayBegin != nil {
		hooks.onArrayBegin = func(offset, length int) bool {
			visitor.OnArrayBegin(offset, length)
			return true
		}
	}
	if visitor.OnArrayEnd != nil {
		hooks.onArrayEnd = func(offset, length int) bool {
			visitor.OnArrayEnd(offset, length)
			return true
		}
	}
	if visitor.OnLiteralValue != nil {
		hooks.onLiteralValue = func(value any, offset, length int) bool {
			visitor.OnLiteralValue(value, offset, length)
			return true
		}
	}
	if visitor.OnSeparator != nil {
		hooks.onSeparator = func(character string, offset, length int) bool {
			visitor.OnSeparator(character, offset, length)
			return true
		}
	}
	if visitor.OnComment != nil {
		hooks.onComment = func(offset, length int) bool {
			visitor.OnComment(offset, length)
			return true
		}
	}
	if visitor.OnError != nil {
		hooks.onError = func(code ParseErrorCode, offset, length int) bool {
			visitor.OnError(code, offset, length)
			return true
		}
	}
	newParser(text, hooks, parseOptions(options)).run()
}

// Parse is parse in VS Code's json.ts. Unlike the JS version it returns the
// errors instead of appending to a caller-owned slice. Decoded values use
// map[string]any for objects, []any for arrays and float64 for numbers.
func Parse(text string, options ...ParseOptions) (any, []ParseError) {
	opts := parseOptions(options)
	type parent struct {
		array       *[]any
		object      map[string]any
		property    string
		hasProperty bool
	}
	rootArray := new([]any)
	current := parent{array: rootArray}
	previousParents := make([]parent, 0)
	errs := make([]ParseError, 0)

	onValue := func(value any) {
		if current.array != nil {
			*current.array = append(*current.array, value)
		} else if current.object != nil && current.hasProperty {
			current.object[current.property] = value
		}
	}
	hooks := visitorHooks{
		onObjectBegin: func(_, _ int) bool {
			object := make(map[string]any)
			onValue(object)
			previousParents = append(previousParents, current)
			current = parent{object: object}
			return true
		},
		onObjectProperty: func(property string, _, _ int) bool {
			current.property = property
			current.hasProperty = true
			return true
		},
		onObjectEnd: func(_, _ int) bool {
			current = previousParents[len(previousParents)-1]
			previousParents = previousParents[:len(previousParents)-1]
			return true
		},
		onArrayBegin: func(_, _ int) bool {
			array := new([]any)
			onValue(array)
			previousParents = append(previousParents, current)
			current = parent{array: array}
			return true
		},
		onArrayEnd: func(_, _ int) bool {
			current = previousParents[len(previousParents)-1]
			previousParents = previousParents[:len(previousParents)-1]
			return true
		},
		onLiteralValue: func(value any, _, _ int) bool {
			onValue(value)
			return true
		},
		onError: func(code ParseErrorCode, offset, length int) bool {
			errs = append(errs, ParseError{Error: code, Offset: offset, Length: length})
			return true
		},
	}
	newParser(text, hooks, opts).run()
	if len(*rootArray) == 0 {
		return nil, errs
	}
	return finalizeValue((*rootArray)[0]), errs
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
