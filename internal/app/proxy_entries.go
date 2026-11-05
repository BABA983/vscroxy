package app

// The extra proxy entries: a SOCKS5 listener for clients that cannot use an
// HTTP proxy, and reverse proxies that forward a loopback port to a remote
// base URL. Both feed the same capture engine. SOCKS5 is configured for the
// next launch like the HTTP port; reverse proxies apply as they are edited.

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
)

// socksEntry is the SOCKS5 configuration as a comparable value, for the
// launch snapshot that decides whether a restart is pending.
type socksEntry struct {
	on       bool
	port     int
	auth     bool
	username string
	password string
}

func (a *app) socksEntry() socksEntry {
	return socksEntry{on: a.socksOn, port: a.socksPort, auth: a.socksAuth, username: a.socksUsername, password: a.socksPassword}
}

// socksDraft is the settings page's unsaved SOCKS5 values. An unparsable
// port reads as zero so it still counts as a change.
func (a *app) socksDraft() socksEntry {
	port := 0
	if n, err := strconv.Atoi(strings.TrimSpace(a.socksPortInput)); err == nil {
		port = n
	}
	user, pass := "", ""
	if a.socksAuthDraft {
		user, pass = a.socksUserDraft, a.socksPassDraft
	}
	return socksEntry{on: a.socksOnDraft, port: port, auth: a.socksAuthDraft, username: user, password: pass}
}

// resetSOCKSDrafts copies the persisted SOCKS5 configuration into the fields
// the settings page edits, discarding unsaved edits.
func (a *app) resetSOCKSDrafts() {
	a.socksOnDraft, a.socksAuthDraft = a.socksOn, a.socksAuth
	a.socksUserDraft, a.socksPassDraft = a.socksUsername, a.socksPassword
	a.socksPortInput = ""
	if a.socksPort != 0 {
		a.socksPortInput = strconv.Itoa(a.socksPort)
	}
}

// socksConfig turns the persisted SOCKS5 settings into what capture needs.
func (a *app) socksConfig() (capture.SOCKSConfig, error) {
	if !a.socksOn {
		return capture.SOCKSConfig{}, nil
	}
	addr := "127.0.0.1:0"
	if a.socksPort != 0 {
		if a.socksPort < 1024 || a.socksPort > 65535 {
			return capture.SOCKSConfig{}, fmt.Errorf("SOCKS5 port must be between 1024 and 65535, or empty for automatic selection")
		}
		addr = "127.0.0.1:" + strconv.Itoa(a.socksPort)
	}
	user, pass := "", ""
	if a.socksAuth {
		if a.socksUsername == "" {
			return capture.SOCKSConfig{}, fmt.Errorf("SOCKS5 authentication needs a username")
		}
		if len(a.socksUsername) > 255 || len(a.socksPassword) > 255 {
			return capture.SOCKSConfig{}, fmt.Errorf("SOCKS5 username and password must each be at most 255 bytes")
		}
		user, pass = a.socksUsername, a.socksPassword
	}
	return capture.SOCKSConfig{Enabled: true, Addr: addr, Username: user, Password: pass}, nil
}

