package request

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nativeSnippetRequest has the edges the native generators must preserve:
// a query, repeated and empty headers, a joined Cookie, a header and body
// that need escaping, and Unicode.
func nativeSnippetRequest() *Prepared {
	return &Prepared{
		Method: "PATCH",
		URL:    "https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b",
		Body:   "{\n  \"name\": \"你好 😀\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} #${y}\"\n}",
		Header: http.Header{
			"Content-Type":   {"application/json; charset=utf-8"},
			"X-Dup":          {"a", "b"},
			"X-Empty":        {""},
			"X-Quote":        {`say "hi" \ 'x'`},
			"Cookie":         {"a=7", "b=9"},
			"Content-Length": {"99"},
			"Connection":     {"keep-alive"},
			"Host":           {"API.example.com"},
		},
	}
}

func TestSnippetNativeLanguages(t *testing.T) {
	want := map[string]string{
		"Swift (URLSession)": `// Swift 5+; FoundationNetworking supplies URLSession on Linux.
import Foundation
#if canImport(FoundationNetworking)
import FoundationNetworking
#endif

final class NoRedirects: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

guard let url = URL(string: "https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b") else {
    fatalError("invalid URL")
}
var request = URLRequest(url: url)
request.httpMethod = "PATCH"
request.addValue("application/json; charset=utf-8", forHTTPHeaderField: "Content-Type")
request.addValue("a=7; b=9", forHTTPHeaderField: "Cookie")
request.addValue("a", forHTTPHeaderField: "X-Dup")
request.addValue("b", forHTTPHeaderField: "X-Dup")
request.addValue("", forHTTPHeaderField: "X-Empty")
request.addValue("say \"hi\" \\ 'x'", forHTTPHeaderField: "X-Quote")
request.httpBody = Data("{\n  \"name\": \"\u{4f60}\u{597d} \u{1f600}\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} #${y}\"\n}".utf8)

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
`,
		"Kotlin (OkHttp)": `// Kotlin with OkHttp 4.x.
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

fun main() {
    val client = OkHttpClient.Builder()
        .followRedirects(false)
        .followSslRedirects(false)
        .build()
    val body = "{\n  \"name\": \"\u4f60\u597d \ud83d\ude00\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"\${x} #\${y}\"\n}".toByteArray(Charsets.UTF_8).toRequestBody(null)
    val request = Request.Builder()
        .url("https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b")
        .method("PATCH", body)
        .addHeader("Content-Type", "application/json; charset=utf-8")
        .addHeader("Cookie", "a=7; b=9")
        .addHeader("X-Dup", "a")
        .addHeader("X-Dup", "b")
        .addHeader("X-Empty", "")
        .addHeader("X-Quote", "say \"hi\" \\ 'x'")
        .build()

    client.newCall(request).execute().use { response ->
        println(response.code)
        println(response.body?.string())
    }
}
`,
		"Java (java.net.http)": `// Java 11+; save as Main.java.
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;

public class Main {
    public static void main(String[] args) throws Exception {
        HttpClient client = HttpClient.newBuilder()
            .followRedirects(HttpClient.Redirect.NEVER)
            .build();
        HttpRequest.Builder builder = HttpRequest.newBuilder()
            .uri(URI.create("https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b"))
            .method("PATCH", HttpRequest.BodyPublishers.ofByteArray("{\n  \"name\": \"\u4f60\u597d \ud83d\ude00\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} #${y}\"\n}".getBytes(StandardCharsets.UTF_8)));
        builder.header("Content-Type", "application/json; charset=utf-8");
        builder.header("Cookie", "a=7; b=9");
        builder.header("X-Dup", "a");
        builder.header("X-Dup", "b");
        builder.header("X-Empty", "");
        builder.header("X-Quote", "say \"hi\" \\ 'x'");
        HttpRequest request = builder.build();
        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString(StandardCharsets.UTF_8));
        System.out.println(response.statusCode());
        System.out.println(response.body());
    }
}
`,
		"C# (HttpClient)": `// C# with .NET 6+.
using System;
using System.Net.Http;
using System.Text;
using System.Threading.Tasks;

class Program
{
    static async Task Main()
    {
        using var client = new HttpClient(new HttpClientHandler { AllowAutoRedirect = false });
        using var request = new HttpRequestMessage(new HttpMethod("PATCH"), "https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b");
        request.Content = new ByteArrayContent(Encoding.UTF8.GetBytes("{\n  \"name\": \"\u4f60\u597d \U0001f600\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} #${y}\"\n}"));
        request.Content.Headers.TryAddWithoutValidation("Content-Type", "application/json; charset=utf-8");
        request.Headers.TryAddWithoutValidation("Cookie", "a=7; b=9");
        request.Headers.TryAddWithoutValidation("X-Dup", "a");
        request.Headers.TryAddWithoutValidation("X-Dup", "b");
        request.Headers.TryAddWithoutValidation("X-Empty", "");
        request.Headers.TryAddWithoutValidation("X-Quote", "say \"hi\" \\ 'x'");
        using var response = await client.SendAsync(request);
        Console.WriteLine((int)response.StatusCode);
        Console.WriteLine(await response.Content.ReadAsStringAsync());
    }
}
`,
		"PHP (cURL)": `<?php
$ch = curl_init("https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b");
curl_setopt_array($ch, [
    CURLOPT_CUSTOMREQUEST => "PATCH",
    CURLOPT_RETURNTRANSFER => true,
    CURLOPT_FOLLOWLOCATION => false,
    CURLOPT_HTTPHEADER => [
        "Content-Type: application/json; charset=utf-8",
        "Cookie: a=7; b=9",
        "X-Dup: a",
        "X-Dup: b",
        "X-Empty;",
        "X-Quote: say \"hi\" \\ 'x'",
    ],
    CURLOPT_POSTFIELDS => "{\n  \"name\": \"\xe4\xbd\xa0\xe5\xa5\xbd \xf0\x9f\x98\x80\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"\${x} #\${y}\"\n}",
]);
$response = curl_exec($ch);
if ($response === false) {
    fwrite(STDERR, curl_error($ch) . "\n");
    exit(1);
}
$status = curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
curl_close($ch);
echo $status . "\n" . $response . "\n";
`,
		"Ruby (Net::HTTP)": `require "net/http"
require "uri"

uri = URI("https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b")
request = Net::HTTPGenericRequest.new("PATCH", true, true, uri)
request.add_field("Content-Type", "application/json; charset=utf-8")
request.add_field("Cookie", "a=7; b=9")
request.add_field("X-Dup", "a")
request.add_field("X-Dup", "b")
request.add_field("X-Empty", "")
request.add_field("X-Quote", "say \"hi\" \\ 'x'")
request.body = "{\n  \"name\": \"\xe4\xbd\xa0\xe5\xa5\xbd \xf0\x9f\x98\x80\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} \#${y}\"\n}"
response = Net::HTTP.start(uri.host, uri.port, use_ssl: uri.scheme == "https") do |http|
  http.request(request)
end
puts response.code
puts response.body
`,
		"Rust (reqwest)": `// Cargo.toml: reqwest = { version = "0.12", features = ["blocking"] }
use std::error::Error;

fn main() -> Result<(), Box<dyn Error>> {
    let client = reqwest::blocking::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let mut headers = reqwest::header::HeaderMap::new();
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"Content-Type")?,
        reqwest::header::HeaderValue::from_bytes(b"application/json; charset=utf-8")?,
    );
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"Cookie")?,
        reqwest::header::HeaderValue::from_bytes(b"a=7; b=9")?,
    );
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"X-Dup")?,
        reqwest::header::HeaderValue::from_bytes(b"a")?,
    );
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"X-Dup")?,
        reqwest::header::HeaderValue::from_bytes(b"b")?,
    );
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"X-Empty")?,
        reqwest::header::HeaderValue::from_bytes(b"")?,
    );
    headers.append(
        reqwest::header::HeaderName::from_bytes(b"X-Quote")?,
        reqwest::header::HeaderValue::from_bytes(b"say \"hi\" \\ 'x'")?,
    );
    let response = client
        .request(reqwest::Method::from_bytes(b"PATCH")?, "https://api.example.com/v1/items/42?q=%E4%BD%A0&tag=a&tag=b")
        .headers(headers)
        .body("{\n  \"name\": \"\u{4f60}\u{597d} \u{1f600}\",\n  \"quote\": \"say \\\"hi\\\" \\\\ done\",\n  \"tpl\": \"${x} #${y}\"\n}".as_bytes().to_vec())
        .send()?;
    println!("{}", response.status());
    println!("{}", response.text()?);
    Ok(())
}
`,
	}
	for _, language := range []string{"Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)", "PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)"} {
		got, err := Snippet(language, nativeSnippetRequest())
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		if got != want[language] {
			t.Errorf("%s:\n%s\nwant:\n%s", language, got, want[language])
		}
	}
}

func TestSnippetNativeBinaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload.bin")
	body := []byte{0, 0xff, '\r', '\n', '{', '{', 'x', '}', '}'}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ language, reference string }{
		{"Swift (URLSession)", `Data(contentsOf: URL(fileURLWithPath: "` + path + `"))`},
		{"Kotlin (OkHttp)", `java.io.File("` + path + `").readBytes()`},
		{"Java (java.net.http)", `HttpRequest.BodyPublishers.ofFile(Path.of("` + path + `"))`},
		{"C# (HttpClient)", `File.ReadAllBytes("` + path + `")`},
		{"PHP (cURL)", `file_get_contents("` + path + `")`},
		{"Ruby (Net::HTTP)", `File.binread("` + path + `")`},
		{"Rust (reqwest)", `std::fs::read("` + path + `")`},
	} {
		req := &Prepared{Method: "PUT", URL: "https://api.example.com/upload", Body: string(body), BodyFile: path,
			Header: http.Header{"Content-Type": {"application/octet-stream"}}}
		code, err := Snippet(tc.language, req)
		if err != nil {
			t.Fatalf("%s: %v", tc.language, err)
		}
		if !strings.Contains(code, tc.reference) {
			t.Errorf("%s did not read the binary file:\n%s", tc.language, code)
		}
		if strings.Contains(code, string(body)) {
			t.Errorf("%s inlined binary bytes:\n%s", tc.language, code)
		}
	}
}

