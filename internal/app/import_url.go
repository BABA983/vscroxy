package app

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// maxURLImport is the largest document a URL import reads. A larger response
// is refused rather than truncated into a misleading collection.
const maxURLImport = 32 << 20

func (a *app) openImportURL() {
	a.urlImportOpen, a.urlImportText, a.urlImportProblem, a.urlImportBusy = true, "", "", false
}

func (a *app) urlImportModal(c *ui.Context, t *ui.Theme) {
	if !a.urlImportOpen {
		return
	}
	uikit.Modal(c, &a.urlImportOpen, locales.ImportFromUrl2(), func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, locales.ImportFromUrl2())
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(4)).Padding(0, t.Space(1)).Children(func() {
				uikit.Hint(c, locales.ImportUrlHint())
				uikit.FormRow(c, locales.Url(), "", a.urlImportProblem, func() {
					if uikit.Input(c, &a.urlImportText).Label(locales.Url()).Placeholder("https://example.com/collection.json").
						AutoFocus().Changed() {
						a.urlImportProblem = ""
					}
				})
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					a.urlImportOpen = false
				}
				if uikit.PrimaryButton(c, locales.ImportAction()).Disabled(a.urlImportBusy || strings.TrimSpace(a.urlImportText) == "").Clicked() {
					a.fetchImportURL()
				}
			})
		})
	})
}

// fetchImportURL reads the document off the UI thread, then merges it through
// the same path as a dropped file.
func (a *app) fetchImportURL() {
	raw := strings.TrimSpace(a.urlImportText)
	a.urlImportBusy, a.urlImportProblem = true, ""
	go func() {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // Never loop back through vscroxy's own proxy.
		data, name, err := downloadImportURL(&http.Client{Timeout: 30 * time.Second, Transport: transport}, raw)
		if err != nil {
			a.win.Update(func() { a.urlImportBusy, a.urlImportProblem = false, err.Error() })
			return
		}
		a.win.Update(func() {
			a.urlImportOpen, a.urlImportBusy = false, false
			a.importData(data, name)
		})
	}()
}

// downloadImportURL fetches one import document. It refuses a non-HTTP URL, a
// non-2xx answer and anything over maxURLImport instead of truncating it.
func downloadImportURL(client *http.Client, raw string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, "", fmt.Errorf("%s", locales.ImportUrlNeedsHttp())
	}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("the server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxURLImport+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxURLImport {
		return nil, "", fmt.Errorf("%s", locales.ImportUrlTooLarge())
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = "import"
	}
	return data, name, nil
}
