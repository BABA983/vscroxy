package app

import (
	"cmp"
	"github.com/baba983/vscroxy/internal/uikit"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/lan"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// Device access is session-only. Neither consent nor the listener is restored
// from disk, so reopening the app cannot silently expose the proxy to the LAN.
type deviceSetup struct {
	open       bool
	ips        []string
	ip, port   string
	allowed    string
	consent    bool
	askUnknown bool
	platform   int
	problem    string
	gateway    *lan.Gateway
}

func deviceIPs() []string {
	var ips []string
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			// IPv4 link-local works on a direct local network. IPv6 link-
			// local needs a device-specific zone, so do not advertise a URL
			// containing this computer's interface name to another device.
			if ip.IsGlobalUnicast() || (ip.Is4() && ip.IsLinkLocalUnicast()) {
				ips = append(ips, ip.String())
			}
		}
	}
	slices.SortFunc(ips, func(a, b string) int {
		left, right := netip.MustParseAddr(a), netip.MustParseAddr(b)
		if left.IsLinkLocalUnicast() != right.IsLinkLocalUnicast() {
			if left.IsLinkLocalUnicast() {
				return 1
			}
			return -1
		}
		return left.Compare(right) // IPv4 first; link-local only as a fallback
	})
	return slices.Compact(ips)
}

func (a *app) openDevices() {
	d := &a.deviceUI
	if d.gateway == nil {
		d.ips = deviceIPs()
		if !slices.Contains(d.ips, d.ip) {
			d.ip = ""
			if len(d.ips) > 0 {
				d.ip = d.ips[0]
			}
		}
		d.port = cmp.Or(d.port, "7891")
	}
	d.open = true
}

func (a *app) enableDevices() {
	d := &a.deviceUI
	if a.srv == nil || !a.srv.Running() {
		d.problem = locales.StartCaptureProxyFirst()
		return
	}
	if !d.consent {
		d.problem = locales.ConfirmTrustNetwork()
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(d.port))
	if err != nil || port < 1 || port > 65534 {
		d.problem = locales.ChoosePortRange()
		return
	}
	var ask func(netip.Addr) lan.Verdict
	if d.askUnknown {
		ask = a.askDevice
	}
	gateway, err := lan.Start(lan.Config{
		ListenAddr: net.JoinHostPort(d.ip, strconv.Itoa(port)), CertificateAddr: net.JoinHostPort(d.ip, strconv.Itoa(port+1)),
		ProxyAddr: a.srv.Addr(), SOCKSAddr: a.srv.SOCKSAddr(),
		Allowed: strings.Fields(d.allowed), Certificate: a.srv.CACertificate(), Ask: ask,
	})
	if err != nil {
		d.problem = err.Error()
		return
	}
	d.gateway, d.problem = gateway, ""
	a.done(locales.DeviceAccessEnabled(gateway.Addr()))
}

func (a *app) disableDevices() {
	d := &a.deviceUI
	a.answerDeviceAsk(lan.VerdictUndecided)
	if d.gateway != nil {
		err := d.gateway.Close()
		d.gateway = nil
		if err != nil {
			d.problem = err.Error()
		}
	}
	d.consent = false
	a.inform(locales.DeviceAccessOffLocalCapture())
}

// deviceAskTimeout is how long a prompt waits for an answer. A test may
// shorten it; the app always uses a minute.
var deviceAskTimeout = 60 * time.Second

// deviceAskRequest is one unknown device waiting for the reader's answer.
// The gateway's accept loop blocks on reply until the prompt answers or times
// out, so only one device is asked at a time.
type deviceAskRequest struct {
	addr  string
	reply chan lan.Verdict
}

// askDevice runs on the gateway's connection goroutine, never on the UI
// goroutine. It posts the prompt and waits for an explicit answer or a
// timeout; a second device while a prompt is open is refused for this
// connection only, so it can ask again on its next one.
func (a *app) askDevice(addr netip.Addr) lan.Verdict {
	request := &deviceAskRequest{addr: addr.String(), reply: make(chan lan.Verdict, 1)}
	a.deviceAskMu.Lock()
	if a.deviceAskPending != nil {
		a.deviceAskMu.Unlock()
		return lan.VerdictUndecided
	}
	a.deviceAskPending = request
	a.deviceAskMu.Unlock()
	a.dialogs.arrive(dialogDeviceAsk)
	a.invalidate()
	select {
	case verdict := <-request.reply:
		return verdict
	case <-time.After(deviceAskTimeout):
		a.deviceAskMu.Lock()
		if a.deviceAskPending == request {
			a.deviceAskPending = nil
		}
		a.deviceAskMu.Unlock()
		a.invalidate()
		return lan.VerdictUndecided
	}
}

