package main

import (
	"cmp"
	"fmt"
	"net/url"
	"time"

	"github.com/baba983/vscroxy/desktop/internal/capture"
	"github.com/egoist/mygo/ui"
)

type clientSetup struct {
	open    bool
	runtime string
	url     string
	since   time.Time
}

func (a *app) openClientSetup() {
	a.clientUI = clientSetup{open: true, runtime: "cURL", url: "https://example.com/", since: time.Now()}
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

// Evidence is deliberately session-wide, not attributed to the selected device
// or runtime: gateways do not preserve a device identity in the capture core.
func (a *app) setupEvidence(c *ui.Context, t *ui.Theme, target string, since time.Time) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		ui.Text(c, "Enter an HTTPS URL without embedded credentials.").FontSize(12).TextColor(t.Warning)
		return
	}
	enabled := (capture.SSLConfig{All: a.sslAll, Hosts: splitHosts(a.sslHosts)}).Decrypts(u.Hostname())
	rule := "Disabled — new connections stay encrypted"
	if enabled {
		rule = "Enabled for new connections (not proof of decryption)"
	}
	ui.Text(c, "Decryption rule: "+rule).FontSize(12)
	if !enabled && ui.Button(c, "Enable inspection for "+u.Hostname()).FontSize(11).Clicked() {
		a.decryptHost(u.Hostname())
	}
	ui.Text(c, "Client CA trust: not verified here. A captured request can also come from a client that skips verification.").FontSize(11).TextColor(t.TextMuted)
	var observed *flow
	for i := len(a.flows) - 1; i >= 0; i-- {
		f := &a.flows[i]
		capturedURL, parseErr := url.Parse(f.url)
		if f.live && !f.at.Before(since) && parseErr == nil && capturedURL.Host == u.Host && f.scheme == "https" && f.kind != "connect" && f.reqHeaders != "" {
			observed = f
			break
		}
	}
	if observed == nil {
		ui.Text(c, "Decrypted HTTPS: not observed for this host since opening setup.").FontSize(12).TextColor(t.Warning)
	} else {
		ui.Text(c, fmt.Sprintf("Decrypted HTTPS observed · #%d · %s · %d", observed.id, observed.path, observed.status)).FontSize(12).TextColor(t.Success)
		if ui.Button(c, "Inspect observed request").FontSize(11).Clicked() {
			a.clientUI.open, a.deviceUI.open = false, false
			a.inspectCapture(observed.id)
		}
	}
	ui.Text(c, "Evidence is from any client in this live session, not necessarily this device or runtime. Imported sessions do not count.").FontSize(11).TextColor(t.TextMuted)
}

func (a *app) clientSetupModal(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	if !s.open {
		return
	}
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(800).Height(500).Gap(10).Children(func() {
			ui.Text(c, "Set up a local client").FontSize(17).Bold()
			ui.Row(c).Gap(12).Children(func() {
				ui.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label("Client runtime").Width(150)
				ui.TextInput(c, &s.url).Label("Setup HTTPS URL").Grow(1)
			})
			ui.Row(c).Grow(1).Gap(16).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Grow(1).Gap(8).Children(func() {
					addr := ""
					if a.srv != nil && a.srv.Running() {
						addr = a.srv.Addr()
					}
					ui.Text(c, "1  Process-scoped configuration").Bold()
					ui.Text(c, "Proxy listener: "+cmp.Or(addr, "not running")).FontSize(12)
					ui.Text(c, "The example trusts only the public CA file for this client. No system changes; no insecure TLS bypass. cURL commands use a POSIX shell.").FontSize(11).TextColor(t.TextMuted)
					example := clientExample(s.runtime, addr, a.caPath, s.url)
					if ui.Button(c, "Copy client example").Disabled(addr == "" || a.caPath == "").Clicked() {
						c.Services().WriteClipboard(example)
						a.setMessage("Copied " + s.runtime + " setup; review URL and CA path before running")
					}
					ui.Text(c, example).Font("monospace").FontSize(11).Selectable()
				})
				ui.Scroll(c).Width(310).Gap(8).Children(func() {
					ui.Text(c, "2  Observe, then inspect").Bold()
					if ui.Button(c, "Check local proxy").Disabled(a.srv == nil || a.deviceUI.checkDone != nil).Clicked() {
						a.checkDeviceProxy()
					}
					if a.deviceUI.check != "" {
						ui.Text(c, a.deviceUI.check).FontSize(11)
					}
					a.setupEvidence(c, t, s.url, s.since)
					ui.Text(c, "Run the example yourself after enabling the host. Reconnect after setup changes. Pinning or a private runtime trust store needs client-side changes.").FontSize(11).TextColor(t.TextMuted)
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Phone / tablet setup…").Clicked() {
					s.open = false
					a.openDevices()
				}
				if ui.Button(c, "Close").Clicked() {
					s.open = false
				}
			})
		})
	})
}
