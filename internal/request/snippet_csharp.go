package request

import (
	"fmt"
	"net/http"
	"strings"
)

// csharpContentHeaders are sent on HttpContent rather than HttpRequestMessage
// in .NET.
var csharpContentHeaders = map[string]bool{
	"Allow":               true,
	"Content-Disposition": true,
	"Content-Encoding":    true,
	"Content-Language":    true,
	"Content-Location":    true,
	"Content-Md5":         true,
	"Content-Range":       true,
	"Content-Type":        true,
	"Expires":             true,
	"Last-Modified":       true,
}

func csharpSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireASCIIHeaders("C#", headers); err != nil {
		return "", err
	}
	hasBody := req.Body != "" || req.BodyFile != ""
	hasContent := hasBody
	for _, h := range headers {
		if csharpContentHeaders[http.CanonicalHeaderKey(h.name)] {
			hasContent = true
		}
	}
	var b strings.Builder
	b.WriteString("// C# with .NET 6+.\n")
	b.WriteString("using System;\n")
	if req.BodyFile != "" {
		b.WriteString("using System.IO;\n")
	}
	b.WriteString("using System.Net.Http;\n")
	if req.Body != "" && req.BodyFile == "" {
		b.WriteString("using System.Text;\n")
	}
	b.WriteString("using System.Threading.Tasks;\n\n")
	b.WriteString("class Program\n{\n")
	b.WriteString("    static async Task Main()\n    {\n")
	b.WriteString("        using var client = new HttpClient(new HttpClientHandler { AllowAutoRedirect = false });\n")
	b.WriteString("        using var request = new HttpRequestMessage(new HttpMethod(" + csharpQuote(method) + "), " + csharpQuote(req.URL) + ");\n")
	if hasContent {
		switch {
		case req.BodyFile != "":
			b.WriteString("        request.Content = new ByteArrayContent(File.ReadAllBytes(" + csharpQuote(req.BodyFile) + "));\n")
		case req.Body != "":
			b.WriteString("        request.Content = new ByteArrayContent(Encoding.UTF8.GetBytes(" + csharpQuote(req.Body) + "));\n")
		default:
			b.WriteString("        request.Content = new ByteArrayContent(Array.Empty<byte>());\n")
		}
	}
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "C#")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			switch {
			case strings.EqualFold(h.name, "Host"):
				b.WriteString("        request.Headers.Host = " + csharpQuote(value) + ";\n")
			case csharpContentHeaders[http.CanonicalHeaderKey(h.name)]:
				b.WriteString("        request.Content.Headers.TryAddWithoutValidation(" + csharpQuote(h.name) + ", " + csharpQuote(value) + ");\n")
			default:
				b.WriteString("        request.Headers.TryAddWithoutValidation(" + csharpQuote(h.name) + ", " + csharpQuote(value) + ");\n")
			}
		}
	}
	b.WriteString("        using var response = await client.SendAsync(request);\n")
	b.WriteString("        Console.WriteLine((int)response.StatusCode);\n")
	b.WriteString("        Console.WriteLine(await response.Content.ReadAsStringAsync());\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// csharpQuote writes s as a C# string literal. Non-ASCII uses \uXXXX or
// \UXXXXXXXX, so the source stays ASCII.
func csharpQuote(s string) string {
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
		case '\v':
			b.WriteString(`\v`)
		default:
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