// saveSOCKS validates the draft and saves it for the next launch. The running
// listener is not touched, like the HTTP port.
func (a *app) saveSOCKS() {
	a.socksProblem = ""
	port := 0
	if input := strings.TrimSpace(a.socksPortInput); input != "" {
		n, err := strconv.Atoi(input)
		if err != nil || n < 1024 || n > 65535 {
			a.socksProblem = "Enter a port from 1024 to 65535, or leave empty for automatic selection"
			return
		}
		port = n
		if port == a.proxyPort {
			a.socksProblem = fmt.Sprintf("Port %d is already used by the HTTP proxy", port)
			return
		}
		if a.srv != nil {
			if p, err := strconv.Atoi(portOfAddr(a.srv.Addr())); err == nil && p == port {
				a.socksProblem = fmt.Sprintf("Port %d is already used by the HTTP proxy", port)
				return
			}
		}
		for _, r := range a.reverses {
			if r.Port == port {
				a.socksProblem = fmt.Sprintf("Port %d is already used by a reverse proxy", port)
				return
			}
		}
	}
	user, pass := "", ""
	if a.socksAuthDraft {
		user = strings.TrimSpace(a.socksUserDraft)
		pass = a.socksPassDraft
		if user == "" {
			a.socksProblem = "Enter a username, or turn authentication off"
			return
		}
		if len(user) > 255 || len(pass) > 255 {
			a.socksProblem = "Username and password must each be at most 255 bytes"
			return
		}
	}
	if a.settingsPath == "" {
		a.socksProblem = "Settings location is unavailable; SOCKS5 settings were not saved"
		return
	}
	previous := a.socksEntry()
	a.socksOn, a.socksPort, a.socksAuth, a.socksUsername, a.socksPassword = a.socksOnDraft, port, a.socksAuthDraft, user, pass
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		a.socksOn, a.socksPort, a.socksAuth, a.socksUsername, a.socksPassword = previous.on, previous.port, previous.auth, previous.username, previous.password
		a.socksProblem = "SOCKS5 settings were not saved: " + err.Error()
		return
	}
	a.resetSOCKSDrafts()
	a.done("SOCKS5 settings saved · restart the app to apply")
}

func (a *app) socksSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsGroup(c, "SOCKS5", func() {
		ui.Field(c, "Enable", func() {
			ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Switch(c, &a.socksOnDraft).Label("Enable SOCKS5 proxy")
				if a.srv != nil && a.srv.SOCKSAddr() != "" {
					uikit.Mono(c, a.srv.SOCKSAddr()).Selectable().TextColor(t.TextMuted)
				} else if err := a.srvError(); err != nil {
					uikit.Hint(c, err.Error()).TextColor(t.Danger)
				} else if a.socksDraft() != a.socksEntry() {
					uikit.Hint(c, "Not saved")
				} else if a.socksEntry() != a.launchSOCKS {
					uikit.Hint(c, "Restart to apply")
				} else {
					uikit.Hint(c, "Off")
				}
			})
		}).Description("A second entry for clients that cannot use an HTTP proxy. Plaintext HTTP and HTTPS with the same decryption rules. Applies after a restart.")
		if a.socksOnDraft {
			ui.Field(c, "Port", func() {
				uikit.Input(c, &a.socksPortInput).Label("SOCKS5 port").Placeholder("Automatic").Width(100)
			}).Description("Blank is automatic (an OS-assigned loopback port shown above). A fixed port (1024–65535) never falls back.").Error(a.socksProblem)
			ui.Field(c, "Authentication", func() {
				ui.Switch(c, &a.socksAuthDraft).Label("Require username and password")
			}).Description("RFC 1929. Without it the entry accepts any local client that can reach the port.")
			if a.socksAuthDraft {
				ui.Field(c, "Username", func() {
					uikit.Input(c, &a.socksUserDraft).Label("SOCKS5 username").Width(220)
				})
				ui.Field(c, "Password", func() {
					uikit.Input(c, &a.socksPassDraft).Label("SOCKS5 password").Password().Width(220)
				})
			}
		}
		ui.Field(c, "", func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, "Save SOCKS5 settings").Clicked() {
					a.saveSOCKS()
				}
				restartPending(c, a.socksEntry() != a.launchSOCKS)
			})
		})
	})
}

// srvError reports a configured SOCKS5 listener that could not start.
func (a *app) srvError() error {
	if a.srv == nil {
		return nil
	}
	return a.srv.SOCKSError()
}

// reverseProxyDraft is the add or edit dialog.
type reverseProxyDraft struct {
	open     bool
	editing  string // ID; empty for a new entry
	port     string
	target   string
	keepHost bool
	problem  string
}

