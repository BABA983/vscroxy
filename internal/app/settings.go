package app

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/egoist/mygo/ui"
)

// settings is what the app keeps between launches besides the window's
// bounds, which MyGo saves itself for a window with a StateKey.
type settings struct {
	// Appearance is "light" or "dark"; empty follows the system.
	Appearance   string       `json:"appearance,omitempty"`
	SidebarWidth float32      `json:"sidebarWidth,omitempty"`
	TablePercent float32      `json:"tablePercent,omitempty"`
	Columns      columnLayout `json:"columns,omitempty"`
	// InspectorLayout is "right" (side by side, the default) or "bottom"
	// (stacked); each keeps its own divider.
	InspectorLayout  string  `json:"inspectorLayout,omitempty"`
	SideTablePercent float32 `json:"sideTablePercent,omitzero"`
	SidebarHidden    bool    `json:"sidebarHidden,omitzero"`
	// CustomColumns are the table's added columns, HiddenColumns the IDs of
	// the built-in ones taken out.
	CustomColumns []customColumn `json:"customColumns,omitempty"`
	HiddenColumns []string       `json:"hiddenColumns,omitempty"`
	// KeyLogPath is where the TLS secrets go for Wireshark, empty when the
	// key log is off.
	KeyLogPath string `json:"keyLogPath,omitempty"`
	// ProxyPort is the fixed loopback port, or zero for automatic selection.
	ProxyPort int `json:"proxyPort,omitzero"`
	// SOCKSOn opens the SOCKS5 entry on the next launch; SOCKSPort is its
	// fixed loopback port, or zero for automatic selection. SOCKSAuth asks
	// clients for the RFC 1929 username and password.
	SOCKSOn       bool   `json:"socksOn,omitzero"`
	SOCKSPort     int    `json:"socksPort,omitzero"`
	SOCKSAuth     bool   `json:"socksAuth,omitzero"`
	SOCKSUsername string `json:"socksUsername,omitempty"`
	SOCKSPassword string `json:"socksPassword,omitempty"`
	// ReverseProxies are loopback ports that forward plaintext HTTP to a
	// remote base URL.
	ReverseProxies []reverseProxySetting `json:"reverseProxies,omitempty"`
	// VaultScripts lets request scripts use pm.vault.
	VaultScripts bool `json:"vaultScripts,omitzero"`
	// CookiePersistence keeps persistent cookies across launches in the
	// local cookie file. Off by default: cookies are credentials.
	CookiePersistence bool `json:"cookiePersistence,omitzero"`
	// Guide keeps the setup guide open until it is finished or skipped.
	Guide bool `json:"guide,omitzero"`
	// AllowList narrows the table to the hosts and apps in allow.txt and
	// allow-apps.txt.
	AllowList bool `json:"allowList,omitzero"`
	// FlowLimit, MemoryLimit (MB) and DiskLimit (GB) bound what the table
	// keeps of a long capture; zero is the default.
	FlowLimit   int `json:"flowLimit,omitzero"`
	MemoryLimit int `json:"memoryLimitMB,omitzero"`
	DiskLimit   int `json:"diskLimitGB,omitzero"`
}

// reverseProxySetting is one saved reverse proxy entry. ID keeps a row's
// identity across edits; the port and target are what it forwards.
type reverseProxySetting struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Target   string `json:"target"`
	KeepHost bool   `json:"keepHost,omitzero"`
	Enabled  bool   `json:"enabled,omitzero"`
	// Insecure allows a self-signed certificate on the remote target. Off by
	// default; the app verifies the real remote, not only the local proxy.
	Insecure bool `json:"insecure,omitzero"`
}

func (s settings) proxyAddr() (string, error) {
	if s.ProxyPort == 0 {
		return "", nil
	}
	if s.ProxyPort < 1024 || s.ProxyPort > 65535 {
		return "", errors.New("Proxy port must be between 1024 and 65535, or empty for automatic selection")
	}
	return fmt.Sprintf("127.0.0.1:%d", s.ProxyPort), nil
}

