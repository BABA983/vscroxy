package main

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type clientSetup struct {
	open    bool
	runtime string
	url     string
	since   time.Time
	scroll  ui.ScrollState
	details bool
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
func (a *app) setupEvidence(c *ui.Context, t *ui.Theme, target string, since time.Time, details *bool) {
	u, err := url.Parse(strings.TrimSpace(target))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		ui.Text(c, "Enter an HTTPS URL without embedded credentials.").FontSize(12).TextColor(t.Warning)
		return
	}
	var observed, latest *flow
	requests, tunnels := 0, 0
	for i := range a.flows {
		f := &a.flows[i]
		capturedURL, parseErr := url.Parse(f.url)
		if !f.live || f.at.Before(since) || parseErr != nil || diagnosticOrigin(capturedURL) != diagnosticOrigin(u) {
			continue
		}
		latest = f
		if f.kind == "connect" {
			tunnels++
		} else if f.reqHeaders != "" {
			requests++
			observed = f
		}
	}
	enabled := a.inspectionConfig().Decrypts(u.Hostname())
	failed := latest != nil && latest.conn.error != ""
	ui.Text(c, "Observed · since opening setup").FontSize(12).Bold()
	switch {
	case latest == nil:
		ui.Text(c, "No target traffic observed yet.").FontSize(12).TextColor(t.TextMuted)
	case failed:
		ui.Text(c, latest.conn.explanation().title).FontSize(12).TextColor(t.Warning)
		ui.Text(c, "This is a recorded failure, not a confirmed certificate diagnosis.").FontSize(11).TextColor(t.TextMuted)
	case observed != nil:
		ui.Text(c, fmt.Sprintf("Decrypted HTTPS observed · #%d · %s · %d", observed.id, observed.path, observed.status)).FontSize(12).TextColor(t.Success)
	default:
		ui.Text(c, "Encrypted tunnel observed; no decrypted request yet.").FontSize(12).TextColor(t.Warning)
	}
	ui.Text(c, "Client CA trust is not verified here.").FontSize(11).TextColor(t.TextMuted)
	ui.Text(c, "Next step").FontSize(12).Bold()
	switch {
	case failed:
		ui.Text(c, "Inspect the recorded failure before changing trust or rules.").FontSize(12)
	case !enabled:
		ui.Text(c, "Enable inspection, then run the client request on a new connection.").FontSize(12)
		if ui.Button(c, "Enable inspection for "+u.Hostname()).FontSize(11).Clicked() {
			a.decryptHost(u.Hostname())
		}
	case observed != nil:
		ui.Text(c, "Inspect the decrypted request.").FontSize(12)
	case latest != nil:
		ui.Text(c, "Verify this client's CA trust, then reconnect and send an HTTPS request.").FontSize(12)
	default:
		ui.Text(c, "Run the client request. If nothing appears, check proxy and bypass settings.").FontSize(12)
	}
	inspect, label := latest, "Inspect latest connection"
	if observed != nil && !failed {
		inspect, label = observed, "Inspect observed request"
	}
	if inspect != nil && ui.Button(c, label).FontSize(11).Tooltip("Inspect the recorded exchange without clearing the current view's filters").Clicked() {
		a.clientUI.open, a.deviceUI.open = false, false
		a.inspectCapture(inspect.id)
	}
	ui.Collapsible(c, "Evidence and limitations", details, func() {
		ui.Text(c, fmt.Sprintf("%d decrypted HTTPS requests · %d CONNECT records", requests, tunnels)).FontSize(12)
		if failed {
			ui.Text(c, latest.conn.error).FontSize(12).Selectable()
			if observed != nil {
				ui.Text(c, fmt.Sprintf("Earlier decrypted request · #%d · %s · %d", observed.id, observed.path, observed.status)).FontSize(12)
				if ui.Button(c, "Inspect observed request").FontSize(11).Clicked() {
					a.clientUI.open, a.deviceUI.open = false, false
					a.inspectCapture(observed.id)
				}
			}
		}
		rule := "Disabled — new connections stay encrypted"
		if enabled {
			rule = "Enabled for new connections (not proof of decryption)"
		}
		ui.Text(c, "Decryption rule: "+rule).FontSize(12)
		if failed && !enabled && ui.Button(c, "Enable inspection for "+u.Hostname()).FontSize(11).Clicked() {
			a.decryptHost(u.Hostname())
		}
		ui.Text(c, "Client CA trust: not verified here. A captured request can also come from a client that skips verification.").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "Evidence is from any client in this live session, not necessarily this device or runtime. Imported sessions do not count.").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "No traffic? Check proxy address, bypass rules and ignored hosts/apps. Reconnect after setup changes. Pinning or a private runtime trust store needs client-side changes.").FontSize(11).TextColor(t.TextMuted)
	}).FontSize(12)
}

func (a *app) clientSetupModal(c *ui.Context, t *ui.Theme) {
	s := &a.clientUI
	if !s.open {
		return
	}
	appModal(c, &s.open, func() {
		ui.Column(c).Width(800).Height(500).Gap(10).Children(func() {
			dialogTitle(c, "Set up a local client")
			ui.Row(c).Gap(12).Children(func() {
				ui.Select(c, &s.runtime, []string{"cURL", "Node.js", "Python", "Go"}).Label("Client runtime").Width(150)
				if ui.TextInput(c, &s.url).Label("Setup HTTPS URL").Grow(1).Changed() {
					s.scroll.Y = 0
				}
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
				ui.Column(c).Width(310).Gap(8).Children(func() {
					ui.Text(c, "2  Observe, then inspect").Bold()
					ui.Scroll(c).TrackScroll(&s.scroll).Grow(1).Gap(8).Padding(0, 8, 8, 0).Children(func() {
						if ui.Button(c, "Check local proxy").Disabled(a.srv == nil || !a.srv.Running() || a.deviceUI.checkDone != nil).Clicked() {
							a.checkDeviceProxy()
						}
						if a.deviceUI.check != "" {
							ui.Text(c, a.deviceUI.check).FontSize(11)
						}
						a.setupEvidence(c, t, s.url, s.since, &s.details)
					})
					if s.scroll.Y < s.scroll.MaxY-1 {
						ui.Text(c, "More evidence below ↓").FontSize(11).TextColor(t.TextMuted)
					}
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Phone / tablet setup…").Clicked() {
					s.open = false
					a.openDevices()
				}
				if ui.Button(c, "Capture diagnostics…").Clicked() {
					s.open = false
					a.openDiagnostics()
					a.diagnostics.target = s.url
				}
				if ui.Button(c, "Close").Clicked() {
					s.open = false
				}
			})
		})
	})
}
