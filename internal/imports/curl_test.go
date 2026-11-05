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

func TestParseCurlOlderCMDPlainQuotesAndCaretContinuation(t *testing.T) {
	// Older Chrome builds wrote ordinary double quotes and used a caret at
	// the end of the line as cmd.exe's continuation, rather than ^" quoting.
	command := "curl \"https://x.test/\" ^\n  -H \"a: b\""
	if got := DetectCurl(command); got != CurlCMD {
		t.Fatalf("DetectCurl = %v, want CurlCMD", got)
	}
	got, warnings, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings %q", warnings)
	}
	if got.URL != "https://x.test/" || got.Method != "GET" {
		t.Fatalf("request %+v", got)
	}
	if value := request.ParseHeaderBlock(got.Headers).Get("A"); value != "b" {
		t.Fatalf("A header = %q, want %q", value, "b")
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

func TestDetectCurlKeepsBashWhenCaretTextIsNotTheFirstArgument(t *testing.T) {
	got, _, err := ParseCurl(`curl https://x.test/ --data-raw 'q=^"x"'`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != `q=^"x"` || got.Method != "POST" {
		t.Fatalf("Bash body changed: %+v", got)
	}
	got, _, err = ParseCurl(`curl.exe 'https://x.test/a b'`)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/a b" || got.Method != "GET" {
		t.Fatalf("curl.exe POSIX command: %+v", got)
	}
	got, _, err = ParseCurl(`curl https://x.test/ --data-raw '--url ^"x"'`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != `--url ^"x"` {
		t.Fatalf("Bash body with --url text changed: %+v", got)
	}
	// Adjacent quoted fragments are one Bash word. The caret text is part
	// of the body, not a cmd.exe first argument or --url value.
	got, _, err = ParseCurl(`curl -H 'X: a' --data-raw 'it'\''s --url ^"x' https://x.test/`)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/" || got.Body != `it's --url ^"x` || got.Method != "POST" {
		t.Fatalf("Bash adjacent quotes changed: %+v", got)
	}
	if value := request.ParseHeaderBlock(got.Headers).Get("X"); value != "a" {
		t.Fatalf("X header = %q, want %q", value, "a")
	}
}

func TestParseCurlUnescapesChromeURLGlobs(t *testing.T) {
	got, _, err := ParseCurl(`curl 'https://x.test/a\[b\]/\{c\}'`)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/a[b]/{c}" {
		t.Fatalf("Bash URL %q", got.URL)
	}
	cmd := "curl ^\"https://x.test/a^\\^[b^\\^]^\""
	got, _, err = ParseCurl(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/a[b]" {
		t.Fatalf("CMD URL %q", got.URL)
	}
}

func TestPowerShellCombinesUTF16SurrogatePairs(t *testing.T) {
	command := "Invoke-WebRequest -Uri \"https://x.test\" -Body \"$([char]55357)$([char]56832)\""
	got, _, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "😀" {
		t.Fatalf("body %q", got.Body)
	}
	command = "Invoke-WebRequest -Uri \"https://x.test\" -Body \"$([char]55357)\""
	got, _, err = ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "\uFFFD" {
		t.Fatalf("lone surrogate body %q", got.Body)
	}
}

func TestPowerShellDropsHTTP2PseudoHeaders(t *testing.T) {
	command := `Invoke-WebRequest -Uri "https://x.test" -Headers @{ "authority"="x.test"; "method"="GET"; "path"="/"; "scheme"="https"; "accept"="application/json" }`
	got, _, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	headers := request.ParseHeaderBlock(got.Headers)
	for _, name := range []string{"Authority", "Method", "Path", "Scheme"} {
		if headers.Get(name) != "" {
			t.Errorf("pseudo header %s was imported: %q", name, got.Headers)
		}
	}
	if headers.Get("Accept") != "application/json" {
		t.Errorf("Accept header missing: %q", got.Headers)
	}
}

func TestPowerShellRejectsHeaderInjection(t *testing.T) {
	for _, command := range []string{
		"$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\n$session.UserAgent = \"ok\r\nX-Injected: yes\"\nInvoke-WebRequest -Uri \"https://x.test\"",
		"$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession\n$session.Cookies.Add((New-Object System.Net.Cookie(\"a\", \"b\r\nX-Injected: yes\", \"/\", \"x.test\")))\nInvoke-WebRequest -Uri \"https://x.test\"",
	} {
		if _, _, err := ParseCurl(command); err == nil {
			t.Errorf("accepted injected PowerShell input: %q", command)
		} else if strings.Contains(err.Error(), "X-Injected") {
			t.Errorf("injection error leaked the header: %v", err)
		}
	}
}

func TestPowerShellHeadersAreSorted(t *testing.T) {
	command := `Invoke-WebRequest -Uri "https://x.test" -Headers @{ "zeta"="1"; "alpha"="2" }`
	got, _, err := ParseCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, row := range got.HeaderList {
		names = append(names, row.Name)
	}
	if !reflect.DeepEqual(names, []string{"Alpha", "Zeta"}) {
		t.Fatalf("header order %q", names)
	}
}

// Redirect, certificate and timeout options map the same way in all three
// dialects Chrome copies. PowerShell's switches are its own spelling.
func TestParseCurlTransferSettingsInEveryDialect(t *testing.T) {
	cases := []struct{ name, command string }{
		{"bash", `curl -L --max-redirs 3 -k --max-time 2.5 https://example.test/`},
		{"cmd", `curl.exe ^"https://example.test/^" -L --max-redirs 3 --insecure --max-time 2.5`},
		{"powershell", `Invoke-WebRequest -Uri "https://example.test/" -MaximumRedirection 3 -SkipCertificateCheck -TimeoutSec 3`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warnings, err := ParseCurl(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != 0 {
				t.Fatalf("warnings %q", warnings)
			}
			if got.Settings.FollowRedirects == nil || !*got.Settings.FollowRedirects ||
				got.Settings.MaxRedirects == nil || *got.Settings.MaxRedirects != 3 {
				t.Fatalf("redirects = %+v, want following three hops", got.Settings)
			}
			if got.Settings.VerifyTLS == nil || *got.Settings.VerifyTLS {
				t.Fatalf("verify TLS = %v, want off", got.Settings.VerifyTLS)
			}
			if got.Settings.TimeoutSeconds == nil || *got.Settings.TimeoutSeconds != 3 {
				t.Fatalf("timeout = %v, want 3 seconds", got.Settings.TimeoutSeconds)
			}
		})
	}

	// Zero means no redirects in both cURL and PowerShell.
	for _, command := range []string{
		`curl -L --max-redirs 0 https://example.test/`,
		`Invoke-WebRequest -Uri "https://example.test/" -MaximumRedirection 0`,
	} {
		got, _, err := ParseCurl(command)
		if err != nil {
			t.Fatal(err)
		}
		if got.Settings.Follows() {
			t.Errorf("%q = %+v, want redirects off", command, got.Settings)
		}
	}
}

func TestDetectCurlFindsChromeURLArgument(t *testing.T) {
	got, _, err := ParseCurl("curl.exe -q --path-as-is --url ^\"https://x.test/a^\"")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/a" {
		t.Fatalf("CMD --url import %+v", got)
	}
	got, _, err = ParseCurl("curl --url=^\"https://x.test/b^\"")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://x.test/b" {
		t.Fatalf("CMD --url= import %+v", got)
	}
}
