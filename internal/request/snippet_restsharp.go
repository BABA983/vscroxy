package request

import (
	"fmt"
	"strings"
)

// restsharpMethods are the methods RestSharp's Method enum can send.
var restsharpMethods = map[string]string{
	"GET": "Get", "POST": "Post", "PUT": "Put", "DELETE": "Delete",
	"HEAD": "Head", "OPTIONS": "Options", "PATCH": "Patch",
}

// restsharpSnippet writes a C# RestSharp request.
func restsharpSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	restMethod, ok := restsharpMethods[strings.ToUpper(method)]
	if !ok {
		return "", fmt.Errorf("RestSharp cannot send %s; copy it as cURL instead", method)
	}
	if err := requireASCIIHeaders("RestSharp", headers); err != nil {
		return "", err
	}
	hasBody := req.Body != "" || req.BodyFile != ""
	var b strings.Builder
	b.WriteString("// C# with RestSharp 110+.\n")
	b.WriteString("using System;\n")
	if req.BodyFile != "" {
		b.WriteString("using System.IO;\n")
	}
	b.WriteString("using System.Threading.Tasks;\n")
	b.WriteString("using RestSharp;\n\n")
	b.WriteString("class Program\n{\n")
	b.WriteString("    static async Task Main()\n    {\n")
	b.WriteString("        var client = new RestClient(new RestClientOptions { FollowRedirects = false });\n")
	b.WriteString("        var request = new RestRequest(" + csharpQuote(req.URL) + ", Method." + restMethod + ");\n")
	bodyType := "application/octet-stream"
	for _, h := range headers {
		if strings.EqualFold(h.name, "Content-Type") && len(h.values) > 0 {
			bodyType = h.values[0]
		}
	}
	for _, h := range headers {
		if strings.EqualFold(h.name, "Host") {
			return "", fmt.Errorf("RestSharp cannot set the Host header; copy it as cURL instead")
		}
		if hasBody && strings.EqualFold(h.name, "Content-Type") {
			continue // The body parameter sets it.
		}
		values, err := snippetHeaderValues(h, "RestSharp")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			b.WriteString("        request.AddHeader(" + csharpQuote(h.name) + ", " + csharpQuote(value) + ");\n")
		}
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("        request.AddParameter(new BodyParameter(File.ReadAllBytes(" + csharpQuote(req.BodyFile) + "), " + csharpQuote(bodyType) + ", DataFormat.Binary));\n")
	case req.Body != "":
		b.WriteString("        request.AddStringBody(" + csharpQuote(req.Body) + ", " + csharpQuote(bodyType) + ");\n")
	}
	b.WriteString("        var response = await client.ExecuteAsync(request);\n")
	b.WriteString("        Console.WriteLine((int)response.StatusCode);\n")
	b.WriteString("        Console.WriteLine(response.Content);\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String(), nil
}
