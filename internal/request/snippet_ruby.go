package request

import (
	"fmt"
	"strconv"
	"strings"
)

func rubySnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	hasBody := req.Body != "" || req.BodyFile != ""
	bodyPermitted := hasBody || method == "POST" || method == "PUT" || method == "PATCH"
	var b strings.Builder
	b.WriteString("require \"net/http\"\n")
	b.WriteString("require \"uri\"\n\n")
	b.WriteString("uri = URI(" + rubyQuote(req.URL) + ")\n")
	b.WriteString("request = Net::HTTPGenericRequest.new(" + rubyQuote(method) + ", " + strconv.FormatBool(bodyPermitted) + ", " + strconv.FormatBool(method != "HEAD") + ", uri)\n")
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "Ruby")
		if err != nil {
			return "", err
		}
		replace := strings.EqualFold(h.name, "Host") || strings.EqualFold(h.name, "User-Agent") ||
			strings.EqualFold(h.name, "Accept") || strings.EqualFold(h.name, "Accept-Encoding")
		for i, value := range values {
			if i == 0 && replace {
				b.WriteString("request[" + rubyQuote(h.name) + "] = " + rubyQuote(value) + "\n")
			} else {
				b.WriteString("request.add_field(" + rubyQuote(h.name) + ", " + rubyQuote(value) + ")\n")
			}
		}
	}
	switch {
	case req.BodyFile != "":
		b.WriteString("request.body = File.binread(" + rubyQuote(req.BodyFile) + ")\n")
	case req.Body != "":
		b.WriteString("request.body = " + rubyQuote(req.Body) + "\n")
	case bodyPermitted:
		b.WriteString("request.body = \"\"\n")
	}
	b.WriteString("response = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == \"https\") do |http|\n")
	b.WriteString("  http.request(request)\n")
	b.WriteString("end\n")
	b.WriteString("puts response.code\n")
	b.WriteString("puts response.body\n")
	return b.String(), nil
}

// rubyQuote writes s as a Ruby double-quoted string. Bytes outside printable
// ASCII use \xHH, preserving UTF-8 and binary bodies. Interpolation markers
// are escaped so a body's #{} or #$ stays literal.
func rubyQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '#':
			if i+1 < len(s) && (s[i+1] == '{' || s[i+1] == '$' || s[i+1] == '@') {
				b.WriteString(`\#`)
			} else {
				b.WriteByte('#')
			}
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
