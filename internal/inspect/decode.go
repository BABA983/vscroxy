package inspect

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DecodedValue is a selected value's decoded form. Text is the result for
// textual decoders, Fields a key-value table, and Image the bytes of a
// Base64 picture with the media type detected from them.
type DecodedValue struct {
	Text      string
	Fields    []FormField
	Image     []byte
	MediaType string
}

// DecodeValue decodes a selection into the shape the decoder shows: a table
// for key-value pairs, a picture for Base64 images, text otherwise.
func DecodeValue(kind, input string) (DecodedValue, error) {
	if len(input) > 64<<10 {
		return DecodedValue{}, errors.New("select at most 64 KiB to decode")
	}
	trimmed := strings.TrimSpace(input)
	switch kind {
	case "JSON":
		value := jsontext.Value(trimmed)
		if value.Kind() != '{' && value.Kind() != '[' {
			return DecodedValue{}, errors.New("JSON decoding needs an object or array")
		}
		if err := value.Indent(jsontext.WithIndent("  ")); err != nil {
			return DecodedValue{}, errors.New("malformed JSON")
		}
		return DecodedValue{Text: string(value)}, nil
	case "XML":
		text, err := FormatXML(trimmed)
		return DecodedValue{Text: text}, err
	case "Key-value":
		fields := parseKeyValues(trimmed)
		if len(fields) == 0 {
			return DecodedValue{}, errors.New("no key-value pairs found; use a=b&c=d, one per line, or key: value")
		}
		return DecodedValue{Fields: fields}, nil
	case "Base64 image":
		image, ok := decodeBase64(trimmed)
		if !ok {
			return DecodedValue{}, errors.New("invalid Base64; use standard or URL-safe encoding")
		}
		mediaType := http.DetectContentType(image)
		switch mediaType {
		case "image/png", "image/jpeg", "image/gif", "image/bmp", "image/webp":
			return DecodedValue{Image: image, MediaType: mediaType}, nil
		}
		return DecodedValue{}, fmt.Errorf("decoded bytes are %s, not a supported image", mediaType)
	}
	text, err := Decode(kind, input)
	return DecodedValue{Text: text}, err
}

// FormatXML pretty-prints an XML fragment for the decoder. The body
// inspector's own formatter keeps namespaces, attributes and text intact.
func FormatXML(input string) (string, error) {
	return formatBodyXML([]byte(input))
}

func parseKeyValues(input string) []FormField {
	var fields []FormField
	if strings.Contains(input, "&") && strings.Contains(input, "=") {
		if parsed, err := formatBodyForm([]byte(input)); err == nil && len(parsed) > 0 {
			return parsed
		}
	}
	for line := range strings.SplitSeq(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sep := strings.IndexAny(line, "=:")
		if sep <= 0 {
			continue
		}
		key, value := strings.TrimSpace(line[:sep]), strings.TrimSpace(line[sep+1:])
		if key == "" {
			continue
		}
		fields = append(fields, FormField{RawKey: key, RawValue: value, Key: key, Value: value, HasEquals: line[sep] == '='})
	}
	return fields
}

func decodeBase64(input string) ([]byte, bool) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := encoding.Strict().DecodeString(input)
		if err == nil && encoding.EncodeToString(b) == input {
			return b, true
		}
	}
	return nil, false
}

// Decode interprets a small, explicitly selected value, never changing the
// exchange it came from. JWT decoding deliberately performs no verification.
func Decode(kind, input string) (string, error) {
	if len(input) > 64<<10 {
		return "", errors.New("select at most 64 KiB to decode")
	}
	input = strings.TrimSpace(input)
	switch kind {
	case "Base64":
		b, ok := decodeBase64(input)
		if !ok {
			return "", errors.New("invalid Base64; use standard or URL-safe encoding")
		}
		if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
			return fmt.Sprintf("Binary · %d bytes\n\n%s", len(b), hex.Dump(b)), nil
		}
		return string(b), nil
	case "URL component", "URL query":
		decode := url.PathUnescape
		if kind == "URL query" {
			decode = url.QueryUnescape
		}
		result, err := decode(input)
		if err != nil || !utf8.ValidString(result) || strings.ContainsRune(result, 0) {
			return "", errors.New("invalid UTF-8 URL encoding")
		}
		return result, nil
	case "JWT":
		parts := strings.Split(input, ".")
		if len(parts) != 3 {
			return "", errors.New("JWT requires three dot-separated segments")
		}
		var values []string
		for _, part := range parts[:2] {
			b, err := base64.RawURLEncoding.Strict().DecodeString(part)
			v := jsontext.Value(b)
			if err != nil || base64.RawURLEncoding.EncodeToString(b) != part || v.Kind() != '{' || v.Indent(jsontext.WithIndent("  ")) != nil {
				return "", errors.New("JWT header and payload must be Base64URL JSON objects")
			}
			values = append(values, string(v))
		}
		if _, err := base64.RawURLEncoding.Strict().DecodeString(parts[2]); err != nil {
			return "", errors.New("invalid JWT signature encoding")
		}
		return "Unverified JWT · signature and claims are not validated\n\nHeader\n" + values[0] + "\n\nPayload\n" + values[1], nil
	case "Unix seconds", "Unix milliseconds":
		n, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return "", errors.New("enter an integer Unix timestamp")
		}
		stamp := time.Unix(n, 0)
		if kind == "Unix milliseconds" {
			stamp = time.UnixMilli(n)
		}
		stamp = stamp.UTC()
		if stamp.Year() < 1 || stamp.Year() > 9999 {
			return "", errors.New("timestamp must fall within years 0001–9999")
		}
		return stamp.Format(time.RFC3339Nano), nil
	default:
		return "", errors.New("choose a supported decoder")
	}
}
