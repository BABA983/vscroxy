package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/control"
	"github.com/baba983/vscroxy/internal/system"
)

// controlSettings is the closed set of settings remote automation may read or
// write. Keys are the stable ids from the Settings registry, never localized
// labels. Secrets, passphrases and local-only trust lists are absent; adding
// an id here is the only way a client can reach it.
type controlSetting struct {
	kind  string
	read  func(*app) any
	parse func(jsontext.Value) (any, bool)
}

var controlSettings = map[string]controlSetting{
	"general.checkForUpdates": {kind: "boolean", read: func(a *app) any { return a.updateCheck }, parse: controlBool},
	"general.setupGuide":      {kind: "boolean", read: func(a *app) any { return a.guide }, parse: controlBool},
	"https.allDomains":        {kind: "boolean", read: func(a *app) any { return a.sslAll }, parse: controlBool},
	"https.domains": {
		kind:  "string[]",
		read:  func(a *app) any { return splitHosts(a.sslHosts) },
		parse: controlDomains,
	},
	"capture.flowLimit":     {kind: "integer", read: func(a *app) any { return a.flowLimit }, parse: controlChoice(flowLimits, 0, 100000)},
	"capture.memoryLimit":   {kind: "integer", read: func(a *app) any { return a.memoryLimit }, parse: controlChoice(memoryLimits, 0, 8192)},
	"capture.diskLimit":     {kind: "integer", read: func(a *app) any { return a.diskLimit }, parse: controlChoice(diskLimits, 0, 128)},
	"capture.historyLimit":  {kind: "integer", read: func(a *app) any { return a.historyLimit }, parse: controlChoice([]int{25, 50, 100, 250, 500, 1000}, 0, 1000)},
	"capture.historySizeMB": {kind: "integer", read: func(a *app) any { return a.historySizeMB }, parse: controlChoice([]int{8, 16, 32, 64, 128, 256}, 0, 1024)},
}

func controlBool(raw jsontext.Value) (any, bool) {
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func controlChoice(options []int, min, max int) func(jsontext.Value) (any, bool) {
	return func(raw jsontext.Value) (any, bool) {
		var value int
		if err := json.Unmarshal(raw, &value); err != nil || value < min || value > max || value != 0 && !slices.Contains(options, value) {
			return nil, false
		}
		return value, true
	}
}

// controlDomains accepts a replacement decryption host list, with the same
// host-only syntax as ssl.update. The value never names a file or a secret.
func controlDomains(raw jsontext.Value) (any, bool) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || len(list) > 1000 {
		return nil, false
	}
	hosts := make([]string, 0, len(list))
	for _, host := range list {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.ContainsAny(host, " \t\n/?#@\\") {
			return nil, false
		}
		hosts = append(hosts, host)
	}
	return hosts, true
}

// controlSettings runs on the UI thread and exposes only the allowlisted
// values. Unknown ids get one generic refusal: no fallback to the full
// Settings registry, which holds secrets and local trust decisions.
func (a *app) controlSettings(command control.Command) (any, error) {
	switch command.Action {
	case "settings.list":
		if _, err := controlParams[struct{}](command); err != nil {
			return nil, err
		}
		var rows []map[string]any
		for _, id := range slices.Sorted(maps.Keys(controlSettings)) {
			setting := controlSettings[id]
			rows = append(rows, map[string]any{"id": id, "kind": setting.kind, "value": setting.read(a)})
		}
		return rows, nil
	case "settings.get":
		p, err := controlParams[struct {
			ID string `json:"id"`
		}](command)
		if err != nil {
			return nil, err
		}
		setting, ok := controlSettings[p.ID]
		if !ok {
			return nil, errors.New("setting is not available to automation")
		}
		return map[string]any{"id": p.ID, "kind": setting.kind, "value": setting.read(a)}, nil
	case "settings.set":
		p, err := controlParams[struct {
			ID      string         `json:"id"`
			Value   jsontext.Value `json:"value"`
			Confirm bool           `json:"confirm"`
		}](command)
		if err != nil || !p.Confirm {
			return nil, errors.New("setting, value and confirm:true are required")
		}
		setting, ok := controlSettings[p.ID]
		if !ok {
			return nil, errors.New("setting is not available to automation")
		}
		value, valid := setting.parse(p.Value)
		if !valid {
			return nil, errors.New("invalid value for setting")
		}
		if err := a.applyControlSetting(p.ID, value); err != nil {
			return nil, err
		}
		return map[string]any{"id": p.ID, "value": value}, nil
	}
	return nil, errors.New("unknown settings action")
}

