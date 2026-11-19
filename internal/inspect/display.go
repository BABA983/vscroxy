package inspect

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
)

// Display limits follow the body view: it keeps at most 8 MiB, and no
// decoded form may be larger than three times the captured bytes (the widest
// single-byte encoding expands a byte to a three-byte UTF-8 rune).
const (
	utf16BEDetectionBOM     = "\xfe\xff"
	utf16LEDetectionBOM     = "\xff\xfe"
	utf8DetectionBOM        = "\xef\xbb\xbf"
	displayBodyLimit        = 8 << 20
	decodedDisplayLimit     = 3 * displayBodyLimit
	zeroByteDetectionMaxLen = 512
	htmlCharsetScanMaxLen   = 4096
)

// DisplayText returns the text form of a body for a read-only viewer. BOM,
// Content-Type charset, an HTML charset declaration, and VS Code's UTF-16
// zero-byte heuristic are consulted in that order; the first signal wins.
// There is no statistical guess: without a signal the captured bytes are left
// alone.
// ok is false for plain UTF-8, an unsupported label, a binary body and an
// oversized body. Callers then keep today's behavior (valid UTF-8 as text,
// everything else as hex). text and encoding are only meaningful when ok is
// true.
func DisplayText(contentType string, body []byte) (text, encoding string, ok bool) {
	if len(body) == 0 || len(body) > displayBodyLimit || BinaryContentType(contentType) {
		return "", "", false
	}
	enc, name, guessed := detectEncoding(contentType, body)
	if enc == nil || name == "" || name == "UTF-8" {
		return "", "", false
	}
	decoded, err := decodeDisplay(enc, body)
	if err != nil || guessed && hasBinaryControls(decoded) {
		return "", "", false
	}
	return decoded, name, true
}

var binaryMediaTypes = []string{
	"application/octet-stream", "application/x-binary", "application/pdf",
	"application/zip", "application/x-zip-compressed", "application/gzip",
	"application/x-gzip", "application/x-tar", "application/x-bzip2",
	"application/x-7z-compressed", "application/vnd.rar", "application/java-archive",
	"application/wasm", "application/sqlite3",
}

// BinaryContentType is the viewer's single classification of bodies that are
// known to be binary. DisplayText refuses all four detection steps for them,
// and the body view uses the same rule when it picks Hex.
func BinaryContentType(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		media, _, _ = strings.Cut(contentType, ";")
		media = strings.ToLower(strings.TrimSpace(media))
	}
	switch {
	case strings.HasPrefix(media, "image/"), strings.HasPrefix(media, "audio/"),
		strings.HasPrefix(media, "video/"), strings.HasPrefix(media, "font/"),
		strings.Contains(media, "protobuf"), strings.HasPrefix(media, "application/grpc"),
		strings.HasSuffix(media, "+zip"), strings.HasSuffix(media, "+gzip"),
		strings.HasSuffix(media, "+protobuf"), strings.Contains(media, "binary"):
		return true
	case slices.Contains(binaryMediaTypes, media):
		return true
	}
	return BodyFormat(contentType) == "MessagePack"
}

// hasBinaryControls rejects a guess based only on alternating NULs when the
// decoded bytes contain C0 controls. Real text may keep tab and line breaks,
// but a uint16 array must stay in Hex. An explicit BOM, charset or <meta>
// declaration is a signal from the sender and is not second-guessed here.
func hasBinaryControls(text string) bool {
	for _, r := range text {
		if r < 0x20 && r != '\t' && r != '\r' && r != '\n' {
			return true
		}
	}
	return false
}

// detectEncoding ports VS Code's textfile encoding order. An explicit BOM
// takes precedence over every declared charset; only then can the header or
// the document's own declaration choose an encoding, with the UTF-16 zero-byte
// check last.
func detectEncoding(contentType string, body []byte) (encoding.Encoding, string, bool) {
	if enc, name := encodingFromBOM(body); enc != nil {
		return enc, name, false
	}
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		media, params = "", nil
	}
	if label := params["charset"]; label != "" {
		if enc, name := lookupCharset(label); enc != nil {
			return enc, name, false
		}
	}
	if media == "text/html" || media == "application/xhtml+xml" {
		if label := charsetFromHTML(body[:min(len(body), htmlCharsetScanMaxLen)]); label != "" {
			if enc, name := lookupCharset(label); enc != nil {
				return enc, name, false
			}
		}
	}
	if enc, name := encodingFromZeroBytes(body); enc != nil {
		return enc, name, true
	}
	return nil, "", false
}

