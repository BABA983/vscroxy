package request

import (
	"fmt"
	"slices"
	"strings"
)

func kotlinSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireASCIIHeaders("Kotlin", headers); err != nil {
		return "", err
	}
	hasBody := req.Body != "" || req.BodyFile != ""
	if hasBody && (method == "GET" || method == "HEAD") {
		return "", fmt.Errorf("OkHttp cannot send a body with %s; copy it as cURL instead", method)
	}
	var b strings.Builder
	b.WriteString("// Kotlin with OkHttp 4.x.\n")
	b.WriteString("import okhttp3.OkHttpClient\n")
	b.WriteString("import okhttp3.Request\n")
	b.WriteString("import okhttp3.RequestBody.Companion.toRequestBody\n\n")
	b.WriteString("fun main() {\n")
	b.WriteString("    val client = OkHttpClient.Builder()\n")
	b.WriteString("        .followRedirects(false)\n")
	b.WriteString("        .followSslRedirects(false)\n")
	b.WriteString("        .build()\n")
	requiresBody := slices.Contains([]string{"POST", "PUT", "PATCH", "PROPPATCH", "REPORT"}, method)
	switch {
	case req.BodyFile != "":
		b.WriteString("    val body = java.io.File(" + kotlinQuote(req.BodyFile) + ").readBytes().toRequestBody(null)\n")
	case req.Body != "":
		b.WriteString("    val body = " + kotlinQuote(req.Body) + ".toByteArray(Charsets.UTF_8).toRequestBody(null)\n")
	case requiresBody:
		b.WriteString("    val body = ByteArray(0).toRequestBody(null)\n")
	}
	b.WriteString("    val request = Request.Builder()\n")
	b.WriteString("        .url(" + kotlinQuote(req.URL) + ")\n")
	if hasBody || requiresBody {
		b.WriteString("        .method(" + kotlinQuote(method) + ", body)\n")
	} else {
		b.WriteString("        .method(" + kotlinQuote(method) + ", null)\n")
	}
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "Kotlin")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			b.WriteString("        .addHeader(" + kotlinQuote(h.name) + ", " + kotlinQuote(value) + ")\n")
		}
	}
	b.WriteString("        .build()\n\n")
	b.WriteString("    client.newCall(request).execute().use { response ->\n")
	b.WriteString("        println(response.code)\n")
	b.WriteString("        println(response.body?.string())\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// kotlinQuote writes s as a Kotlin string literal. $ is escaped so template
// expressions stay literal, and non-ASCII uses \uXXXX or a surrogate pair.
func kotlinQuote(s string) string {
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
		case '\b':
			b.WriteString(`\b`)
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				r -= 0x10000
				fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