// applyControlSetting writes through the same save paths the Settings editor
// uses. A failed save leaves the live value and the capture limits untouched.
func (a *app) applyControlSetting(id string, value any) error {
	var restore, afterSave func()
	switch id {
	case "general.checkForUpdates":
		previous := a.updateCheck
		a.updateCheck = value.(bool)
		restore = func() { a.updateCheck = previous }
	case "general.setupGuide":
		previous := a.guide
		a.guide = value.(bool)
		restore = func() { a.guide = previous }
	case "https.allDomains":
		cfg := a.inspectionConfig()
		cfg.All = value.(bool)
		if !a.saveSSL(cfg) {
			return errors.New("could not save HTTPS decryption settings; nothing changed")
		}
		return nil
	case "https.domains":
		cfg := a.inspectionConfig()
		cfg.Hosts = value.([]string)
		if !a.saveSSL(cfg) {
			return errors.New("could not save HTTPS decryption settings; nothing changed")
		}
		return nil
	case "capture.flowLimit":
		previous := a.flowLimit
		a.flowLimit = value.(int)
		restore = func() { a.flowLimit = previous }
		afterSave = a.applyFlowLimits
	case "capture.memoryLimit":
		previous := a.memoryLimit
		a.memoryLimit = value.(int)
		restore = func() { a.memoryLimit = previous }
		afterSave = a.applyFlowLimits
	case "capture.diskLimit":
		previous := a.diskLimit
		a.diskLimit = value.(int)
		restore = func() { a.diskLimit = previous }
		afterSave = a.applyFlowLimits
	case "capture.historyLimit":
		previous := a.historyLimit
		a.historyLimit = value.(int)
		restore = func() { a.historyLimit = previous }
		afterSave = a.applyHistoryLimits
	case "capture.historySizeMB":
		previous := a.historySizeMB
		a.historySizeMB = value.(int)
		restore = func() { a.historySizeMB = previous }
		afterSave = a.applyHistoryLimits
	default:
		return errors.New("setting is not available to automation")
	}
	a.bypassSettingsSave()
	if err := a.persistSettings(); err != nil {
		if restore != nil {
			restore()
		}
		return err
	}
	if afterSave != nil {
		afterSave()
	}
	return nil
}

func (a *app) applyFlowLimits() {
	a.trimFlows()
	a.recount()
	a.applyFilter()
}

// controlCertificates reads the local CA's identity and trust status. The
// trust check touches the machine's trust store read-only; the CA certificate
// is public. Errors are reduced to stable categories so arbitrary system
// exception text never reaches a tool result.
func (a *app) controlCertificates(command control.Command) (any, error) {
	if _, err := controlParams[struct{}](command); err != nil {
		return nil, err
	}
	path, all, hosts, apps := a.caPath, a.sslAll, splitHosts(a.sslHosts), len(a.sslApps)
	// Only the host and the certificate file's name are safe to expose: the
	// directory can carry private details, and passphrases are sealed by ID.
	servers := make([]map[string]string, 0, len(a.certificates.Servers))
	for _, server := range a.certificates.Servers {
		file := ""
		if server.CertFile != "" {
			file = filepath.Base(server.CertFile)
		}
		servers = append(servers, map[string]string{"host": server.Host, "file": file})
	}
	return controlWork(func(context.Context) (any, error) {
		result := map[string]any{"path": path, "trusted": false, "fingerprint": "",
			"ssl":                map[string]any{"all": all, "hosts": hosts, "apps": apps},
			"serverCertificates": servers}
		if path == "" {
			result["problem"] = "unavailable"
			return result, nil
		}
		cert, err := system.CheckCA(path)
		if cert != nil {
			sum := sha256.Sum256(cert.Raw)
			result["fingerprint"] = hex.EncodeToString(sum[:])
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			result["problem"] = "unavailable"
		case cert == nil:
			result["problem"] = "unreadable"
		case err == nil:
			result["trusted"] = true
		default:
			if _, invalid := errors.AsType[x509.CertificateInvalidError](err); invalid {
				result["problem"] = "invalid"
			} else {
				result["problem"] = "untrusted"
			}
		}
		return result, nil
	}), nil
}
