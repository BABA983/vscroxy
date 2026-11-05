package request

import (
	"fmt"
	"strings"
)

func javaSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireASCIIHeaders("Java", headers); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("// Java 11+; save as Main.java.\n")
	b.WriteString("import java.net.URI;\n")
	b.WriteString("import java.net.http.HttpClient;\n")
	b.WriteString("import java.net.http.HttpRequest;\n")
	b.WriteString("import java.net.http.HttpResponse;\n")
	b.WriteString("import java.nio.charset.StandardCharsets;\n")
	if req.BodyFile != "" {
		b.WriteString("import java.nio.file.Path;\n")
	}
	b.WriteString("\npublic class Main {\n")
	b.WriteString("    public static void main(String[] args) throws Exception {\n")
	if has(headers, "Host") {
		// java.net.http restricts Host unless it is allowed before the
		// client builds its request.
		b.WriteString("        System.setProperty(\"jdk.httpclient.allowRestrictedHeaders\", \"host\");\n")
	}
	b.WriteString("        HttpClient client = HttpClient.newBuilder()\n")
	b.WriteString("            .followRedirects(HttpClient.Redirect.NEVER)\n")
	b.WriteString("            .build();\n")
	b.WriteString("        HttpRequest.Builder builder = HttpRequest.newBuilder()\n")
	b.WriteString("            .uri(URI.create(" + javaQuote(req.URL) + "))\n")
	b.WriteString("            .method(" + javaQuote(method) + ", ")
	switch {
	case req.BodyFile != "":
		b.WriteString("HttpRequest.BodyPublishers.ofFile(Path.of(" + javaQuote(req.BodyFile) + "))")
	case req.Body != "":
		b.WriteString("HttpRequest.BodyPublishers.ofByteArray(" + javaQuote(req.Body) + ".getBytes(StandardCharsets.UTF_8))")
	default:
		b.WriteString("HttpRequest.BodyPublishers.noBody()")
	}
	b.WriteString(");\n")
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "Java")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			b.WriteString("        builder.header(" + javaQuote(h.name) + ", " + javaQuote(value) + ");\n")
		}
	}
	b.WriteString("        HttpRequest request = builder.build();\n")
	b.WriteString("        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));\n")
	b.WriteString("        System.out.println(response.statusCode());\n")
	b.WriteString("        System.out.println(response.body());\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// javaQuote writes s as a Java string literal. The source stays ASCII;
// non-ASCII uses \uXXXX or a UTF-16 surrogate pair.
func javaQuote(s string) string {
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
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
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
