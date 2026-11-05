package request

import (
	"fmt"
	"strings"
)

func rustSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	var b strings.Builder
	b.WriteString("// Cargo.toml: reqwest = { version = \"0.12\", features = [\"blocking\"] }\n")
	b.WriteString("use std::error::Error;\n\n")
	b.WriteString("fn main() -> Result<(), Box<dyn Error>> {\n")
	b.WriteString("    let client = reqwest::blocking::Client::builder()\n")
	b.WriteString("        .redirect(reqwest::redirect::Policy::none())\n")
	b.WriteString("        .build()?;\n")
	if len(headers) > 0 {
		b.WriteString("    let mut headers = reqwest::header::HeaderMap::new();\n")
	} else {
		b.WriteString("    let headers = reqwest::header::HeaderMap::new();\n")
	}
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "Rust")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			b.WriteString("    headers.append(\n")
			b.WriteString("        reqwest::header::HeaderName::from_bytes(" + rustByteString(h.name) + ")?,\n")
			b.WriteString("        reqwest::header::HeaderValue::from_bytes(" + rustByteString(value) + ")?,\n")
			b.WriteString("    );\n")
		}
	}
	b.WriteString("    let response = client\n")
	b.WriteString("        .request(reqwest::Method::from_bytes(" + rustByteString(method) + ")?, " + rustQuote(req.URL) + ")\n")
	b.WriteString("        .headers(headers)\n")
	switch {
	case req.BodyFile != "":
		b.WriteString("        .body(std::fs::read(" + rustQuote(req.BodyFile) + ")?)\n")
	case req.Body != "":
		b.WriteString("        .body(" + rustQuote(req.Body) + ".as_bytes().to_vec())\n")
	}
	b.WriteString("        .send()?;\n")
	b.WriteString("    println!(\"{}\", response.status());\n")
	b.WriteString("    println!(\"{}\", response.text()?);\n")
	b.WriteString("    Ok(())\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// rustQuote writes s as a Rust string literal. Non-ASCII uses \u{...}, so
// the source stays ASCII and .as_bytes() gives the same UTF-8 bytes.
func rustQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r >= 0x20 && r <= 0x7e {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(&b, `\u{%x}`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// rustByteString writes s as a Rust byte-string literal, one \xHH per
// non-printable or non-ASCII byte.
func rustByteString(s string) string {
	var b strings.Builder
	b.WriteString("b\"")
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
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
	b.WriteString("\"")
	return b.String()
}
