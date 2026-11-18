package app

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/store"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// markdownOptions follows the window's current theme. Markdown's Theme is a
// concrete look, so it cannot be left on the app's zero Options when Light and
// Dark switch while the window is open.
func markdownOptions(c *ui.Context, opts *markdown.Options) *markdown.Options {
	opts.Theme = uikit.MarkdownTheme(c.Theme())
	return opts
}

// openDocs shows the read-only documentation of a collection or folder.
func (a *app) openDocs(path string) {
	a.docsOpen, a.docsPath = true, path
	if a.docsSampleLang == nil {
		a.docsSampleLang = map[string]int{}
	}
}

// docsModal renders a collection's or folder's Markdown description and the
// requests under it: headers, body, auth with secrets hidden, examples and
// copyable code samples. The same content exports as Markdown or a
// self-contained HTML page.
func (a *app) docsModal(c *ui.Context, t *ui.Theme) {
	if !a.docsOpen {
		return
	}
	path := a.docsPath
	var description string
	for _, f := range a.folders {
		if f.Path == path {
			description = f.Description
			break
		}
	}
	entries := docEntries(a.requests, path)
	_, height := c.Size()
	uikit.Modal(c, &a.docsOpen, locales.DocumentationPath(path), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(680, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.DocumentationPath(path))
			uikit.Meta(c, locales.ReadOnlyMarkdownFromThe())
			ui.Scroll(c).Grow(1).MinHeight(0).PaddingY(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceL)).Children(func() {
				if strings.TrimSpace(description) == "" {
					uikit.Hint(c, locales.NoDescriptionYet(docsLevel(path)))
				} else {
					markdown.View(c, description, markdownOptions(c, &a.docsMarkdown))
				}
				ui.Divider(c)
				uikit.Label(c, locales.RequestsCount(len(entries)))
				uikit.Hint(c, locales.DocumentationSecretsHidden())
				if len(entries) == 0 {
					uikit.Hint(c, locales.NoRequestsAreSavedUnder())
				}
				for _, entry := range entries {
					a.docsEntryView(c, t, entry)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.ExportMarkdown()).Clicked() {
					a.exportDocs(path, description, entries, "md")
				}
				if uikit.SecondaryButton(c, locales.ExportHtml()).Clicked() {
					a.exportDocs(path, description, entries, "html")
				}
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					a.docsOpen = false
				}
			})
		})
	})
}

// docsEntryView is one request's entry in the documentation view.
func (a *app) docsEntryView(c *ui.Context, t *ui.Theme, entry docsEntry) {
	ui.Column(c).Padding(t.Space(uikit.SpaceM), 0).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Center).Children(func() {
			uikit.MethodLabel(c, entry.Method).MinWidth(38).Shrink(0).Height(16)
			uikit.Body(c, entry.Name).FontWeight(500).Grow(1)
			if entry.Collection != "" {
				uikit.Meta(c, entry.Collection)
			}
		})
		uikit.Mono(c, entry.URL).TextColor(t.TextMuted).Selectable()
		if strings.TrimSpace(entry.Description) != "" {
			markdown.View(c, entry.Description, markdownOptions(c, &a.docsMarkdown))
		}
		uikit.Meta(c, locales.Auth()+" · "+entry.Auth)
		if len(entry.Headers) > 0 {
			uikit.Label(c, locales.Headers())
			for _, header := range entry.Headers {
				ui.Row(c).Gap(t.Space(uikit.SpaceM)).AlignItems(ui.Start).Children(func() {
					uikit.Mono(c, header.Name).TextColor(t.TextMuted).MinWidth(uikit.WidthNarrow).Shrink(0)
					uikit.Mono(c, header.Value).Selectable().Grow(1)
				})
			}
		}
		if entry.Body != "" || entry.BodyNote != "" {
			uikit.Label(c, locales.Body())
			if entry.BodyNote != "" {
				uikit.Hint(c, entry.BodyNote)
			}
			if entry.Body != "" {
				uikit.Mono(c, entry.Body).Selectable()
			}
		}
		if len(entry.Examples) > 0 {
			uikit.Label(c, locales.Examples())
			for _, example := range entry.Examples {
				label := example.Name
				if label == "" {
					label = statusText(example.Status)
				} else {
					label += " · " + statusText(example.Status)
				}
				uikit.Meta(c, label)
				for _, header := range example.Headers {
					uikit.Mono(c, header.Name+": "+header.Value).TextColor(t.TextMuted).Selectable()
				}
				if example.Body != "" {
					uikit.Mono(c, example.Body).Selectable()
				}
			}
		}
		if len(entry.Samples) > 0 {
			uikit.Label(c, locales.CodeSamples())
			key := entry.Collection + "\x00" + entry.Name
			chosen := a.docsSampleLang[key]
			if chosen < 0 || chosen >= len(entry.Samples) {
				chosen = 0
			}
			ui.Row(c).Gap(t.Space(uikit.SpaceXS)).Wrap().Children(func() {
				for i, sample := range entry.Samples {
					button := uikit.ToolbarButton(c, sample.Language, sample.Language, i == chosen).Children(func() {
						uikit.Caption(c, sample.Language)
					})
					if button.Clicked() {
						a.docsSampleLang[key] = i
					}
				}
			})
			uikit.Mono(c, entry.Samples[chosen].Code).Selectable()
		}
	})
}

func statusText(status int) string {
	if status == 0 {
		return "—"
	}
	return strconv.Itoa(status)
}

// exportDocs writes the documentation to a file the user chooses.
func (a *app) exportDocs(path, description string, entries []docsEntry, format string) {
	var data []byte
	title := locales.ExportMarkdown()
	name := "documentation.md"
	if format == "html" {
		title = locales.ExportHtml()
		name = "documentation.html"
		page, err := docsHTMLFile(path, description, entries)
		if err != nil {
			a.fail(title, err)
			return
		}
		data = page
	} else {
		data = []byte(docsMarkdown(path, description, entries))
	}
	if base := filepath.Base(path); base != "." && base != "/" && base != "" {
		name = base + "." + format
	}
	go guarded(func() {
		target, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: title, DefaultPath: name})
		if err != nil {
			a.fail(title, err)
			return
		}
		if target == "" {
			return
		}
		if err := store.WriteFile(target, data); err != nil {
			a.fail(title, err)
			return
		}
		a.done(locales.DocumentationSavedTo(target))
	})
}

// docsLevel names what a documentation path is.
func docsLevel(path string) string {
	if strings.Contains(path, "/") {
		return "folder"
	}
	return "collection"
}
