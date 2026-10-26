package main

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/lan"
	"github.com/egoist/mygo/ui"
)

// Device access is session-only. Neither consent nor the listener is restored
// from disk, so reopening the app cannot silently expose the proxy to the LAN.
type deviceSetup struct {
	open      bool
	ips       []string
	ip, port  string
	allowed   string
	consent   bool
	platform  int
	problem   string
	gateway   *lan.Gateway
	check     string
	checkDone chan string
	since     time.Time
	target    string
	details   bool
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
	d.since = time.Now()
	d.details = false
	d.target = cmp.Or(d.target, "https://example.com/")
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
		d.problem = "Start the local capture proxy successfully before enabling device access"
		return
	}
	if !d.consent {
		d.problem = "Confirm that you trust this network and the allowed devices"
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(d.port))
	if err != nil || port < 1 || port > 65534 {
		d.problem = "Choose a proxy port between 1 and 65534; the certificate service uses the next port"
		return
	}
	gateway, err := lan.Start(lan.Config{
		ListenAddr: net.JoinHostPort(d.ip, strconv.Itoa(port)), CertificateAddr: net.JoinHostPort(d.ip, strconv.Itoa(port+1)),
		ProxyAddr: a.srv.Addr(), Allowed: strings.Fields(d.allowed), Certificate: a.srv.CACertificate(),
	})
	if err != nil {
		d.problem = err.Error()
		return
	}
	d.gateway, d.problem, d.check = gateway, "", ""
	a.setMessage("Device access enabled · " + gateway.Addr())
}

func (a *app) disableDevices() {
	d := &a.deviceUI
	if d.gateway != nil {
		err := d.gateway.Close()
		d.gateway = nil
		if err != nil {
			d.problem = err.Error()
		}
	}
	d.consent = false
	a.setMessage("Device access off · local capture is unchanged")
}

func (a *app) checkDeviceProxy() {
	if a.srv == nil || a.deviceUI.checkDone != nil {
		return
	}
	done := make(chan string, 1)
	a.deviceUI.checkDone = done
	addr := a.srv.Addr()
	go func() {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		result := "Local proxy: reachable. Test the network path and HTTPS trust from your device."
		if err != nil {
			result = "Local proxy: " + err.Error()
		} else {
			_ = conn.Close()
		}
		done <- result
		a.invalidate()
	}()
}