// saveProxyPort saves only a validated draft. The running listener is not
// touched: clients, the system proxy and device gateways keep their address.
func (a *app) saveProxyPort() {
	a.proxyPortProblem = ""
	port := 0
	if input := strings.TrimSpace(a.proxyPortInput); input != "" {
		n, err := strconv.Atoi(input)
		if err != nil || n < 1024 || n > 65535 {
			a.proxyPortProblem = "Enter a port from 1024 to 65535, or leave empty for automatic selection"
			return
		}
		port = n
	}
	if a.settingsPath == "" {
		a.proxyPortProblem = "Settings location is unavailable; port was not saved"
		return
	}
	previous := a.proxyPort
	a.proxyPort = port
	a.lastSave = time.Time{} // explicit save must not wait for the layout debounce
	if err := a.persistSettings(); err != nil {
		a.proxyPort = previous
		a.proxyPortProblem = "Port was not saved: " + err.Error()
		return
	}
	a.done("Proxy port saved · restart the app to apply")
}

// columnLayout is a table's columns as the user arranged them, saved with
// names of our own so the file does not follow MyGo's Go names.
type columnLayout struct {
	Order  []string           `json:"order,omitempty"`
	Widths map[string]float32 `json:"widths,omitempty"`
}

// loadSettings reads the saved settings, the defaults when there is no file.
func loadSettings(path string) (settings, error) {
	if path == "" {
		return settings{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return settings{}, nil
	}
	if err != nil {
		return settings{}, err
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		return settings{}, err
	}
	return s, nil
}

// writeSettings keeps the settings for the next launch.
func writeSettings(path string, s settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return store.WriteFile(path, append(data, '\n'))
}

// layout turns the saved columns into the table's.
func (c columnLayout) layout() ui.TableLayout {
	return ui.TableLayout{Order: slices.Clone(c.Order), Widths: maps.Clone(c.Widths)}
}

// savedLayout turns the table's columns into the file's.
func savedLayout(l ui.TableLayout) columnLayout {
	return columnLayout{Order: slices.Clone(l.Order), Widths: maps.Clone(l.Widths)}
}

// sameSettings reports whether two settings would write the same file.
func sameSettings(x, y settings) bool {
	return x.Appearance == y.Appearance &&
		x.SidebarWidth == y.SidebarWidth &&
		x.TablePercent == y.TablePercent &&
		x.InspectorLayout == y.InspectorLayout &&
		x.SideTablePercent == y.SideTablePercent &&
		x.SidebarHidden == y.SidebarHidden &&
		slices.Equal(x.CustomColumns, y.CustomColumns) &&
		slices.Equal(x.HiddenColumns, y.HiddenColumns) &&
		x.KeyLogPath == y.KeyLogPath &&
		x.ProxyPort == y.ProxyPort &&
		x.SOCKSOn == y.SOCKSOn && x.SOCKSPort == y.SOCKSPort &&
		x.SOCKSAuth == y.SOCKSAuth && x.SOCKSUsername == y.SOCKSUsername &&
		x.SOCKSPassword == y.SOCKSPassword &&
		slices.Equal(x.ReverseProxies, y.ReverseProxies) &&
		x.VaultScripts == y.VaultScripts &&
		x.CookiePersistence == y.CookiePersistence &&
		x.Guide == y.Guide &&
		x.AllowList == y.AllowList &&
		x.FlowLimit == y.FlowLimit && x.MemoryLimit == y.MemoryLimit && x.DiskLimit == y.DiskLimit &&
		slices.Equal(x.Columns.Order, y.Columns.Order) &&
		maps.Equal(x.Columns.Widths, y.Columns.Widths)
}

// inspectorLayout reads a saved layout: "bottom" is stacked, anything else
// side by side.
func inspectorLayout(saved string) string {
	if saved == "bottom" {
		return "bottom"
	}
	return "right"
}
