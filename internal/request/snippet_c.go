package request

import (
	"fmt"
	"strings"
)

// cSnippet writes a libcurl easy-interface program.
func cSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	hasBody := req.Body != "" || req.BodyFile != ""
	if method == "HEAD" && hasBody {
		return "", fmt.Errorf("C libcurl cannot send a body with HEAD; copy it as cURL instead")
	}
	var b strings.Builder
	b.WriteString("// C with libcurl; link with -lcurl.\n")
	b.WriteString("#include <curl/curl.h>\n")
	b.WriteString("#include <stdio.h>\n")
	b.WriteString("#include <stdlib.h>\n\n")
	b.WriteString("static size_t write_body(char *data, size_t size, size_t count, void *userdata) {\n")
	b.WriteString("    (void)userdata;\n")
	b.WriteString("    return fwrite(data, size, count, stdout);\n")
	b.WriteString("}\n\n")
	b.WriteString("int main(void) {\n")
	b.WriteString("    CURL *curl = curl_easy_init();\n")
	b.WriteString("    if (!curl) return 1;\n")
	b.WriteString("    struct curl_slist *headers = NULL;\n")
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "C libcurl")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			line := h.name + ": " + value
			if value == "" {
				line = h.name + ";"
			}
			b.WriteString("    headers = curl_slist_append(headers, " + cLiteral(line) + ");\n")
		}
	}
	if hasBody && !has(headers, "Content-Type") {
		b.WriteString("    headers = curl_slist_append(headers, \"Content-Type:\");\n")
	}
	b.WriteString("    curl_easy_setopt(curl, CURLOPT_URL, " + cLiteral(req.URL) + ");\n")
	b.WriteString("    curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, " + cLiteral(method) + ");\n")
	b.WriteString("    curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 0L);\n")
	b.WriteString("    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);\n")
	b.WriteString("    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, write_body);\n")
	if method == "HEAD" {
		b.WriteString("    curl_easy_setopt(curl, CURLOPT_NOBODY, 1L);\n")
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("    FILE *body = fopen(" + cLiteral(req.BodyFile) + ", \"rb\");\n")
		b.WriteString("    if (!body) { fprintf(stderr, \"cannot read request body\\n\"); return 1; }\n")
		b.WriteString("    fseek(body, 0, SEEK_END);\n")
		b.WriteString("    long body_len = ftell(body);\n")
		b.WriteString("    rewind(body);\n")
		b.WriteString("    char *body_data = malloc((size_t)body_len);\n")
		b.WriteString("    if (!body_data || fread(body_data, 1, (size_t)body_len, body) != (size_t)body_len) {\n")
		b.WriteString("        fprintf(stderr, \"cannot read request body\\n\");\n")
		b.WriteString("        return 1;\n")
		b.WriteString("    }\n")
		b.WriteString("    fclose(body);\n")
		b.WriteString("    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body_data);\n")
		b.WriteString("    curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, body_len);\n")
	case req.Body != "":
		b.WriteString("    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, " + cLiteral(req.Body) + ");\n")
		b.WriteString("    curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, (long)" + fmt.Sprint(len(req.Body)) + ");\n")
	}
	b.WriteString("    CURLcode result = curl_easy_perform(curl);\n")
	b.WriteString("    if (result != CURLE_OK) {\n")
	b.WriteString("        fprintf(stderr, \"%s\\n\", curl_easy_strerror(result));\n")
	b.WriteString("        return 1;\n")
	b.WriteString("    }\n")
	b.WriteString("    long status = 0;\n")
	b.WriteString("    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &status);\n")
	b.WriteString("    printf(\"\\n%ld\\n\", status);\n")
	if req.BodyFile != "" {
		b.WriteString("    free(body_data);\n")
	}
	b.WriteString("    curl_slist_free_all(headers);\n")
	b.WriteString("    curl_easy_cleanup(curl);\n")
	b.WriteString("    return 0;\n")
	b.WriteString("}\n")
	return b.String(), nil
}

// cQuote writes s as a C string literal. Bytes outside printable ASCII use
// three-digit octal escapes, which cannot swallow a following hex digit.
func cLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('"')
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
				fmt.Fprintf(&b, `\%03o`, c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
