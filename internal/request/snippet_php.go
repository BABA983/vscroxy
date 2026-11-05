package request

import (
	"fmt"
	"strings"
)

func phpSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	hasBody := req.Body != "" || req.BodyFile != ""
	if method == "HEAD" && hasBody {
		return "", fmt.Errorf("PHP cURL cannot send a body with HEAD; copy it as cURL instead")
	}
	var b strings.Builder
	b.WriteString("<?php\n")
	if req.BodyFile != "" {
		b.WriteString("$body = file_get_contents(" + phpQuote(req.BodyFile) + ");\n")
		b.WriteString("if ($body === false) {\n")
		b.WriteString("    fwrite(STDERR, \"cannot read request body\\n\");\n")
		b.WriteString("    exit(1);\n")
		b.WriteString("}\n")
	}
	b.WriteString("$ch = curl_init(" + phpQuote(req.URL) + ");\n")
	var options []string
	options = append(options, "CURLOPT_CUSTOMREQUEST => "+phpQuote(method))
	options = append(options, "CURLOPT_RETURNTRANSFER => true")
	options = append(options, "CURLOPT_FOLLOWLOCATION => false")
	if method == "HEAD" {
		options = append(options, "CURLOPT_NOBODY => true")
	}
	var lines []string
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "PHP")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			if value == "" {
				// libcurl reads a header line without a colon and with a
				// trailing semicolon as an empty header value.
				lines = append(lines, phpQuote(h.name+";"))
			} else {
				lines = append(lines, phpQuote(h.name+": "+value))
			}
		}
	}
	if hasBody && !has(headers, "Content-Type") {
		// POSTFIELDS makes cURL add a form Content-Type; an empty header
		// removes that default so the request keeps its authored headers.
		lines = append(lines, phpQuote("Content-Type:"))
	}
	if len(lines) > 0 {
		var header strings.Builder
		header.WriteString("CURLOPT_HTTPHEADER => [\n")
		for _, line := range lines {
			header.WriteString("        " + line + ",\n")
		}
		header.WriteString("    ]")
		options = append(options, header.String())
	}
	switch {
	case req.BodyFile != "":
		options = append(options, "CURLOPT_POSTFIELDS => $body")
	case req.Body != "":
		options = append(options, "CURLOPT_POSTFIELDS => "+phpQuote(req.Body))
	}
	b.WriteString("curl_setopt_array($ch, [\n")
	for _, option := range options {
		b.WriteString("    " + option + ",\n")
	}
	b.WriteString("]);\n")
	b.WriteString("$response = curl_exec($ch);\n")
	b.WriteString("if ($response === false) {\n")
	b.WriteString("    fwrite(STDERR, curl_error($ch) . \"\\n\");\n")
	b.WriteString("    exit(1);\n")
	b.WriteString("}\n")
	b.WriteString("$status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);\n")
	b.WriteString("curl_close($ch);\n")
	b.WriteString("echo $status . \"\\n\" . $response . \"\\n\";\n")
	return b.String(), nil
}

// phpQuote writes s as a PHP double-quoted string. Bytes outside printable
// ASCII use \xHH, so binary bodies and UTF-8 keep their exact bytes.
func phpQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c >= 0x20 && c <= 0x7e {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