// answerDeviceAsk resolves the open prompt. The UI owns the modal's open
// state; a timeout or Disable leaves it to close itself.
func (a *app) answerDeviceAsk(verdict lan.Verdict) {
	a.deviceAskMu.Lock()
	request := a.deviceAskPending
	a.deviceAskPending = nil
	a.deviceAskMu.Unlock()
	if request != nil {
		select {
		case request.reply <- verdict:
		default:
		}
	}
	a.deviceAskOpen = false
}

// deviceAskModal asks about one unknown device. Dismissing the dialog denies,
// as does the timeout in askDevice.
func (a *app) deviceAskModal(c *ui.Context, t *ui.Theme) {
	a.deviceAskMu.Lock()
	request := a.deviceAskPending
	a.deviceAskMu.Unlock()
	if request == nil {
		a.deviceAskOpen = false
		return
	}
	a.deviceAskOpen = true
	// Only one dialog stacks above the window at a time; the connection
	// prompt is urgent, so the device setup dialog steps aside.
	a.deviceUI.open = false
	uikit.Modal(c, &a.deviceAskOpen, locales.DeviceWantsToConnect(), func() {
		uikit.DialogColumn(c, uikit.DialogSmall).Children(func() {
			uikit.DialogTitle(c, locales.DeviceWantsToConnect())
			uikit.Body(c, locales.DeviceNotAllowed(request.addr))
			uikit.Hint(c, locales.DeviceAskTimeout())
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Deny()).Clicked() {
					a.answerDeviceAsk(lan.VerdictDeny)
				}
				ui.Spacer(c)
				if uikit.PrimaryButton(c, locales.Allow()).Clicked() {
					a.answerDeviceAsk(lan.VerdictAllow)
				}
			})
		})
	})
	if !a.deviceAskOpen {
		a.answerDeviceAsk(lan.VerdictUndecided)
	}
}

