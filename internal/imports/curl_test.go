package imports

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/baba983/vscroxy/internal/request"
)

func TestParseCurlCMDChromeOutput(t *testing.T) {
	// Chrome's Copy as cURL (cmd): ^" quotes each argument, ^% keeps a
	// percent, ^\n\n is a newline inside the body, and a caret newline
	// continues the command.
	command := `curl ^"https://api.example.com/items?limit=7^" ^` + "\n" +
		`  -X ^"POST^" ^` + "\n" +
		`  -H ^"accept: application/json^" ^` + "\n" +
		`  -H ^"x-tag: first^" ^` + "\n" +
		`  --data-raw ^"^{^` + "\n\n" +
		`^\^"name^\^":^\^"Ada^\^"^` + "\n\n" +
		`^}^"`
	got, warnings, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings %q", warnings)
	}
	if got.Method != "POST" || got.URL != "https://api.example.com/items?limit=7" ||
		got.Body != "{\n\"name\":\"Ada\"\n}" || got.BodyMode != "" {
		t.Fatalf("request %+v", got)
	}
	wantHeaders := http.Header{"Accept": {"application/json"}, "X-Tag": {"first"}, "Content-Type": {"application/x-www-form-urlencoded"}}
	if headers := request.ParseHeaderBlock(got.Headers); !reflect.DeepEqual(headers, wantHeaders) {
		t.Fatalf("headers %#v, want %#v", headers, wantHeaders)
	}
}

func TestParseCurlCMDKeepsPercentAndLineContinuations(t *testing.T) {
	got, _, err := ParseCurl("curl.exe ^\"https://example.test/?x=^%^PATH^%^&q=1^\" ^\r\n  -H ^\"X-Test: a^&b^\"")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://example.test/?x=%PATH%&q=1" || request.ParseHeaderBlock(got.Headers).Get("X-Test") != "a&b" {
		t.Fatalf("request %+v headers %q", got, got.Headers)
	}
}

func TestParseCurlPowerShellChromeOutput(t *testing.T) {
	command := "$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\n" +
		"$session.UserAgent = \"Mozilla/5.0 fixture\"\n" +
		"$session.Cookies.Add((New-Object System.Net.Cookie(\"session\", \"abc\", \"/\", \"api.example.com\")))\n" +
		"Invoke-WebRequest -UseBasicParsing -Uri \"https://api.example.com/items?limit=7\" `\n" +
		"-Method \"POST\" `\n" +
		"-WebSession $session `\n" +
		"-Headers @{\n" +
		"\"accept\"=\"application/json\"\n" +
		"  \"x-tag\"=\"first\"\n" +
		"} `\n" +
		"-ContentType \"application/json\" `\n" +
		"-Body \"{`\"name`\":`\"Ada`\"}\""
	got, warnings, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings %q", warnings)
	}
	if got.Method != "POST" || got.URL != "https://api.example.com/items?limit=7" ||
		got.Body != `{"name":"Ada"}` || got.BodyMode != "Raw" {
		t.Fatalf("request %+v", got)
	}
	want := map[string]string{
		"Accept":       "application/json",
		"X-Tag":        "first",
		"Content-Type": "application/json",
		"User-Agent":   "Mozilla/5.0 fixture",
		"Cookie":       "session=abc",
	}
	headers := request.ParseHeaderBlock(got.Headers)
	for name, value := range want {
		if headers.Get(name) != value {
			t.Errorf("header %s = %q, want %q", name, headers.Get(name), value)
		}
	}
}

func TestParseCurlPowerShellByteBodyAndCookieWarning(t *testing.T) {
	command := "$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\n" +
		"$session.Cookies.Add((New-Object System.Net.Cookie(\"token\", \"a b\", \"/app\", \"api.example.com\")))\n" +
		"Invoke-WebRequest -UseBasicParsing -Uri \"https://api.example.com/echo\" -Method \"PATCH\" -WebSession $session -Body ([System.Text.Encoding]::UTF8.GetBytes(\"line`n雪\"))"
	got, warnings, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "PATCH" || got.Body != "line\n雪" || got.BodyMode != "Raw" {
		t.Fatalf("request %+v", got)
	}
	if request.ParseHeaderBlock(got.Headers).Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Errorf("PowerShell string body default content type: %q", got.Headers)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "cookie domain/path were dropped") {
		t.Errorf("warnings %q", warnings)
	}
}

func TestParseCurlRefusesUnsupportedPowerShell(t *testing.T) {
	for _, command := range []string{
		`Invoke-WebRequest -Uri "https://example.test" -OutFile "x"`,
		`Invoke-RestMethod -Uri "https://example.test"`,
		`Invoke-WebRequest -Uri "https://example.test" -Headers @{ "a" = "b"`,
		`Invoke-WebRequest -Uri "https://example.test" -Body ([System.IO.File]::ReadAllBytes("x"))`,
		"$session = New-Object X\nWrite-Host \"hi\"\nInvoke-WebRequest -Uri \"https://example.test\"",
	} {
		if _, _, err := ParseCurl(command); err == nil {
			t.Errorf("accepted unsupported PowerShell: %q", command)
		}
	}
}

func TestParseCurlRefusesMalformedCMD(t *testing.T) {
	for _, command := range []string{
		`curl ^"https://example.test^`,
		`curl.exe ^"https://example.test^" ^"https://other.test^"`,
		`curl.exe ^"https://example.test^" -X ^"BAD METHOD^"`,
	} {
		if _, _, err := ParseCurl(command); err == nil {
			t.Errorf("accepted malformed CMD: %q", command)
		}
	}
}