// encodingFromBOM reads the three BOMs VS Code checks, with UTF-16 before
// UTF-8 because its two-byte prefixes are shorter.
func encodingFromBOM(body []byte) (encoding.Encoding, string) {
	if len(body) >= 2 {
		switch {
		case bytes.HasPrefix(body, []byte(utf16BEDetectionBOM)):
			return unicode.UTF16(unicode.BigEndian, unicode.UseBOM), "UTF-16BE"
		case bytes.HasPrefix(body, []byte(utf16LEDetectionBOM)):
			return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), "UTF-16LE"
		}
	}
	if bytes.HasPrefix(body, []byte(utf8DetectionBOM)) {
		return unicode.UTF8BOM, "UTF-8 BOM"
	}
	return nil, ""
}

// lookupCharset resolves a charset label through WHATWG's HTML index first,
// then the IANA registry. The name is the canonical one for display, not the
// label from the wire.
func lookupCharset(label string) (encoding.Encoding, string) {
	label = strings.Trim(strings.TrimSpace(label), "\"'")
	enc, err := htmlindex.Get(label)
	if err != nil || enc == nil {
		enc, err = ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, ""
		}
	}
	if name, err := ianaindex.MIME.Name(enc); err == nil && name != "" {
		return enc, name
	}
	name, err := htmlindex.Name(enc)
	if err != nil || name == "" {
		return nil, ""
	}
	return enc, strings.ToUpper(name)
}

var (
	metaTagPattern     = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	charsetAttrPattern = regexp.MustCompile(`(?is)\bcharset\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s;>]*))`)
	contentTypePattern = regexp.MustCompile(`(?is)\bhttp-equiv\s*=\s*(?:"content-type"|'content-type'|content-type\b)`)
)

// charsetFromHTML looks for a charset in a <meta> tag within the first 4 KiB.
// NULs are dropped so a UTF-16 page without a BOM still exposes its ASCII
// markup; that does not change where the 4 KiB window ends in the wire bytes.
func charsetFromHTML(head []byte) string {
	ascii := bytes.ReplaceAll(head, []byte{0}, nil)
	for _, tag := range metaTagPattern.FindAll(ascii, -1) {
		if contentTypePattern.Match(tag) {
			if match := charsetAttrPattern.FindSubmatch(tag); match != nil {
				return cleanCharset(match)
			}
		} else if match := charsetAttrPattern.FindSubmatch(tag); match != nil {
			return cleanCharset(match)
		}
	}
	return ""
}

func cleanCharset(match [][]byte) string {
	for _, part := range match[1:] {
		if label := strings.Trim(string(part), " \t\r\n\"';>"); label != "" {
			return label
		}
	}
	return ""
}

// encodingFromZeroBytes is VS Code's simplified UTF-16 check over the first
// 512 bytes: ASCII in UTF-16LE puts NUL at odd indices, UTF-16BE at even ones.
// NULs elsewhere mean the bytes are binary, not a legacy text encoding.
func encodingFromZeroBytes(body []byte) (encoding.Encoding, string) {
	couldBeLE, couldBeBE, containsZero := true, true, false
	for i := range min(len(body), zeroByteDetectionMaxLen) {
		isEndian, isZero := i%2 == 1, body[i] == 0
		if isZero {
			containsZero = true
		}
		if couldBeLE && (isEndian && !isZero || !isEndian && isZero) {
			couldBeLE = false
		}
		if couldBeBE && (isEndian && isZero || !isEndian && !isZero) {
			couldBeBE = false
		}
		if isZero && !couldBeLE && !couldBeBE {
			break
		}
	}
	if !containsZero {
		return nil, ""
	}
	if couldBeLE {
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), "UTF-16LE"
	}
	if couldBeBE {
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), "UTF-16BE"
	}
	return nil, ""
}

// decodeDisplay bounds both sides of the conversion: DisplayText refuses an
// oversized body, and the reader stops one byte past the largest text a
// supported encoding can produce. Invalid input is replaced by U+FFFD by
// x/text, as it is in the browser.
func decodeDisplay(enc encoding.Encoding, body []byte) (string, error) {
	reader := io.LimitReader(enc.NewDecoder().Reader(bytes.NewReader(body)), decodedDisplayLimit+1)
	decoded, err := io.ReadAll(reader)
	if err != nil || len(decoded) > decodedDisplayLimit || !utf8.Valid(decoded) {
		return "", errors.New("display text exceeds the decoding limit")
	}
	return string(decoded), nil
}