func (a *app) reverseConfigs() []capture.ReverseProxy {
	out := make([]capture.ReverseProxy, len(a.reverses))
	for i, r := range a.reverses {
		out[i] = capture.ReverseProxy{ID: r.ID, Port: r.Port, Target: r.Target, KeepHost: r.KeepHost, Enabled: r.Enabled}
	}
	return out
}

// applyReverseProxies hands the saved entries to capture and persists them.
// Editing one entry restarts the listeners, so a port that moved takes effect
// at once; a port that cannot be bound is shown on its row and in a toast.
func (a *app) applyReverseProxies() {
	a.reverseProblem = ""
	if a.srv != nil {
		if err := a.srv.SetReverseProxies(a.reverseConfigs()); err != nil {
			a.reverseProblem = err.Error()
			a.fail("Reverse proxy", err)
		}
	}
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil && a.reverseProblem == "" {
		a.reverseProblem = "Settings were not saved: " + err.Error()
	}
	a.invalidate()
}

func (a *app) openReverseProxy(id string) {
	d := reverseProxyDraft{open: true, editing: id}
	if id != "" {
		for _, r := range a.reverses {
			if r.ID == id {
				d.port, d.target, d.keepHost = strconv.Itoa(r.Port), r.Target, r.KeepHost
				break
			}
		}
	}
	a.reverseDraft = d
}

func (a *app) saveReverseProxy() {
	d := &a.reverseDraft
	port, err := strconv.Atoi(strings.TrimSpace(d.port))
	if err != nil || port < 1024 || port > 65535 {
		d.problem = "Enter a port from 1024 to 65535"
		return
	}
	if used, ok := a.reversePortOwner(port, d.editing); ok {
		d.problem = fmt.Sprintf("Port %d is already used by %s", port, used)
		return
	}
	target := strings.TrimSpace(d.target)
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		d.problem = "Enter an http:// or https:// URL"
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		d.problem = "The remote URL must start with http:// or https://"
		return
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		d.problem = "The remote URL must not carry credentials, a query or a fragment"
		return
	}
	entry := reverseProxySetting{ID: d.editing, Port: port, Target: target, KeepHost: d.keepHost, Enabled: true}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
		a.reverses = append(a.reverses, entry)
	} else {
		found := false
		for i := range a.reverses {
			if a.reverses[i].ID == entry.ID {
				entry.Enabled = a.reverses[i].Enabled
				a.reverses[i] = entry
				found = true
				break
			}
		}
		if !found {
			a.reverses = append(a.reverses, entry)
		}
	}
	d.open = false
	a.applyReverseProxies()
}

// reversePortOwner says what already uses port, excluding the entry being
// edited. It checks the saved entries, the running HTTP and SOCKS5 listeners,
// and the configured next-launch ports.
func (a *app) reversePortOwner(port int, except string) (string, bool) {
	for _, r := range a.reverses {
		if r.ID != except && r.Port == port {
			return "another reverse proxy", true
		}
	}
	if a.proxyPort == port {
		return "the HTTP proxy", true
	}
	if a.socksOn && a.socksPort == port {
		return "the SOCKS5 proxy", true
	}
	if a.srv != nil {
		if p, err := strconv.Atoi(portOfAddr(a.srv.Addr())); err == nil && p == port {
			return "the HTTP proxy", true
		}
		if p, err := strconv.Atoi(portOfAddr(a.srv.SOCKSAddr())); err == nil && p == port {
			return "the SOCKS5 proxy", true
		}
	}
	return "", false
}

func portOfAddr(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}

