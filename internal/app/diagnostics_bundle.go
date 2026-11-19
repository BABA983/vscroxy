package app

// The diagnostics bundle is what a person attaches to a bug report: which
// build, which platform, the shape of this computer's settings, the startup
// marks, the recent log and the connection diagnostics. It never carries
// request contents or secrets: settings are an allowlist of values, and the
// traffic check reports a method and status without the URL path.

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	// diagnosticsLogBytes is how much of each recent log file goes in.
	diagnosticsLogBytes = 256 << 10
	// diagnosticsLogFiles is how many of the newest log files go in.
	diagnosticsLogFiles = 2
)

// diagnosticSettings is the allowlist of settings a bundle carries: values
// that describe the setup, never paths, hostnames, usernames or secrets.
type diagnosticSettings struct {
	Version           string `json:"version"`
	Platform          string `json:"platform"`
	Appearance        string `json:"appearance"`
	Language          string `json:"language"`
	InspectorLayout   string `json:"inspectorLayout,omitempty"`
	ProxyPort         int    `json:"proxyPort,omitempty"`
	SOCKSOn           bool   `json:"socksOn"`
	SOCKSPort         int    `json:"socksPort,omitempty"`
	SOCKSAuth         bool   `json:"socksAuth"`
	VaultScripts      bool   `json:"vaultScripts"`
	CookiePersistence bool   `json:"cookiePersistence"`
	LocalControl      bool   `json:"localControl"`
	AllowList         bool   `json:"allowList"`
	BreakWait         int    `json:"breakWait,omitempty"`
	FlowLimit         int    `json:"flowLimit,omitempty"`
	MemoryLimit       int    `json:"memoryLimitMB,omitempty"`
	DiskLimit         int    `json:"diskLimitGB,omitempty"`

	Counts diagnosticCounts `json:"counts"`
}

type diagnosticCounts struct {
	Requests     int `json:"requests"`
	Rules        int `json:"rules"`
	Environments int `json:"environments"`
	HostProfiles int `json:"hostProfiles"`
	APIs         int `json:"apis"`
	Templates    int `json:"breakpointTemplates"`
}

func (a *app) diagnosticSettings() diagnosticSettings {
	s := a.currentSettings()
	return diagnosticSettings{
		Version:           version.String(),
		Platform:          version.Platform(),
		Appearance:        s.Appearance,
		Language:          s.Language,
		InspectorLayout:   s.InspectorLayout,
		ProxyPort:         s.ProxyPort,
		SOCKSOn:           s.SOCKSOn,
		SOCKSPort:         s.SOCKSPort,
		SOCKSAuth:         s.SOCKSAuth,
		VaultScripts:      s.VaultScripts,
		CookiePersistence: s.CookiePersistence,
		LocalControl:      s.LocalControl,
		AllowList:         s.AllowList,
		BreakWait:         s.BreakWait,
		FlowLimit:         s.FlowLimit,
		MemoryLimit:       s.MemoryLimit,
		DiskLimit:         s.DiskLimit,
		Counts: diagnosticCounts{
			Requests: len(a.requests), Rules: len(a.rules), Environments: len(a.envs),
			HostProfiles: len(a.profiles.set.Profiles), APIs: len(a.apis), Templates: len(a.bpTemplates),
		},
	}
}

// buildDiagnosticsBundle is the zip a person attaches to a bug report.
func (a *app) buildDiagnosticsBundle(t *ui.Theme, target *url.URL) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	header := fmt.Sprintf("vscroxy %s\ncommit: %s\nplatform: %s\ngo: %s\n",
		version.String(), cmp.Or(version.CommitString(), "unknown"), version.Platform(), runtime.Version())
	if err := add("version.txt", []byte(header)); err != nil {
		return nil, err
	}
	settings, err := json.MarshalIndent(a.diagnosticSettings(), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := add("settings.json", append(settings, '\n')); err != nil {
		return nil, err
	}
	if err := add("startup-marks.txt", startupMarksReport()); err != nil {
		return nil, err
	}
	if err := add("diagnostics.txt", a.diagnosticsReport(t, target)); err != nil {
		return nil, err
	}
	for _, log := range a.recentLogs() {
		if err := add("logs/"+log.name, log.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// diagnosticsReport is the connection diagnostics as text. A target is its
// scheme and host: the path and query can hold tokens and are request content.
func (a *app) diagnosticsReport(t *ui.Theme, target *url.URL) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "target: %s://%s\n", target.Scheme, target.Host)
	client := "System proxy"
	switch a.diagnostics.client {
	case diagnoseDevice:
		client = "Phone or tablet"
	case diagnoseProcess:
		client = "One process"
	}
	fmt.Fprintf(&b, "client: %s\n\n", client)
	for i, check := range a.diagnosticChecks(t, target) {
		state := "not checked"
		switch check.state {
		case checkPass:
			state = "pass"
		case checkWarn:
			state = "warning"
		case checkFail:
			state = "fail"
		}
		fmt.Fprintf(&b, "%d. %s: %s — %s\n", i+1, check.title, state, cmp.Or(check.report, check.result))
		for _, detail := range check.detail {
			fmt.Fprintf(&b, "   %s\n", detail)
		}
	}
	return []byte(b.String())
}

type recentLog struct {
	name string
	data []byte
}

// recentLogs reads the tail of the newest log files. The log holds the app's
// own messages, never request contents.
func (a *app) recentLogs() []recentLog {
	if a.settingsPath == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(a.settingsPath), "logs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		name string
		at   time.Time
	}
	var files []file
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "vscroxy.log") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, file{entry.Name(), info.ModTime()})
	}
	slices.SortFunc(files, func(x, y file) int { return y.at.Compare(x.at) })
	if len(files) > diagnosticsLogFiles {
		files = files[:diagnosticsLogFiles]
	}
	var logs []recentLog
	for _, f := range files {
		data, err := tailFile(filepath.Join(dir, f.name), diagnosticsLogBytes)
		if err != nil {
			continue
		}
		logs = append(logs, recentLog{f.name, data})
	}
	return logs
}

// tailFile reads the last limit bytes of path.
func tailFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// chooseDiagnosticsExport asks where to save the bundle and writes it.
func (a *app) chooseDiagnosticsExport(t *ui.Theme, target *url.URL) {
	win := a.win
	go guarded(func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       locales.ExportDiagnostics(),
			DefaultPath: "vscroxy-diagnostics-" + time.Now().Format("20060102-150405") + ".zip",
			Filters:     []mygo.FileFilter{{Name: "Zip archive", Extensions: []string{"zip"}}},
		})
		win.Update(func() {
			if err != nil {
				a.fail(locales.ExportDiagnostics(), err)
				return
			}
			if path == "" {
				return
			}
			data, err := a.buildDiagnosticsBundle(t, target)
			if err == nil {
				err = store.WriteFile(path, data)
			}
			if err != nil {
				a.fail(locales.ExportDiagnostics(), err)
				return
			}
			a.done(locales.DiagnosticsBundleSaved(filepath.Base(path)))
		})
	})
}
