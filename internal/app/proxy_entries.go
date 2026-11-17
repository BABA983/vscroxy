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
	"github.com/baba983/vscroxy/locales"
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
		if a.socksPassword == "" {
			return capture.SOCKSConfig{}, fmt.Errorf("SOCKS5 authentication needs a password")
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
			a.socksProblem = locales.EnterPortRange()
			return
		}
		port = n
		if port == a.proxyPort {
			a.socksProblem = locales.PortUsedHTTPProxy(port)
			return
		}
		if a.srv != nil {
			if p, err := strconv.Atoi(portOfAddr(a.srv.Addr())); err == nil && p == port {
				a.socksProblem = locales.PortUsedHTTPProxy(port)
				return
			}
		}
		for _, r := range a.reverses {
			if r.Port == port {
				a.socksProblem = locales.PortUsedReverseProxy(port)
				return
			}
		}
	}
	user, pass := "", ""
	if a.socksAuthDraft {
		user = strings.TrimSpace(a.socksUserDraft)
		pass = a.socksPassDraft
		if user == "" {
			a.socksProblem = locales.EnterUsernameOrOff()
			return
		}
		if pass == "" {
			a.socksProblem = locales.EnterPasswordOrOff()
			return
		}
		if len(user) > 255 || len(pass) > 255 {
			a.socksProblem = locales.CredentialsMax255()
			return
		}
	}
	if a.settingsPath == "" {
		a.socksProblem = locales.SocksLocationUnavailable()
		return
	}
	previous := a.socksEntry()
	a.socksOn, a.socksPort, a.socksAuth, a.socksUsername, a.socksPassword = a.socksOnDraft, port, a.socksAuthDraft, user, pass
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil {
		a.socksOn, a.socksPort, a.socksAuth, a.socksUsername, a.socksPassword = previous.on, previous.port, previous.auth, previous.username, previous.password
		a.socksProblem = locales.SocksSettingsNotSaved(err.Error())
		return
	}
	a.resetSOCKSDrafts()
	a.done(locales.Socks5SettingsSavedRestartThe())
}

func (a *app) socksSettings(c *ui.Context, t *ui.Theme) {
	uikit.SettingsCard(c, locales.Socks5(), func() {
		ui.Row(c).FillWidth().MinHeight(uikit.SettingsRowHeight).Padding(t.Space(uikit.SpaceXS), t.Space(uikit.SpaceXL)).
			Gap(t.Space(uikit.SpaceXL)).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
			Children(func() {
				left := ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceXS))
				left.Children(func() {
					uikit.Body(c, locales.Enable())
					uikit.Small(c, locales.ASecondEntryForClients()).TextColor(t.TextMuted)
					err := a.srvError()
					switch {
					case a.srv != nil && a.srv.SOCKSAddr() != "":
						uikit.Mono(c, a.srv.SOCKSAddr()).Selectable().TextColor(t.TextMuted)
					case err != nil:
						uikit.ErrorText(c, err.Error())
					case a.socksDraft() != a.socksEntry():
						uikit.Hint(c, locales.NotSaved())
					case a.socksEntry() != a.launchSOCKS:
						uikit.Hint(c, locales.RestartToApply())
					default:
						uikit.Hint(c, locales.Off())
					}
				})
				uikit.SettingsSwitch(c, &a.socksOnDraft, locales.EnableSocks5Proxy())
			})
		if a.socksOnDraft {
			settingsCardBlock(c, locales.Port(), func() ui.Element {
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					uikit.Input(c, &a.socksPortInput).Label(locales.Socks5Port()).Placeholder(locales.Automatic()).Width(100).
						Invalid(a.socksProblem != "")
					if a.socksProblem != "" {
						uikit.ErrorText(c, a.socksProblem)
					}
				})
			}, locales.BlankIsAutomaticAnOs())
			settingsCardRow(c, locales.Authentication(), func() ui.Element {
				return uikit.SettingsSwitch(c, &a.socksAuthDraft, locales.RequireUsernameAndPassword())
			}, locales.Rfc1929WithoutItThe())
			if a.socksAuthDraft {
				settingsCardRow(c, locales.Username(), func() ui.Element {
					return uikit.Input(c, &a.socksUserDraft).Label(locales.Socks5Username()).Width(220)
				}, "")
				settingsCardRow(c, locales.Password(), func() ui.Element {
					return uikit.Input(c, &a.socksPassDraft).Label(locales.Socks5Password()).Password().Width(220)
				}, "")
			}
		}
		settingsCardBlock(c, "", func() ui.Element {
			return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, locales.SaveSocks5Settings()).Clicked() {
					a.saveSOCKS()
				}
				restartPending(c, a.socksEntry() != a.launchSOCKS)
			})
		}, "")
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
	insecure bool
	problem  string
}

func (a *app) reverseConfigs() []capture.ReverseProxy {
	out := make([]capture.ReverseProxy, len(a.reverses))
	for i, r := range a.reverses {
		out[i] = capture.ReverseProxy{ID: r.ID, Port: r.Port, Target: r.Target, KeepHost: r.KeepHost, Enabled: r.Enabled, Insecure: r.Insecure}
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
			a.fail(locales.ReverseProxy(), err)
		}
	}
	a.lastSave = time.Time{}
	if err := a.persistSettings(); err != nil && a.reverseProblem == "" {
		a.reverseProblem = locales.SettingsNotSaved(err.Error())
	}
	a.invalidate()
}