func TestSnippetNativeRejectsLossyInputs(t *testing.T) {
	for _, language := range []string{"Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)", "PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)"} {
		if _, err := Snippet(language, &Prepared{Method: "POST", URL: "https://example.test", Body: "\x89PNG\xff"}); err == nil || err.Error() != "the body is binary; copy it as cURL instead" {
			t.Errorf("%s accepted a binary body without a file: %v", language, err)
		}
	}
	for _, language := range []string{"Swift (URLSession)", "Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)"} {
		if _, err := Snippet(language, &Prepared{URL: "https://example.test", Header: http.Header{"X-Test": {"\xff"}}}); err == nil {
			t.Errorf("%s accepted a header value that is not UTF-8", language)
		}
	}
	for _, language := range []string{"Kotlin (OkHttp)", "Java (java.net.http)", "C# (HttpClient)"} {
		if _, err := Snippet(language, &Prepared{URL: "https://example.test", Header: http.Header{"X-Test": {"你好"}}}); err == nil {
			t.Errorf("%s accepted a non-ASCII header value", language)
		}
	}
	for _, language := range []string{"Swift (URLSession)", "PHP (cURL)", "Ruby (Net::HTTP)", "Rust (reqwest)"} {
		if _, err := Snippet(language, &Prepared{URL: "https://example.test", Header: http.Header{"X-Test": {"你好"}}}); err != nil {
			t.Errorf("%s rejected a non-ASCII header value: %v", language, err)
		}
	}
	if _, err := Snippet("Kotlin (OkHttp)", &Prepared{Method: "GET", URL: "https://example.test", Body: "body"}); err == nil {
		t.Fatal("OkHttp accepted a GET body")
	}
	if _, err := Snippet("PHP (cURL)", &Prepared{Method: "HEAD", URL: "https://example.test", Body: "body"}); err == nil {
		t.Fatal("PHP cURL accepted a HEAD body")
	}
	for _, language := range []string{"C# (HttpClient)", "Ruby (Net::HTTP)"} {
		if _, err := Snippet(language, &Prepared{URL: "https://example.test", Header: http.Header{"Set-Cookie": {"a=1", "b=2"}}}); err == nil {
			t.Errorf("%s accepted repeated Set-Cookie headers", language)
		}
	}
}