func (a *app) devicesModal(c *ui.Context, t *ui.Theme) {
	d := &a.deviceUI
	if d.checkDone != nil {
		select {
		case d.check = <-d.checkDone:
			d.checkDone = nil
		default:
			c.After(time.Second)
		}
	}
	if !d.open {
		return
	}
	if d.gateway != nil {
		c.After(time.Second)
	}
	appModal(c, &d.open, func() {
		ui.Column(c).Width(820).Height(510).Gap(14).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				dialogTitle(c, "Connect a device")
				ui.Spacer(c)
				if d.gateway == nil {
					ui.Badge(c, "LAN off")
				} else {
					ui.Badge(c, "LAN enabled").Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
				}
			})
			ui.Text(c, "Same Wi-Fi / LAN. Only listed source addresses can connect. Local capture stays on loopback.").FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Grow(1).Gap(22).AlignItems(ui.Stretch).Children(func() {
				ui.Scroll(c).Width(350).Gap(10).Children(func() {
					if d.gateway == nil {
						ui.Text(c, "1  Choose a network address").Bold()
						ui.Row(c).Gap(8).Children(func() {
							ui.Select(c, &d.ip, d.ips).Label("Device-facing address").Grow(1)
							ui.TextInput(c, &d.port).Width(82).Label("LAN proxy port").Placeholder("7891")
						})
						if len(d.ips) == 0 {
							ui.Text(c, "No active LAN address. Connect to a network, then reopen this dialog.").FontSize(12).TextColor(t.Danger)
						}
						if ip, err := netip.ParseAddr(d.ip); err == nil && ip.IsLinkLocalUnicast() {
							ui.Text(c, "Link-local address: the device needs a compatible address on the same network link.").FontSize(11).TextColor(t.TextMuted)
						}
						ui.Text(c, "Proxy port above; the certificate service uses port + 1. Allow both in your firewall.").FontSize(11).TextColor(t.TextMuted)
						ui.Text(c, "2  Allow your device's IP address").Bold()
						ui.TextArea(c, &d.allowed).Height(74).Label("Allowed device addresses").Placeholder("192.168.1.42\nOne IP or CIDR per line")
						ui.Text(c, "Find it in the device’s Wi-Fi details. Prefer one IP; CIDRs allow every device in that range. Empty = deny all.").FontSize(11).TextColor(t.TextMuted)
						ui.Checkbox(c, &d.consent, "I trust this network and the allowed devices")
					} else {
						ui.Text(c, "Proxy address").FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, d.gateway.Addr()).FontSize(17).Bold()
						if ui.Button(c, "Copy proxy address").Clicked() {
							c.Services().WriteClipboard(d.gateway.Addr())
						}
						ui.Divider(c)
						ui.Text(c, "Open on your device to download the CA").FontSize(12).TextColor(t.TextMuted)
						certificateURL := "http://" + d.gateway.CertificateAddr() + "/vscroxy-ca.cer"
						ui.Text(c, certificateURL).FontSize(12)
						if ui.Button(c, "Copy certificate URL").Clicked() {
							c.Services().WriteClipboard(certificateURL)
						}
						ui.Text(c, "Public certificate only — the signing key is never served. Compare the CA fingerprint under Checks on a trusted network.").FontSize(11).TextColor(t.TextMuted)
						ui.Divider(c)
						ui.Text(c, "Allowed sources").FontSize(12).Bold()
						ui.Text(c, d.allowed).FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, "Disable access before changing the address or allowlist.").FontSize(11).TextColor(t.TextMuted)
					}
				})
				ui.Column(c).Grow(1).Gap(12).Children(func() {
					ui.Tabs(c, &d.platform, "iOS", "Android", "Checks")
					ui.Scroll(c).Grow(1).Gap(6).Padding(0, 10, 0, 0).Children(func() {
						switch d.platform {
						case 0:
							ui.Text(c, "Set up iPhone / iPad").FontSize(14).Bold()
							ui.Text(c, "1. Wi-Fi → your network → Configure Proxy → Manual. Enter the proxy IP and port shown here; authentication off.").FontSize(12)
							ui.Text(c, "2. Open the certificate URL in Safari. Allow the profile, then install it in Settings → General → VPN & Device Management (or Profile Downloaded).").FontSize(12)
							ui.Text(c, "3. General → About → Certificate Trust Settings → enable full trust for this CA.").FontSize(12)
							ui.Text(c, "4. Enable SSL proxying for the hosts you inspect, then open an HTTPS page and check Capture.").FontSize(12)
						case 1:
							ui.Text(c, "Set up Android").FontSize(14).Bold()
							ui.Text(c, "1. Edit your Wi-Fi network → Advanced → Proxy → Manual. Enter the proxy IP and port; save. Labels vary by vendor.").FontSize(12)
							ui.Text(c, "2. Download the certificate URL. In Security / Encryption & credentials, choose Install a certificate → CA certificate, then select vscroxy-ca.cer.").FontSize(12)
							ui.Text(c, "3. Enable SSL proxying for your hosts and open an HTTPS page. For your own debug app, opt in to user CAs with Network Security Configuration.").FontSize(12)
							ui.Text(c, "Many Android 7+ apps do not trust user-installed CAs. Certificate pinning and apps that ignore the system proxy need app-side changes; installing this CA does not bypass them.").FontSize(12).TextColor(t.TextMuted)
						case 2:
							ui.Text(c, "Connection checks").FontSize(14).Bold()
							if a.srv != nil {
								fingerprint := fmt.Sprintf("%X", sha256.Sum256(a.srv.CACertificate()))
								ui.Text(c, "CA SHA-256\n"+fingerprint[:32]+"\n"+fingerprint[32:]).Font("monospace").FontSize(11)
								if ui.Button(c, "Copy CA fingerprint").Clicked() {
									c.Services().WriteClipboard(fingerprint)
								}
							}
							ui.TextInput(c, &d.target).Label("Device HTTPS URL").Placeholder("https://example.com/")
							if ui.Button(c, "Check local proxy").Disabled(a.srv == nil || d.checkDone != nil).Clicked() {
								a.checkDeviceProxy()
							}
							if d.check != "" {
								ui.Text(c, d.check).FontSize(12)
							}
							if d.gateway != nil {
								status := d.gateway.Status()
								ui.Text(c, "Proxy TCP client: "+cmp.Or(status.LastProxyClient, "not seen yet")).FontSize(12)
								ui.Text(c, "Certificate port client: "+cmp.Or(status.LastCertificateClient, "not seen yet")).FontSize(12)
							}
							ui.Text(c, "A TCP connection is not proof of HTTPS trust. Verify the decrypted host, path and response in Capture. Mobile app identity is not available through this gateway.").FontSize(11).TextColor(t.TextMuted)
							a.setupEvidence(c, t, d.target, d.since, &d.details)
							ui.Text(c, "No device seen? Check the source IP / DHCP changes, selected interface, VPN, firewall and Wi-Fi client isolation. HTTP works but HTTPS fails? Check CA trust, SSL host rules and pinning.").FontSize(11).TextColor(t.TextMuted)
						}
					})
					if ui.Button(c, "Configure HTTPS inspection…").Clicked() {
						d.open, a.sslOpen = false, true
					}
				})
			})
			if d.problem != "" {
				ui.Text(c, d.problem).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if d.gateway == nil {
					ui.Text(c, "Off by default on every launch.").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Text(c, "Closing this dialog keeps access on. Disable when finished.").FontSize(11).TextColor(t.TextMuted)
				}
				ui.Spacer(c)
				if ui.Button(c, "Local client setup…").Clicked() {
					d.open = false
					a.openClientSetup()
				}
				if ui.Button(c, "Close").Clicked() {
					d.open = false
				}
				if d.gateway == nil {
					if ui.PrimaryButton(c, "Enable device access").Disabled(a.srv == nil || !d.consent || d.ip == "" || strings.TrimSpace(d.allowed) == "").Clicked() {
						a.enableDevices()
					}
				} else if ui.Button(c, "Disable device access").TextColor(t.Danger).Clicked() {
					a.disableDevices()
				}
			})
		})
	})
}
