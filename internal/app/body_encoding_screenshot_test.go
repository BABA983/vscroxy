package app

import (
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/egoist/mygo/ui"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestBodyGBKEncodingScreenshots exports 2x Tester captures of a GBK response
// in both themes. Set VSCROXY_BODY_ENCODING_SCREENSHOTS to an output directory.
func TestBodyGBKEncodingScreenshots(t *testing.T) {
	dir := os.Getenv("VSCROXY_BODY_ENCODING_SCREENSHOTS")
	if dir == "" {
		t.Skip("set VSCROXY_BODY_ENCODING_SCREENSHOTS to an output directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	const decoded = "服务器返回：订单已创建，金额 128 元"
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(decoded))
	if err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		appearance := "light"
		if dark {
			appearance = "dark"
		}
		t.Run(appearance, func(t *testing.T) {
			a := snapshotApp()
			a.flows[0].resBody = string(body)
			a.flows[0].bytes, a.flows[0].resSize = len(body), len(body)
			a.flows[0].respHeader = http.Header{"Content-Type": {"text/plain; charset=gbk"}}
			a.syncDetail()
			a.bodyModes(a.flows[0], 0) // initialize the per-side cache bodySideView expects
			a.bodyMode[0] = 0          // Auto must choose the decoded text view

			tt := ui.NewTester(func(c *ui.Context) {
				th := uikit.Theme(dark)
				c.SetTheme(th)
				ui.Column(c).Fill().Background(th.Background).Padding(16).Children(func() {
					a.bodySideView(c, a.flows[0], 0)
				})
			}, 1200, 700)
			tt.SetLocale("en-US")
			tt.SetScale(2)
			tt.Frame()
			if !tt.HasText("Encoding: GBK") {
				t.Fatalf("encoding label missing: %v", tt.Texts())
			}
			if got := a.bodyText[0].Buffer.String(); got != decoded {
				t.Fatalf("decoded body = %q, want %q", got, decoded)
			}
			if a.flows[0].resBody != string(body) {
				t.Fatal("display decoding changed the captured body")
			}
			file, err := os.Create(filepath.Join(dir, "gbk-"+appearance+".png"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := png.Encode(file, tt.Image()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
