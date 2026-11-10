package app

// The main screens in Simplified Chinese, light and dark at 1280 and 960:
// Capture with the inspector, a request editor, Tools → Rules, Settings, a
// protocol client and a dialog. They are opt-in like TestUISnapshots, with
// their own directory so a run never mixes the two languages.
//
// MyGo's own controls follow the tester's locale; the app's messages follow
// i18n.Locale, which mygo.App sets from the app's language. A tester has no
// app, so the test sets that function for its own run.

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/capture"
	"github.com/baba983/vscroxy/internal/request"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

func TestUISnapshotsChinese(t *testing.T) {
	dir := os.Getenv("VSCROXY_SNAPSHOTS_ZH")
	if dir == "" {
		t.Skip("set VSCROXY_SNAPSHOTS_ZH to an output directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("snapshot output directory must be empty; keep the baseline and choose a new directory")
	}
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })

	mqtt := func(a *app) {
		a.addDocument(savedRequest{Protocol: "MQTT", Name: "Telemetry", URL: "mqtts://broker.example.com:8883", MQTT: request.MQTTConfig{
			Version: 5, ClientID: "vscroxy-demo", KeepAlive: 30,
			Topics: []request.MQTTTopic{{Name: "sensors/+/temperature", QoS: 1}},
		}}, "")
		a.mqttState().tab = 1
	}
	states := []struct {
		name  string
		setup func(*app)
		act   func(*testing.T, *ui.Tester)
		want  string
	}{
		{name: "capture-selected", want: "请求检查器"},
		{name: "requests-editing", setup: func(a *app) { openDraft(a, a.flows[0]) }, want: "请求正文"},
		{name: "tools-rules", setup: func(a *app) {
			snapshotRules(a)
			a.srv = &capture.Server{}
			a.openRules()
		}, want: "规则"},
		{name: "settings-general", setup: func(a *app) { snapshotSettings(a, 0) }, want: "语言"},
		{name: "requests-mqtt", setup: mqtt, want: "主题筛选器接受"},
		{name: "requests-auth-ntlm", setup: func(a *app) {
			openDraft(a, a.flows[0])
			a.composeTab, a.auth.Type = composeTabAuth, request.AuthNTLM
			a.auth.NTLM = request.NTLMAuth{
				Username: "ann", Password: "{{vault:ntlm_password}}",
				Domain: "EXAMPLE", Workstation: "PC-01", DisableRetry: true,
			}
		}, want: "禁用请求重试"},
		{name: "capture-search-mode", act: func(t *testing.T, tt *ui.Tester) {
			if err := tt.Click("抓包搜索模式"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
		}, want: "通配符"},
		{name: "requests-body-type", setup: func(a *app) { openDraft(a, a.flows[0]); a.composeTab = composeTabBody }, act: func(t *testing.T, tt *ui.Tester) {
			if err := tt.Click("请求正文类型"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
		}, want: "Multipart"},
		{name: "dialog-delete", setup: func(a *app) { a.confirmDeleteFolder("Workspace") }, want: "此操作无法撤销"},
		{name: "dialog-socket", setup: func(a *app) {
			a.socket.open, a.socket.url = true, "wss://api.example.com/events"
			a.socket.session = &wsSession{state: "Disconnected"}
		}, want: "会话方向"},
	}
	type file struct {
		Path string `json:"path"`
		Sha  string `json:"sha256"`
	}
	var manifest []file
	for _, theme := range []string{"light", "dark"} {
		dark := theme == "dark"
		for _, state := range states {
			for _, width := range []int{1280, 960} {
				name := fmt.Sprintf("%s-%d", state.name, width)
				t.Run(theme+"/"+name, func(t *testing.T) {
					a := snapshotApp()
					a.library.root = filepath.Join(t.TempDir(), "collections")
					a.language = "zh-Hans"
					a.appearance = appearanceOf(dark)
					t.Cleanup(a.protobuf.stop)
					t.Cleanup(a.socket.protobuf.stop)
					t.Cleanup(a.jsonQuery.job.stop)
					t.Cleanup(a.columnRead.stop)
					t.Cleanup(func() {
						a.structured[0].stop()
						a.structured[1].stop()
						if a.requestDocument != nil {
							a.requestDocument.structured.stop()
						}
					})
					if state.setup != nil {
						state.setup(a)
					}
					tt := ui.NewTester(func(c *ui.Context) { a.view(c) }, width, 800)
					tt.SetLocale("zh-Hans")
					tt.Frame()
					if state.act != nil {
						state.act(t, tt)
					}
					if state.want != "" && !tt.HasText(state.want) {
						t.Fatalf("missing %q: %v", state.want, tt.Texts())
					}
					var b bytes.Buffer
					if err := png.Encode(&b, tt.Image()); err != nil {
						t.Fatal(err)
					}
					rel := filepath.Join(theme, name+".png")
					if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, rel), b.Bytes(), 0644); err != nil {
						t.Fatal(err)
					}
					manifest = append(manifest, file{Path: rel})
				})
			}
		}
	}
	data, err := json.Marshal(manifest, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
