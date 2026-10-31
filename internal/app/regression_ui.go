package app

import (
	"fmt"
	"github.com/baba983/vscroxy/internal/uikit"
	"io"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type regressionShare struct {
	open, importing, reviewed bool
	preview                   string
	changes                   []string
	feedback                  string
}

func (a *app) checksEditor(c *ui.Context, d *requestDocument, t *ui.Theme) {
	ui.Scroll(c).Grow(1).Gap(t.Space(3)).Children(func() {
		uikit.Meta(c, "RESPONSE CHECKS · all must pass · used by Send and Collection Runner")
		ui.Form(c, func() {
			ui.Field(c, "Expected status", func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					uikit.Input(c, &d.checks.Status).Width(90).Label("Expected status").Placeholder("200")
					uikit.Meta(c, "Empty = no status assertion")
				})
			})
			ui.Field(c, "Response headers", func() {
				ui.TextArea(c.Key("check-headers"), &d.checks.Headers).Height(72).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label("Expected response headers").Placeholder("Content-Type: application/json")
				uikit.Meta(c, "Response headers · exact value, any repeated value may match")
			})
			ui.Field(c, "JSON scalar fields", func() {
				ui.TextArea(c.Key("check-json"), &d.checks.JSON).Height(96).Font(uikit.FontMono).FontSize(uikit.TextBody).
					Radius(uikit.RadiusControl).Label("JSON response checks").Placeholder("/data/id = 42\n/items/0/name = \"example\"\n/ok = true")
				uikit.Meta(c, "JSON scalar fields · RFC 6901 pointer = JSON value · one per line")
			})
		})
		if _, _, _, err := d.checks.Parse(); err != nil {
			ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger)
		}
		if names := d.value().Variables(); len(names) > 0 {
			uikit.Meta(c, "Required variables: "+strings.Join(names, ", ")+" · configure in Variables…")
		}
		uikit.Meta(c, "JSON types matter; missing is not null. Checks never run scripts. Rerunning sends real traffic and can change server data.")
	})
}

func (a *app) openRegressionShare(r savedRequest) {
	preview, changes, err := portableRegression(r)
	if err != nil {
		a.setMessage("Share regression: " + err.Error())
		return
	}
	a.regressionUI = regressionShare{open: true, preview: preview, changes: changes}
}

func (a *app) importRegression() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Title: "Import regression", Filters: []mygo.FileFilter{{Name: "Regression JSON", Extensions: []string{"json"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		f, err := os.Open(paths[0])
		if err != nil {
			a.setMessage("Import regression: " + err.Error())
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err == nil {
			_, err = decodeRegression(data)
		}
		if err != nil {
			a.setMessage("Import regression: " + err.Error())
			return
		}
		a.win.Update(func() {
			a.regressionUI = regressionShare{open: true, importing: true, preview: string(data)}
		})
	}()
}

func (a *app) regressionModal(c *ui.Context, t *ui.Theme) {
	s := &a.regressionUI
	if !s.open {
		return
	}
	// Capture/save feedback must not cover the review gate in a small window.
	c.CloseToast("")
	width, height := c.Size()
	uikit.Modal(c, &s.open, func() {
		ui.Column(c).Width(min(760, width-80)).Height(min(660, height-80)).Gap(t.Space(2)).Children(func() {
			title := "Share regression · review sanitized copy"
			if s.importing {
				title = "Import regression · review before opening"
			}
			uikit.DialogTitle(c, title)
			ui.Scroll(c).Grow(1).Gap(t.Space(2)).Children(func() {
				for _, change := range s.changes {
					uikit.Meta(c, "• "+change)
				}
				ui.Text(c, "Review URL paths, parameter names, Content-Type/Accept and assertion values for secrets. Automatic replacements are not a guarantee of anonymization. Edit the JSON below if needed.").FontSize(uikit.TextBody).TextColor(t.Warning)
				if ui.TextArea(c, &s.preview).Height(280).Font(uikit.FontMono).FontSize(uikit.TextMeta).Label("Regression JSON preview").Changed() {
					s.reviewed, s.feedback = false, ""
				}
			})
			r, err := decodeRegression([]byte(s.preview))
			if err != nil {
				ui.Text(c, err.Error()).FontSize(uikit.TextMeta).TextColor(t.Danger)
			} else {
				uikit.Meta(c, "Variables to supply locally: "+strings.Join(r.Variables(), ", "))
			}
			ui.Checkbox(c, &s.reviewed, "I reviewed the full request and checks").Label("Confirm regression review")
			ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
				ui.Text(c, s.feedback).FontSize(uikit.TextMeta).TextColor(t.Success)
				if uikit.SecondaryButton(c, "Cancel").Clicked() {
					s.open = false
				}
				if s.importing {
					if uikit.PrimaryButton(c, "Open unsent case").Disabled(err != nil || !s.reviewed).Clicked() {
						r.Name = uniqueRequestName(a.requests, r.Name)
						a.addDocument(r, "")
						a.composeTab, s.open = 5, false
						a.setMessage("Imported unsent case; configure Variables, review the target, then Save request or Send")
					}
				} else {
					if uikit.SecondaryButton(c, "Copy case JSON").Disabled(err != nil || !s.reviewed).Clicked() {
						c.Services().WriteClipboard(s.preview)
						s.feedback = "Copied"
					}
					if uikit.PrimaryButton(c, "Export case…").Disabled(err != nil || !s.reviewed).Clicked() {
						data := []byte(s.preview + "\n")
						go func() {
							path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "Export reviewed regression", DefaultPath: "vscroxy-regression.json", Filters: []mygo.FileFilter{{Name: "Regression JSON", Extensions: []string{"json"}}}})
							if err != nil || path == "" {
								return
							}
							err = writePrivateFile(path, data)
							a.win.Update(func() {
								s.open = false
								if err != nil {
									a.setMessage("Export regression: " + err.Error())
								} else {
									a.setMessage(fmt.Sprintf("Exported reviewed regression to %s", path))
								}
							})
						}()
					}
				}
			})
		})
	})
}
