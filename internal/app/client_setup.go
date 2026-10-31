package app

import (
	"cmp"
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
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
		ui.Text(c, "Enter an HTTPS URL without embedded credentials.").FontSize(uikit.TextBody).TextColor(t.Warning)
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
	ui.Text(c, "Observed · since opening setup").FontSize(uikit.TextBody).Bold()
	switch {
	case latest == nil:
		ui.Text(c, "No target traffic observed yet.").FontSize(uikit.TextBody).TextColor(t.TextMuted)
	case failed:
		ui.Text(c, latest.conn.explanation().title).FontSize(uikit.TextBody).TextColor(t.Warning)
		ui.Text(c, "This is a recorded failure, not a confirmed certificate diagnosis.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
	case observed != nil:
		ui.Text(c, fmt.Sprintf("Decrypted HTTPS observed · #%d · %s · %d", observed.id, observed.path, observed.status)).FontSize(uikit.TextBody).TextColor(t.Success)
	default:
		ui.Text(c, "Encrypted tunnel observed; no decrypted request yet.").FontSize(uikit.TextBody).TextColor(t.Warning)
	}
	ui.Text(c, "Client CA trust is not verified here.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
	ui.Text(c, "Next step").FontSize(uikit.TextBody).Bold()
	switch {
	case failed:
		ui.Text(c, "Inspect the recorded failure before changing trust or rules.").FontSize(uikit.TextBody)
	case !enabled:
		ui.Text(c, "Enable inspection, then run the client request on a new connection.").FontSize(uikit.TextBody)
		if uikit.SecondaryButton(c, "Enable inspection for "+u.Hostname()).Clicked() {
			a.decryptHost(u.Hostname())
		}
	case observed != nil:
		ui.Text(c, "Inspect the decrypted request.").FontSize(uikit.TextBody)
	case latest != nil:
		ui.Text(c, "Verify this client's CA trust, then reconnect and send an HTTPS request.").FontSize(uikit.TextBody)
	default:
		ui.Text(c, "Run the client request. If nothing appears, check proxy and bypass settings.").FontSize(uikit.TextBody)
	}
	inspect, label := latest, "Inspect latest connection"
	if observed != nil && !failed {
		inspect, label = observed, "Inspect observed request"
	}
	if inspect != nil && uikit.SecondaryButton(c, label).Tooltip("Inspect the recorded exchange without clearing the current view's filters").Clicked() {
		a.clientUI.open, a.deviceUI.open = false, false
		a.inspectCapture(inspect.id)
	}
	ui.Collapsible(c, "Evidence and limitations", details, func() {
		ui.Text(c, fmt.Sprintf("%d decrypted HTTPS requests · %d CONNECT records", requests, tunnels)).FontSize(uikit.TextBody)
		if failed {
			ui.Text(c, latest.conn.error).Font(uikit.FontMono).FontSize(uikit.TextBody).Selectable()
			if observed != nil {
				ui.Text(c, fmt.Sprintf("Earlier decrypted request · #%d · %s · %d", observed.id, observed.path, observed.status)).FontSize(uikit.TextBody)
				if uikit.SecondaryButton(c, "Inspect observed request").Clicked() {
					a.clientUI.open, a.deviceUI.open = false, false
					a.inspectCapture(observed.id)
				}
			}
		}
		rule := "Disabled — new connections stay encrypted"
		if enabled {
			rule = "Enabled for new connections (not proof of decryption)"
		}
		ui.Text(c, "Decryption rule: "+rule).FontSize(uikit.TextBody)
		if failed && !enabled && uikit.SecondaryButton(c, "Enable inspection for "+u.Hostname()).Clicked() {
			a.decryptHost(u.Hostname())
		}
		ui.Text(c, "Client CA trust: not verified here. A captured request can also come from a client that skips verification.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
		ui.Text(c, "Evidence is from any client in this live session, not necessarily this device or runtime. Imported sessions do not count.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
		ui.Text(c, "No traffic? Check proxy address, bypass rules and ignored hosts/apps. Reconnect after setup changes. Pinning or a private runtime trust store needs client-side changes.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
	}).FontSize(uikit.TextBody)
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
					if uikit.Input(c, &s.url).Label("Setup HTTPS URL").Changed() {
						s.scroll.Y = 0
					}
				})
			})
			ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(6)).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Grow(1).MinWidth(0).Gap(t.Space(3)).Padding(0, t.Space(2), 0, 0).Children(func() {
					addr := ""
					if a.srv != nil && a.srv.Running() {
						addr = a.srv.Addr()
					}
					ui.Text(c, "1  Process-scoped configuration").FontSize(uikit.TextBody).Bold()
					ui.Text(c, "Proxy listener: "+cmp.Or(addr, "not running")).FontSize(uikit.TextBody)
					ui.Text(c, "The example trusts only the public CA file for this client. No system changes; no insecure TLS bypass. cURL commands use a POSIX shell.").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
					example := clientExample(s.runtime, addr, a.caPath, s.url)
					if uikit.SecondaryButton(c, "Copy client example").Disabled(addr == "" || a.caPath == "").Clicked() {
						c.Services().WriteClipboard(example)
						a.setMessage("Copied " + s.runtime + " setup; review URL and CA path before running")
					}
					ui.Text(c, example).Font(uikit.FontMono).FontSize(uikit.TextMeta).Selectable()
				})
				ui.Column(c).Width(340).MinWidth(0).Gap(t.Space(2)).Children(func() {
					ui.Text(c, "2  Observe, then inspect").FontSize(uikit.TextBody).Bold()
					ui.Scroll(c).TrackScroll(&s.scroll).Grow(1).MinHeight(0).Gap(t.Space(2)).Padding(0, t.Space(2), t.Space(2), 0).Children(func() {
						if uikit.SecondaryButton(c, "Check local proxy").Disabled(a.srv == nil || !a.srv.Running() || a.deviceUI.checkDone != nil).Clicked() {
							a.checkDeviceProxy()
						}
						if a.deviceUI.check != "" {
							ui.Text(c, a.deviceUI.check).FontSize(uikit.TextMeta)
						}
						a.setupEvidence(c, t, s.url, s.since, &s.details)
					})
					if s.scroll.Y < s.scroll.MaxY-1 {
						ui.Text(c, "More evidence below ↓").FontSize(uikit.TextMeta).TextColor(t.TextMuted)
					}
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Phone / tablet setup…").Clicked() {
					s.open = false
					a.openDevices()
				}
				if uikit.SecondaryButton(c, "Capture diagnostics…").Clicked() {
					s.open = false
					a.openDiagnostics()
					a.diagnostics.target = s.url
				}
				if uikit.SecondaryButton(c, "Close").Clicked() {
					s.open = false
				}
			})
		})
	})
}