func (a *app) openReverseProxy(id string) {
	d := reverseProxyDraft{open: true, editing: id}
	if id != "" {
		for _, r := range a.reverses {
			if r.ID == id {
				d.port, d.target, d.keepHost, d.insecure = strconv.Itoa(r.Port), r.Target, r.KeepHost, r.Insecure
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
		d.problem = locales.EnterAPortFrom1024()
		return
	}
	if used, ok := a.reversePortOwner(port, d.editing); ok {
		d.problem = fmt.Sprintf("Port %d is already used by %s", port, used)
		return
	}
	target := strings.TrimSpace(d.target)
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		d.problem = locales.EnterAnHttpOrHttps2()
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		d.problem = "The remote URL must start with http:// or https://"
		return
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		d.problem = locales.TheRemoteUrlMustNot()
		return
	}
	entry := reverseProxySetting{ID: d.editing, Port: port, Target: target, KeepHost: d.keepHost, Enabled: true, Insecure: d.insecure}
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
	uikit.SettingsCard(c, locales.ReverseProxies(), func() {
		status := map[string]capture.ReverseProxyStatus{}
		if a.srv != nil {
			status = a.srv.ReverseProxyStatus()
		}
		if len(a.reverses) == 0 {
			settingsCardBlock(c, "", func() ui.Element {
				return uikit.Hint(c, locales.NoReverseProxiesALocal())
			}, "")
		}
		for _, r := range slices.Clone(a.reverses) {
			st := status[r.ID]
			mode := "Remote host"
			if r.KeepHost {
				mode = locales.KeepOriginalHost()
			}
			tlsMode := "Verify TLS"
			if r.Insecure {
				tlsMode = locales.SelfSignedAllowed()
			}
			state := "Off"
			if r.Enabled {
				switch {
				case st.Error != "":
					state = locales.NotListening()
				case st.Addr != "":
					state = "Listening on " + st.Addr
				case a.srv == nil || !a.srv.Running():
					state = locales.CaptureIsNotRunning()
				default:
					state = "Starting…"
				}
			}
			settingsCardBlock(c, locales.PortNumber(r.Port), func() ui.Element {
				return ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Children(func() {
					ui.Row(c).Gap(t.Space(uikit.SpaceM)).Wrap().AlignItems(ui.Center).Children(func() {
						enabled := r.Enabled
						if uikit.SettingsSwitch(c, &enabled, "Reverse proxy on port "+strconv.Itoa(r.Port)).Changed() {
							a.setReverseEnabled(r.ID, enabled)
						}
						uikit.Tip(c, uikit.Mono(c, r.Target).SingleLine().MaxWidth(280).TextColor(t.TextMuted), r.Target)
						if uikit.SecondaryButton(c, locales.Edit()).Clicked() {
							a.openReverseProxy(r.ID)
						}
						uikit.MoreMenu(c, locales.ReverseProxyActions(), func(m *ui.Menu) {
							actionMenu(m, []action{do("proxy.reverse.remove", locales.Remove(), func() { a.removeReverseProxy(r.ID) }).in("danger")})
						})
					})
					if st.Error != "" && r.Enabled {
						uikit.ErrorText(c, st.Error)
					}
				})
			}, mode+" · "+tlsMode+" · "+state)
		}
		settingsCardBlock(c, "", func() ui.Element {
			return ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
				if uikit.SecondaryButton(c, locales.AddReverseProxy()).Clicked() {
					a.openReverseProxy("")
				}
				if a.reverseProblem != "" {
					uikit.ErrorText(c, a.reverseProblem)
				}
			})
		}, locales.TheLocalPathIsAppended())
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
		a.inform(locales.ReverseProxyRemoved())
	}
}

func (a *app) reverseProxyModal(c *ui.Context, t *ui.Theme) {
	d := &a.reverseDraft
	if !d.open {
		return
	}
	title := locales.AddReverseProxyItem()
	if d.editing != "" {
		title = locales.EditReverseProxy()
	}
	uikit.Modal(c, &d.open, title, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Scroll(c).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
				uikit.FormRow(c, locales.LocalPort(), locales.TheLoopbackPortClientsConnect(), d.problem, func() {
					uikit.InputState(c, &d.port, d.problem != "").Label(locales.LocalPort()).Placeholder("9000").Width(uikit.WidthNarrow)
				})
				ui.Field(c, locales.RemoteUrl(), func() {
					uikit.Input(c, &d.target).Label(locales.RemoteUrl()).Placeholder("https://api.example.com")
				}).Description(locales.TheBaseUrlRequestsAre())
				ui.Field(c, locales.HostHeader(), func() {
					choice := 0
					if d.keepHost {
						choice = 1
					}
					uikit.Segmented(c, &choice, locales.RemoteHost(), locales.KeepOriginal()).Label(locales.HostHeader())
					d.keepHost = choice == 1
				})
				uikit.Meta(c, locales.RemoteHostSendsTheTarget())
				ui.Field(c, locales.RemoteCertificate(), func() {
					uikit.Switch(c, &d.insecure).Label(locales.AllowSelfSignedCertificates())
				}).Description(locales.OffByDefaultTheApp())
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					d.open = false
				}
				if uikit.PrimaryButton(c, locales.Save()).Clicked() {
					a.saveReverseProxy()
				}
			})
		})
	})
}
