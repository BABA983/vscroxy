package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"

	"github.com/egoist/mygo/ui"
)

type clientSetup struct {
	open    bool
	runtime string
	url     string
}

func (a *app) openClientSetup() {
	a.clientUI = clientSetup{open: true, runtime: "cURL", url: "https://example.com/"}
}

// Examples configure only the requesting process. They never install a root
// certificate, change the system proxy, or disable certificate verification.
func clientExample(runtime, addr, ca, target string) string {
	proxy := "http://" + addr
	switch runtime {
	case "Python":
		return fmt.Sprintf("# Requires requests: python -m pip install requests\nimport requests\ns = requests.Session()\ns.trust_env = False\nr = s.get(%q, proxies={\"http\": %q, \"https\": %q}, verify=%q, timeout=30)\nprint(r.status_code, r.text)", target, proxy, proxy, ca)
	case "Node.js":
		return fmt.Sprintf("// npm install https-proxy-agent\nimport https from 'node:https';\nimport {readFileSync} from 'node:fs';\nimport {HttpsProxyAgent} from 'https-proxy-agent';\nhttps.get(%q, {agent: new HttpsProxyAgent(%q), ca: readFileSync(%q)}, res => {\n  console.log(res.statusCode);\n  res.pipe(process.stdout);\n}).on('error', console.error);", target, proxy, ca)
	case "Go":
		return fmt.Sprintf("package main\nimport (\"crypto/tls\"; \"crypto/x509\"; \"fmt\"; \"io\"; \"net/http\"; \"net/url\"; \"os\"; \"time\")\nfunc main() {\n  pem, err := os.ReadFile(%q); if err != nil { panic(err) }\n  roots, err := x509.SystemCertPool(); if err != nil { roots = x509.NewCertPool() }\n  if !roots.AppendCertsFromPEM(pem) { panic(\"invalid CA file\") }\n  proxy, err := url.Parse(%q); if err != nil { panic(err) }\n  client := &http.Client{Timeout: 30*time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}}}\n  res, err := client.Get(%q); if err != nil { panic(err) }; defer res.Body.Close()\n  fmt.Println(res.Status); io.Copy(os.Stdout, res.Body)\n}", ca, proxy, target)
	default:
		return "curl --noproxy '' --proxy " + shellQuote(proxy) + " --cacert " + shellQuote(ca) + " --url " + shellQuote(target)
	}
}

func (a *app) clientSetupModal(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	if !s.open {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &s.open, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(500, height-80)).Children(func() {
			uikit.DialogTitle(c, "Set up a local client")
			ui.Form(c, func() {
				ui.Field(c, "Client runtime", func() {
					uikit.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label("Client runtime").Width(150)
				})
				ui.Field(c, "Setup HTTPS URL", func() {
					uikit.Input(c, &s.url).Label("Setup HTTPS URL")
				})
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Padding(0, t.Space(2), 0, 0).Children(func() {
				addr := ""
				if a.srv != nil && a.srv.Running() {
					addr = a.srv.Addr()
				}
				uikit.Body(c, "Proxy listener: "+cmp.Or(addr, "not running"))
				uikit.Meta(c, "The example trusts only the public CA file for this client. No system changes; no insecure TLS bypass. cURL commands use a POSIX shell.")
				example := clientExample(s.runtime, addr, a.caPath, s.url)
				if uikit.SecondaryButton(c, "Copy client example").Disabled(addr == "" || a.caPath == "").Clicked() {
					c.Services().WriteClipboard(example)
					a.done("Copied " + s.runtime + " setup; review URL and CA path before running")
				}
				ui.Text(c, example).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable()
				uikit.Meta(c, "Run it, then use Diagnose to see whether the request arrived and was decrypted.")
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Phone / tablet setup…").Clicked() {
					s.open = false
					a.openDevices()
				}
				if uikit.SecondaryButton(c, "Diagnose…").Clicked() {
					s.open = false
					a.diagnostics.target = s.url
					a.openDiagnostics(nil, diagnoseProcess)
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					s.open = false
				}
			})
		})
	})
}
