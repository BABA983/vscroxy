package request

import (
	"fmt"
	"strings"
)

// dartSnippet writes a package:http request. Dart's header map holds one
// value per name, so repeated headers are combined with commas.
func dartSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireUTF8Headers("Dart", headers); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("// Dart 3+ with package:http.\n")
	b.WriteString("import 'dart:convert';\n")
	if req.BodyFile != "" {
		b.WriteString("import 'dart:io';\n")
	}
	b.WriteString("import 'package:http/http.dart' as http;\n\n")
	b.WriteString("Future<void> main() async {\n")
	b.WriteString("  final request = http.Request(" + dartQuote(method) + ", Uri.parse(" + dartQuote(req.URL) + "));\n")
	b.WriteString("  request.followRedirects = false;\n")
	for _, h := range headers {
		if strings.EqualFold(h.name, "Host") {
			return "", fmt.Errorf("Dart http cannot set the Host header; copy it as cURL instead")
		}
		value, err := joined(h, "Dart http")
		if err != nil {
			return "", err
		}
		b.WriteString("  request.headers[" + dartQuote(h.name) + "] = " + dartQuote(value) + ";\n")
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("  request.bodyBytes = await File(" + dartQuote(req.BodyFile) + ").readAsBytes();\n")
	case req.Body != "":
		b.WriteString("  request.bodyBytes = utf8.encode(" + dartQuote(req.Body) + ");\n")
	}
	b.WriteString("  final response = await http.Response.fromStream(await http.Client().send(request));\n")
	b.WriteString("  print('${response.statusCode} ${response.body}');\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// dartQuote writes s as a Dart double-quoted string. It walks runes, because
// Dart's \xHH is a code point, not a byte: writing a UTF-8 byte that way would
// turn é into Ã©. Printable non-ASCII stays as UTF-8 source; control runes use
// \u{...}.
func dartQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
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
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u{%x}`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
