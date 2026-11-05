package request

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// snippetReadsFile reports whether a snippet language can send a Binary
// body by reading its source file instead of inlining the bytes.
func snippetReadsFile(language string) bool {
	switch language {
	case "Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)",
		"C# (HttpClient)", "PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)":
		return true
	}
	return false
}

// snippetHeaderValues is a header's values as a language sends them. Cookie
// values are joined with "; " because HTTP defines that combination; other
// repeated headers keep one value per line.
func snippetHeaderValues(h snippetHeader, language string) ([]string, error) {
	if len(h.values) > 1 && strings.EqualFold(h.name, "Cookie") {
		value, err := joined(h, language)
		if err != nil {
			return nil, err
		}
		return []string{value}, nil
	}
	// HttpClient and Net::HTTP write repeated values as one comma-separated
	// line. Set-Cookie is the one header HTTP does not allow to combine.
	if len(h.values) > 1 && strings.EqualFold(h.name, "Set-Cookie") && (language == "C#" || language == "Ruby") {
		return nil, fmt.Errorf("%s cannot repeat the Set-Cookie header; copy it as cURL instead", language)
	}
	return h.values, nil
}

// requireUTF8Headers rejects header values a language's string type cannot
// carry without replacing bytes. PHP, Ruby and Rust write byte strings and
// do not need this check.
func requireUTF8Headers(language string, headers []snippetHeader) error {
	for _, h := range headers {
		for _, value := range h.values {
			if !utf8.ValidString(value) {
				return fmt.Errorf("%s cannot send a header value that is not UTF-8; copy it as cURL instead", language)
			}
		}
	}
	return nil
}

// requireASCIIHeaders rejects header values that Java, OkHttp and HttpClient
// refuse or replace at send time. Their HTTP stacks only write ASCII header
// values; a non-ASCII value would fail after the user runs the code.
func requireASCIIHeaders(language string, headers []snippetHeader) error {
	for _, h := range headers {
		for _, value := range h.values {
			for _, r := range value {
				if r > 0x7e {
					return fmt.Errorf("%s cannot send a non-ASCII header value; copy it as cURL instead", language)
				}
			}
		}
	}
	return nil
}
