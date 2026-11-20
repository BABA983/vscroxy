package app

import (
	"context"
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
			ui.Scroll(c).MinHeight(0).Shrink(1).Gap(t.Space(uikit.SpaceXL)).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
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
				if uikit.PrimaryButtonBusy(c, locales.ImportAction(), a.urlImportBusy).Disabled(a.urlImportBusy || strings.TrimSpace(a.urlImportText) == "").Clicked() {
					a.fetchImportURL()
				}
			})
		})
	})
}

// fetchImportURL reads the document as one cancellable progress task, then
// merges it through the same path as a dropped file.
func (a *app) fetchImportURL() {
	raw := strings.TrimSpace(a.urlImportText)
	a.urlImportBusy, a.urlImportProblem = true, ""
	var data []byte
	var name string
	a.withProgress(progressOptions{
		title:       locales.ImportURLTask(),
		location:    progressNotification,
		cancellable: true,
		settle:      func() { a.urlImportBusy = false },
		finish: func() (string, error) {
			a.urlImportOpen = false
			return a.resultNotice(func() { a.importData(data, name) })
		},
	}, func(ctx context.Context, report func(progressReport)) (string, error) {
		report(progressReport{message: locales.ImportURLTask(), percentage: 10})
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil // Never loop back through vscroxy's own proxy.
		client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
		fetched, fetchedName, err := downloadImportURLContext(ctx, client, raw)
		if err != nil {
			a.publishProgress(func() { a.urlImportProblem = err.Error() })
			return "", err
		}
		report(progressReport{message: locales.ImportURLTask(), percentage: 80})
		data, name = fetched, fetchedName
		return "", nil
	})
}

// downloadImportURL fetches one import document without a task context.
func downloadImportURL(client *http.Client, raw string) ([]byte, string, error) {
	return downloadImportURLContext(context.Background(), client, raw)
}

// downloadImportURLContext fetches one import document. It refuses a non-HTTP
// URL, a non-2xx answer and anything over maxURLImport instead of truncating.
func downloadImportURLContext(ctx context.Context, client *http.Client, raw string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, "", fmt.Errorf("%s", locales.ImportUrlNeedsHttp())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := client.Do(req)
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
	if err := ctx.Err(); err != nil {
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
