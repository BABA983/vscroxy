package inspect

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Decode interprets a small, explicitly selected value, never changing the
// exchange it came from. JWT decoding deliberately performs no verification.
func Decode(kind, input string) (string, error) {
	if len(input) > 64<<10 {
		return "", errors.New("select at most 64 KiB to decode")
	}
	input = strings.TrimSpace(input)
	switch kind {
	case "Base64":
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b, err := encoding.Strict().DecodeString(input)
			if err == nil && encoding.EncodeToString(b) == input {
				if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
					return fmt.Sprintf("Binary · %d bytes\n\n%s", len(b), hex.Dump(b)), nil
				}
				return string(b), nil
			}
		}
		return "", errors.New("invalid Base64; use standard or URL-safe encoding")
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