func (a *app) devicesModal(c *ui.Context, t *ui.Theme) {
	d := &a.deviceUI
	if !d.open {
		return
	}
	if d.gateway != nil {
		c.After(time.Second)
	}
	_, height := c.Size()
	uikit.Modal(c, &d.open, locales.ConnectADevice2(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(510, height-80)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				uikit.DialogTitle(c, locales.ConnectADevice2())
				ui.Spacer(c)
				if d.gateway == nil {
					uikit.Badge(c, locales.LanOff(), t.TextMuted)
				} else {
					uikit.Badge(c, locales.LanEnabled(), t.Accent)
				}
			})
			uikit.Hint(c, locales.SameWiFiLanOnly())
			ui.Row(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.Space2XL)).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).BasisPercent(44).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceL)).Padding(0, t.Space(uikit.SpaceM), 0, t.Space(uikit.SpaceXS)).Children(func() {
					if d.gateway == nil {
						uikit.Body(c, locales.N1ChooseANetworkAddress()).FontWeight(500)
						ui.Form(c, func() {
							ui.Field(c, locales.Address(), func() {
								uikit.Select(c, &d.ip, d.ips).Label(locales.DeviceFacingAddress())
							})
							ui.Field(c, locales.Port(), func() {
								uikit.Input(c, &d.port).Width(82).Label(locales.LanProxyPort()).Placeholder("7891")
							})
						})
						if len(d.ips) == 0 {
							uikit.Callout(c, uikit.CalloutDanger, locales.NoActiveLanAddressConnect(), nil)
						}
						if ip, err := netip.ParseAddr(d.ip); err == nil && ip.IsLinkLocalUnicast() {
							uikit.Meta(c, locales.LinkLocalAddressTheDevice())
						}
						uikit.Meta(c, locales.ProxyPortAboveTheCertificate())
						uikit.Body(c, locales.N2AllowYourDeviceS()).FontWeight(500)
						uikit.TextArea(c, &d.allowed).Height(74).Font(uikit.FontMono).Radius(uikit.RadiusControl).Label(locales.AllowedDeviceAddresses()).Placeholder("192.168.1.42\nOne IP or CIDR per line")
						uikit.Meta(c, locales.FindItInTheDevice())
						uikit.Checkbox(c, &d.consent, locales.TrustNetworkAndDevices())
						uikit.Checkbox(c, &d.askUnknown, locales.AskWhenUnknownDevice())
						uikit.Meta(c, locales.AskUnknownDeviceHint())
					} else {
						uikit.Hint(c, locales.ProxyAddress())
						uikit.Heading(c, d.gateway.Addr()).Font(uikit.FontMono)
						if uikit.SecondaryButton(c, locales.CopyProxyAddress()).Clicked() {
							c.Services().WriteClipboard(d.gateway.Addr())
						}
						if a.srv != nil && a.srv.SOCKSAddr() != "" {
							uikit.Meta(c, locales.ThisAddressAcceptsHttpAnd())
						} else {
							uikit.Meta(c, locales.ThisAddressAcceptsAnHttp())
						}
						ui.Divider(c)
						uikit.Hint(c, locales.OpenOnYourDeviceTo())
						certificateURL := "http://" + d.gateway.CertificateAddr() + "/vscroxy-ca.cer"
						uikit.Mono(c, certificateURL)
						if uikit.SecondaryButton(c, locales.CopyCertificateUrl()).Clicked() {
							c.Services().WriteClipboard(certificateURL)
						}
						uikit.Meta(c, locales.PublicCertificateOnlyTheSigning())
						ui.Divider(c)
						uikit.Body(c, locales.AllowedSources()).FontWeight(500)
						uikit.Hint(c, d.allowed).Font(uikit.FontMono)
						uikit.Meta(c, locales.DisableAccessBeforeChangingThe())
					}
				})
				ui.Column(c).BasisPercent(56).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
					uikit.Tabs(c, &d.platform, "Device platform", locales.Ios(), locales.Android())
					ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceM)).Padding(0, t.Space(uikit.SpaceM), 0, 0).Children(func() {
						switch d.platform {
						case 0:
							uikit.Heading(c, locales.SetUpIphoneIpad())
							uikit.Prose(c, locales.N1WiFiYourNetwork())
							uikit.Prose(c, locales.N2OpenTheCertificateUrl())
							uikit.Prose(c, locales.N3GeneralAboutCertificateTrust())
							uikit.Prose(c, locales.N4EnableSslProxyingFor())
						case 1:
							uikit.Heading(c, locales.SetUpAndroid())
							uikit.Prose(c, locales.N1EditYourWiFi())
							uikit.Prose(c, locales.N2DownloadTheCertificateUrl())
							uikit.Prose(c, locales.N3EnableSslProxyingFor())
							uikit.Hint(c, locales.ManyAndroid7AppsDo())
						}
					})
					if uikit.SecondaryButton(c, locales.ConfigureHttpsInspection()).Clicked() {
						d.open, a.sslOpen = false, true
					}
				})
			})
			if d.problem != "" {
				uikit.Callout(c, uikit.CalloutDanger, d.problem, nil)
			}
			uikit.DialogFooter(c).Children(func() {
				if d.gateway == nil {
					uikit.Meta(c, locales.OffByDefaultOnEvery())
				} else {
					uikit.Meta(c, locales.ClosingThisDialogKeepsAccess())
				}
				ui.Spacer(c)
				if uikit.SecondaryButton(c, locales.Diagnose()).Clicked() {
					d.open = false
					a.openDiagnostics(nil, diagnoseDevice)
				}
				if uikit.SecondaryButton(c, locales.LocalClientSetup()).Clicked() {
					d.open = false
					a.openClientSetup()
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					d.open = false
				}
				if d.gateway == nil {
					if uikit.PrimaryButton(c, locales.EnableDeviceAccess()).Disabled(a.srv == nil || !d.consent || d.ip == "" || strings.TrimSpace(d.allowed) == "").Clicked() {
						a.enableDevices()
					}
				} else if uikit.SecondaryButton(c, locales.DisableDeviceAccess()).Clicked() {
					a.disableDevices()
				}
			})
		})
	})
}