func (a *app) reverseProxySettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsGroup(c, "Reverse proxies", func() {
		status := map[string]capture.ReverseProxyStatus{}
		if a.srv != nil {
			status = a.srv.ReverseProxyStatus()
		}
		if len(a.reverses) == 0 {
			uikit.Hint(c, "No reverse proxies. A local port forwards plaintext HTTP to a remote base URL and is captured like any other request.")
		}
		for _, r := range slices.Clone(a.reverses) {
			st := status[r.ID]
			mode := "Remote host"
			if r.KeepHost {
				mode = "Keep original Host"
			}
			state := "Off"
			if r.Enabled {
				switch {
				case st.Error != "":
					state = "Not listening"
				case st.Addr != "":
					state = "Listening on " + st.Addr
				case a.srv == nil || !a.srv.Running():
					state = "Capture is not running"
				default:
					state = "Starting…"
				}
			}
			field := ui.Field(c, fmt.Sprintf("Port %d", r.Port), func() {
				ui.Row(c).Gap(t.Space(2)).Wrap().AlignItems(ui.Center).Children(func() {
					enabled := r.Enabled
					if ui.Switch(c, &enabled).Label("Reverse proxy on port " + strconv.Itoa(r.Port)).Changed() {
						a.setReverseEnabled(r.ID, enabled)
					}
					uikit.Mono(c, r.Target).SingleLine().MaxWidth(280).TextColor(t.TextMuted).Tooltip(r.Target)
					if uikit.SecondaryButton(c, "Edit…").Clicked() {
						a.openReverseProxy(r.ID)
					}
					if uikit.SecondaryButton(c, "Remove").TextColor(t.Danger).Clicked() {
						a.removeReverseProxy(r.ID)
					}
				})
			}).Description(mode + " · " + state)
			if st.Error != "" && r.Enabled {
				field = field.Error(st.Error)
			}
		}
		ui.Field(c, "", func() {
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, "Add reverse proxy…").Clicked() {
					a.openReverseProxy("")
				}
				if a.reverseProblem != "" {
					uikit.Hint(c, a.reverseProblem).TextColor(t.Danger)
				}
			})
		}).Description("The local path is appended to the remote URL. Requests go through the same rules, scripts and capture; HTTPS remotes are reached with TLS by the app.")
	})
}

func (a *app) setReverseEnabled(id string, on bool) {
	for i := range a.reverses {
		if a.reverses[i].ID == id {
			a.reverses[i].Enabled = on
			a.applyReverseProxies()
			return
		}
	}
}

func (a *app) removeReverseProxy(id string) {
	before := len(a.reverses)
	a.reverses = slices.DeleteFunc(a.reverses, func(r reverseProxySetting) bool { return r.ID == id })
	if len(a.reverses) != before {
		a.applyReverseProxies()
		a.inform("Reverse proxy removed")
	}
}

func (a *app) reverseProxyModal(c *ui.Context, t *ui.Theme) {
	d := &a.reverseDraft
	if !d.open {
		return
	}
	title := "Add reverse proxy"
	if d.editing != "" {
		title = "Edit reverse proxy"
	}
	uikit.Modal(c, &d.open, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Scroll(c).MinHeight(0).Gap(t.Space(3)).Children(func() {
				ui.Field(c, "Local port", func() {
					uikit.Input(c, &d.port).Label("Local port").Placeholder("9000").Width(120)
				}).Description("The loopback port clients connect to.")
				ui.Field(c, "Remote URL", func() {
					uikit.Input(c, &d.target).Label("Remote URL").Placeholder("https://api.example.com")
				}).Description("The base URL requests are forwarded to. The local path is appended.")
				ui.Field(c, "Host header", func() {
					choice := 0
					if d.keepHost {
						choice = 1
					}
					uikit.Segmented(c, &choice, "Remote host", "Keep original").Label("Host header")
					d.keepHost = choice == 1
				})
				uikit.Meta(c, "Remote host sends the target's authority. Keep original forwards the Host the local client sent.")
				if d.problem != "" {
					ui.Text(c, d.problem).FontSize(uikit.TextBody).TextColor(t.Danger)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					d.open = false
				}
				if uikit.PrimaryButton(c, "Save").Clicked() {
					a.saveReverseProxy()
				}
			})
		})
	})
}
