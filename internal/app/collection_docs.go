package app

import (
	"bytes"
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/locales"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/request"
)

// docsPair is one header row in collection documentation.
type docsPair struct {
	Name  string
	Value string
}

// docsExample is one saved response shown in documentation.
type docsExample struct {
	Name    string
	Status  int
	Headers []docsPair
	Body    string
}

// docsSample is one generated code sample.
type docsSample struct {
	Language string
	Code     string
}

// docsEntry is one request's documentation: what it sends, its auth with
// secrets hidden, its examples and copyable code samples.
type docsEntry struct {
	Name        string
	Method      string
	URL         string
	Collection  string
	Description string
	Headers     []docsPair
	Auth        string
	Body        string
	BodyNote    string
	Examples    []docsExample
	Samples     []docsSample
}

// docSampleLanguages are the code samples a documentation export carries.
var docSampleLanguages = []string{"JavaScript (fetch)", "Python (requests)", "Go (net/http)"}

const docBodyPreview = 4000

// docEntries builds the documentation for the requests under root, in folder
// and name order. Values stay as authored; auth secrets and credential
// headers are replaced, never expanded.
func docEntries(requests []savedRequest, root string) []docsEntry {
	var list []savedRequest
	for _, r := range requests {
		if r.Collection == root || strings.HasPrefix(r.Collection, root+"/") {
			list = append(list, r)
		}
	}
	slices.SortFunc(list, func(x, y savedRequest) int {
		return cmp.Or(strings.Compare(x.Collection, y.Collection), strings.Compare(x.Name, y.Name))
	})
	entries := make([]docsEntry, 0, len(list))
	for _, r := range list {
		entry := docsEntry{Name: r.Name, Method: cmp.Or(r.Protocol, r.Method, "HTTP"), URL: request.RedactURL(r.URL),
			Collection: strings.TrimPrefix(strings.TrimPrefix(r.Collection, root), "/"), Description: r.Description,
			Auth: authSummary(r.Auth)}
		for name, values := range request.ParseHeaderBlock(r.Headers) {
			for _, value := range values {
				entry.Headers = append(entry.Headers, docsPair{Name: name, Value: redactDocsHeader(name, value)})
			}
		}
		slices.SortFunc(entry.Headers, func(x, y docsPair) int {
			return cmp.Or(strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), strings.Compare(x.Value, y.Value))
		})
		entry.Body, entry.BodyNote = docsBody(r)
		for _, e := range r.Examples {
			example := docsExample{Name: e.Name, Status: e.Status, Body: truncateDocs(e.Body, docBodyPreview)}
			for name, values := range request.ParseHeaderBlock(e.Headers) {
				for _, value := range values {
					example.Headers = append(example.Headers, docsPair{Name: name, Value: redactDocsHeader(name, value)})
				}
			}
			entry.Examples = append(entry.Examples, example)
		}
		prepared := &request.Prepared{Method: r.Method, URL: request.RedactURL(r.URL), Header: redactedHeaderBlock(r.Headers), Body: r.Body}
		for _, language := range docSampleLanguages {
			if code, err := request.Snippet(language, prepared); err == nil && strings.TrimSpace(code) != "" {
				entry.Samples = append(entry.Samples, docsSample{Language: language, Code: truncateDocs(code, 8000)})
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// docsBody is the request body as authored, with a note for non-text bodies.
func docsBody(r savedRequest) (string, string) {
	if r.BodyMode == "Binary" {
		return "", cmp.Or("Binary body from "+r.BodyFile, locales.BinaryBody())
	}
	if r.BodyMode == "GraphQL" {
		body := strings.TrimSpace(r.GraphQL.Query)
		if v := strings.TrimSpace(r.GraphQL.Variables); v != "" {
			body += "\n\nVariables:\n" + v
		}
		return truncateDocs(body, docBodyPreview), ""
	}
	if len(r.Form) > 0 {
		var b strings.Builder
		for _, field := range r.Form {
			fmt.Fprintf(&b, "%s = %s\n", field.Name, field.Value)
		}
		return truncateDocs(b.String(), docBodyPreview), ""
	}
	return truncateDocs(r.Body, docBodyPreview), ""
}

func truncateDocs(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "\n… truncated for documentation"
}

// redactHeader hides a credential header's value.
func redactDocsHeader(name, value string) string {
	if request.IsSensitiveHeader(name) {
		return locales.Hidden()
	}
	return value
}

func redactedHeaderBlock(block string) http.Header {
	header := http.Header{}
	for name, values := range request.ParseHeaderBlock(block) {
		for _, value := range values {
			header.Add(name, redactDocsHeader(name, value))
		}
	}
	return header
}

// authSummary names an auth type and its non-secret parts; tokens, passwords,
// secrets and private keys are never included.
func authSummary(auth request.Auth) string {
	switch auth.Type {
	case "", "None":
		return "None"
	case "Inherit":
		return locales.InheritedFromTheFolderOr()
	case "Bearer":
		return locales.BearerTokenHidden()
	case "Basic":
		return "Basic · username " + cmp.Or(auth.Username, "(hidden)") + " · password hidden"
	case "API key":
		return "API key · " + cmp.Or(auth.Location, "header") + " · value hidden"
	case "OAuth 2.0":
		return "OAuth 2.0 · " + cmp.Or(auth.OAuth2.Grant, "grant") + " · client secret and tokens hidden"
	case "OAuth 1.0":
		return "OAuth 1.0 · consumer key " + cmp.Or(auth.OAuth1.ConsumerKey, "(hidden)") + " · secrets hidden"
	case "AWS Signature":
		return locales.AwsSignatureAccessKeyHidden()
	case "JWT":
		return "JWT · " + cmp.Or(auth.JWT.Algorithm, "algorithm") + " · secret and private key hidden"
	case "Hawk":
		return locales.HawkCredentialsHidden()
	case "Akamai EdgeGrid":
		return locales.AkamaiEdgegridCredentialsHidden()
	case "NTLM":
		return "NTLM · username " + cmp.Or(auth.NTLM.Username, "(hidden)") + " · password hidden"
	default:
		return auth.Type + " · credentials hidden"
	}
}

// docsMarkdown renders the documentation as a portable Markdown file.
func docsMarkdown(root, description string, entries []docsEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", cmp.Or(root, "Collection"))
	if strings.TrimSpace(description) != "" {
		b.WriteString(description + "\n\n")
	}
	b.WriteString("Generated by vscroxy. Auth secrets and credential headers are hidden; values are as authored and may still contain sensitive data.\n")
	for _, entry := range entries {
		fmt.Fprintf(&b, "\n## %s %s\n\n", entry.Method, cmp.Or(entry.Name, entry.URL))
		if entry.Collection != "" {
			fmt.Fprintf(&b, "Folder: %s\n\n", entry.Collection)
		}
		fmt.Fprintf(&b, "`%s`\n\n", entry.URL)
		if entry.Description != "" {
			b.WriteString(entry.Description + "\n\n")
		}
		fmt.Fprintf(&b, "**Auth:** %s\n\n", entry.Auth)
		if len(entry.Headers) > 0 {
			b.WriteString("### Headers\n\n| Name | Value |\n| --- | --- |\n")
			for _, header := range entry.Headers {
				fmt.Fprintf(&b, "| %s | %s |\n", docsMarkdownCell(header.Name), docsMarkdownCell(header.Value))
			}
			b.WriteString("\n")
		}
		if entry.Body != "" || entry.BodyNote != "" {
			b.WriteString("### Body\n\n")
			if entry.BodyNote != "" {
				b.WriteString(entry.BodyNote + "\n\n")
			}
			if entry.Body != "" {
				fence := docsFence(entry.Body)
				fmt.Fprintf(&b, "%s\n%s\n%s\n\n", fence, entry.Body, fence)
			}
		}
		if len(entry.Examples) > 0 {
			b.WriteString("### Examples\n\n")
			for _, example := range entry.Examples {
				fmt.Fprintf(&b, "#### %d · %s\n\n", example.Status, example.Name)
				if len(example.Headers) > 0 {
					b.WriteString("| Name | Value |\n| --- | --- |\n")
					for _, header := range example.Headers {
						fmt.Fprintf(&b, "| %s | %s |\n", docsMarkdownCell(header.Name), docsMarkdownCell(header.Value))
					}
					b.WriteString("\n")
				}
				if example.Body != "" {
					fence := docsFence(example.Body)
					fmt.Fprintf(&b, "%s\n%s\n%s\n\n", fence, example.Body, fence)
				}
			}
		}
		if len(entry.Samples) > 0 {
			b.WriteString("### Code samples\n\n")
			for _, sample := range entry.Samples {
				fence := docsFence(sample.Code)
				fmt.Fprintf(&b, "#### %s\n\n%s\n%s\n%s\n\n", sample.Language, fence, sample.Code, fence)
			}
		}
	}
	return b.String()
}

// docsFence returns a backtick fence longer than any run in text, so a body
// that itself contains a fence stays inside its block.
func docsFence(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func docsMarkdownCell(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "|", "\\|"), "\n", " ")
}

var docsHTML = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#fbfbfc;--panel:#fff;--text:#18181b;--muted:#666;--line:#e4e4e7;--code:#f4f4f5}
@media (prefers-color-scheme:dark){:root{--bg:#111113;--panel:#18181b;--text:#ececee;--muted:#a1a1aa;--line:#2e2e33;--code:#202024}}
*{box-sizing:border-box}body{margin:0;padding:32px;background:var(--bg);color:var(--text);font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:960px;margin:0 auto}h1{font-size:24px;margin:0 0 8px}h2{font-size:18px;margin:36px 0 6px;padding-top:18px;border-top:1px solid var(--line)}h3{font-size:14px;margin:20px 0 6px}
p{margin:6px 0}.muted{color:var(--muted)}code,pre{font:12px ui-monospace,SFMono-Regular,Menlo,monospace}pre{background:var(--code);padding:12px;border-radius:8px;overflow:auto;white-space:pre-wrap;word-break:break-word}
table{border-collapse:collapse;width:100%;margin:8px 0;background:var(--panel)}th,td{text-align:left;padding:6px 10px;border:1px solid var(--line);vertical-align:top}th{font-size:12px;color:var(--muted)}
.method{display:inline-block;font:600 12px ui-monospace,monospace;padding:2px 7px;border-radius:5px;background:var(--code);margin-right:6px}
footer{margin-top:40px;color:var(--muted);font-size:12px}
</style></head><body><main>
<h1>{{.Title}}</h1>
{{if .Description}}<div class="muted" style="white-space:pre-wrap">{{.Description}}</div>{{end}}
<p class="muted">Generated by vscroxy. Auth secrets and credential headers are hidden; values are as authored and may still contain sensitive data.</p>
{{range .Entries}}
<h2><span class="method">{{.Method}}</span>{{.Name}}</h2>
{{if .Collection}}<p class="muted">Folder: {{.Collection}}</p>{{end}}
<p><code>{{.URL}}</code></p>
{{if .Description}}<div style="white-space:pre-wrap">{{.Description}}</div>{{end}}
<p><strong>Auth:</strong> {{.Auth}}</p>
{{if .Headers}}<h3>Headers</h3><table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>{{range .Headers}}<tr><td>{{.Name}}</td><td>{{.Value}}</td></tr>{{end}}</tbody></table>{{end}}
{{if or .Body .BodyNote}}<h3>Body</h3>{{if .BodyNote}}<p class="muted">{{.BodyNote}}</p>{{end}}{{if .Body}}<pre>{{.Body}}</pre>{{end}}{{end}}
{{if .Examples}}<h3>Examples</h3>{{range .Examples}}<h4>{{.Status}} · {{.Name}}</h4>{{if .Headers}}<table><thead><tr><th>Name</th><th>Value</th></tr></thead><tbody>{{range .Headers}}<tr><td>{{.Name}}</td><td>{{.Value}}</td></tr>{{end}}</tbody></table>{{end}}{{if .Body}}<pre>{{.Body}}</pre>{{end}}{{end}}{{end}}
{{if .Samples}}<h3>Code samples</h3>{{range .Samples}}<h4>{{.Language}}</h4><pre>{{.Code}}</pre>{{end}}{{end}}
{{end}}
<footer>Generated by vscroxy from saved requests. Review before sharing.</footer>
</main></body></html>
`))

// docsHTMLFile renders the same documentation as one self-contained page: no
// scripts, fonts or stylesheets are fetched.
func docsHTMLFile(root, description string, entries []docsEntry) ([]byte, error) {
	var out bytes.Buffer
	view := struct {
		Title       string
		Description string
		Entries     []docsEntry
	}{cmp.Or(root, "Collection"), description, entries}
	if err := docsHTML.Execute(&out, view); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
