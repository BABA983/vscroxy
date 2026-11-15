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

// dartQuote writes s as a Dart double-quoted string. Bytes outside printable
// ASCII use \xHH so UTF-8 and binary bodies keep their exact bytes.
func dartQuote(s string) string {
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
