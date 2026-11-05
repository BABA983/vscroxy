package request

import (
	"fmt"
	"strings"
)

func swiftSnippet(method string, req *Prepared, headers []snippetHeader) (string, error) {
	if err := requireUTF8Headers("Swift", headers); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("// Swift 5+; FoundationNetworking supplies URLSession on Linux.\n")
	b.WriteString("import Foundation\n")
	b.WriteString("#if canImport(FoundationNetworking)\nimport FoundationNetworking\n#endif\n\n")
	b.WriteString(`final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

`)
	b.WriteString("guard let url = URL(string: " + swiftQuote(req.URL) + ") else {\n    fatalError(\"invalid URL\")\n}\n")
	b.WriteString("var request = URLRequest(url: url)\n")
	b.WriteString("request.httpMethod = " + swiftQuote(method) + "\n")
	for _, h := range headers {
		values, err := snippetHeaderValues(h, "Swift")
		if err != nil {
			return "", err
		}
		for _, value := range values {
			if strings.EqualFold(h.name, "Host") {
				b.WriteString("request.setValue(" + swiftQuote(value) + ", forHTTPHeaderField: \"Host\")\n")
			} else {
				b.WriteString("request.addValue(" + swiftQuote(value) + ", forHTTPHeaderField: " + swiftQuote(h.name) + ")\n")
			}
		}
	}
	if req.BodyFile != "" {
		b.WriteString("guard let body = try? Data(contentsOf: URL(fileURLWithPath: " + swiftQuote(req.BodyFile) + ")) else {\n    fatalError(\"cannot read request body\")\n}\n")
		b.WriteString("request.httpBody = body\n")
	} else if req.Body != "" {
		b.WriteString("request.httpBody = Data(" + swiftQuote(req.Body) + ".utf8)\n")
	}
	b.WriteString(`
let delegate = NoRedirects()
let session = URLSession(configuration: .default, delegate: delegate, delegateQueue: nil)
let semaphore = DispatchSemaphore(value: 0)
session.dataTask(with: request) { data, response, error in
    if let error = error {
        print(error)
    } else {
        if let response = response as? HTTPURLResponse {
            print(response.statusCode)
        }
        if let data = data {
            print(String(decoding: data, as: UTF8.self))
        }
    }
    semaphore.signal()
}.resume()
semaphore.wait()
`)
	return b.String(), nil
}

// swiftQuote writes s as a Swift string literal. Non-ASCII and control
// characters use \u{...}, so the source stays ASCII and the string keeps
// the same Unicode scalars.
func swiftQuote(s string) string {
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
