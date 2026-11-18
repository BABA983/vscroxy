package app

// The About window: which vscroxy this is, and the licenses the package
// carries. The texts are the files mygo.json ships as resources; running from
// source finds them at the repository root instead.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/internal/version"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

const (
	licenseName = "LICENSE"
	noticesName = "THIRD_PARTY_NOTICES.md"
)

// openAbout shows the window and reads the two documents once.
func (a *app) openAbout() {
	a.aboutOpen = true
	a.aboutTab = 0
	a.aboutLicense, a.aboutNotices = shippedText(licenseName), shippedText(noticesName)
}

// shippedText reads a document beside the app: the resources directory of a
// package, the executable's directory, or the repository when run from source.
func shippedText(name string) string {
	var candidates []string
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil && dir != "" {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	candidates = append(candidates, name, filepath.Join("..", "..", name))
	for _, path := range candidates {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return ""
}

func (a *app) aboutModal(c *ui.Context, t *ui.Theme) {
	if !a.aboutOpen {
		return
	}
	_, height := c.Size()
	uikit.Modal(c, &a.aboutOpen, locales.AboutVscroxy(), func() {
		uikit.DialogColumn(c, uikit.DialogLarge).Height(min(680, height-80)).Children(func() {
			uikit.DialogTitle(c, locales.AboutVscroxy())
			uikit.Tabs(c, &a.aboutTab, "About vscroxy", locales.License(), locales.ThirdPartyNotices())
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(t.Space(uikit.SpaceL)).Children(func() {
				if a.aboutTab == 1 {
					uikit.Meta(c, locales.AboutNoticesHint())
					if a.aboutNotices == "" {
						uikit.Hint(c, locales.AboutNoticesMissing())
					} else {
						markdown.View(c, withoutComments(a.aboutNotices), markdownOptions(c, &a.aboutMarkdown))
					}
					return
				}
				row := func(label, value string) {
					uikit.SettingsRow(c, label, func() ui.Element { return uikit.Body(c, value) }, "")
				}
				row(locales.Version(), version.String())
				row(locales.Commit(), valueOrUnknown(version.CommitString()))
				row(locales.Platform(), version.Platform())
				row(locales.License(), "MIT")
				uikit.Meta(c, locales.AboutLicenseLine())
				ui.Divider(c)
				if a.aboutLicense == "" {
					uikit.Hint(c, locales.AboutLicenseMissing())
				} else {
					markdown.View(c, a.aboutLicense, markdownOptions(c, &a.aboutMarkdown))
				}
			})
			uikit.DialogFooter(c).Children(func() {
				if uikit.SecondaryButton(c, locales.Close()).Clicked() {
					a.aboutOpen = false
				}
			})
		})
	})
}

// withoutComments drops HTML comment lines, such as the generator's markers,
// which the Markdown view would otherwise show as text.
func withoutComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func valueOrUnknown(value string) string {
	if value == "" {
		return locales.Unknown()
	}
	return value
}
