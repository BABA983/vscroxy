package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// openDocs shows the read-only documentation of a collection or folder.
func (a *app) openDocs(path string) {
	a.docsOpen, a.docsPath = true, path
}

// docsModal renders a collection's or folder's Markdown description and the
// requests under it, with each request's method, URL and description.
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
	var requests []savedRequest
	for _, r := range a.requests {
		if r.Collection == path || strings.HasPrefix(r.Collection, path+"/") {
			requests = append(requests, r)
		}
	}
	slices.SortFunc(requests, func(x, y savedRequest) int {
		if n := strings.Compare(x.Collection, y.Collection); n != 0 {
			return n
		}
		return strings.Compare(x.Name, y.Name)
	})
	_, height := c.Size()
	uikit.Modal(c, &a.docsOpen, func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(680, height-80)).Children(func() {
			uikit.DialogTitle(c, "Documentation · "+path)
			uikit.Meta(c, locales.ReadOnlyMarkdownFromThe())
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(3)).Children(func() {
				if strings.TrimSpace(description) == "" {
					uikit.Hint(c, "This "+docsLevel(path)+" has no description yet. Add one in its settings, on the Description tab.")
				} else {
					markdown.View(c, description, &a.docsMarkdown)
				}
				ui.Divider(c)
				uikit.Label(c, fmt.Sprintf("Requests · %d", len(requests)))
				if len(requests) == 0 {
					uikit.Hint(c, locales.NoRequestsAreSavedUnder())
				}
				for _, r := range requests {
					a.docsRequest(c, t, r, path)
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					a.docsOpen = false
				}
			})
		})
	})
}

// docsRequest is one request's entry in the documentation view.
func (a *app) docsRequest(c *ui.Context, t *ui.Theme, r savedRequest, root string) {
	ui.Column(c).Padding(t.Space(2), 0).Gap(t.Space(1)).Children(func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			uikit.MethodLabel(c, cmp.Or(r.Protocol, r.Method, "HTTP")).FontSize(uikit.TextMeta).MinWidth(38).Shrink(0).Height(16)
			uikit.Body(c, r.Name).FontWeight(600).Grow(1)
			if sub := strings.TrimPrefix(r.Collection, root); sub != "" {
				uikit.Meta(c, strings.TrimPrefix(sub, "/"))
			}
		})
		uikit.Mono(c, r.URL).TextColor(t.TextMuted).Selectable()
		if strings.TrimSpace(r.Description) != "" {
			markdown.View(c, r.Description, &a.docsMarkdown)
		}
	})
}

// docsLevel names what a documentation path is.
func docsLevel(path string) string {
	if strings.Contains(path, "/") {
		return "folder"
	}
	return "collection"
}
