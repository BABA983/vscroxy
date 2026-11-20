package app

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/baba983/vscroxy/internal/fuzzy"
	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// searchView is the Search view of the desktop: a query with the shared find
// options above, and a result list grouped by source and then by resource.
func (a *app) searchView(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	a.syncSearchQuery() // an Alt+C/W/R toggle changes the query too
	ui.Column(c).Fill().Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).
		Gap(t.Space(uikit.SpaceS)).Children(func() {
		ui.Row(c).Gap(t.Space(uikit.SpaceS)).AlignItems(ui.Center).Children(func() {
			uikit.Heading(c, locales.SearchEverywhere()).Grow(1)
			if s.running {
				uikit.Spinner(c, locales.SearchRunning()).Shrink(0)
			}
			uikit.Meta(c, locales.SearchResultCount(len(s.results))).FontFeatures("tnum").Shrink(0)
		})

		ui.Row(c.Key("global-search-bar")).Gap(t.Space(uikit.SpaceS)).
			AlignItems(ui.Center).Children(func() {
			query := uikit.Input(c, &s.input).Label(locales.SearchEverywhere()).
				Placeholder(locales.SearchAcrossAssets()).Grow(1).MinWidth(0)
			query.Bind(&s.handle)
			bindFindOptionKeys(query, s)
			findOptionToggles(c, s)
			toggleMark := "▸"
			if s.replace.open {
				toggleMark = "▾"
			}
			if uikit.Tip(c, uikit.SecondaryButton(c, toggleMark).Label(locales.ToggleReplace()),
				locales.ToggleReplaceTip()).KeepFocus().Clicked() {
				s.replace.open = !s.replace.open
			}
			if uikit.Tip(c, uikit.SecondaryButton(c, "↑").Label(locales.PreviousSearchMatch()),
				locales.PreviousMatchShiftEnter()).KeepFocus().Clicked() {
				s.step(-1)
			}
			if uikit.Tip(c, uikit.SecondaryButton(c, "↓").Label(locales.NextSearchMatch()),
				locales.NextMatchEnter()).KeepFocus().Clicked() {
				s.step(1)
			}
		})

		if s.replace.open {
			ui.Row(c.Key("global-search-replace-bar")).Gap(t.Space(uikit.SpaceS)).
				AlignItems(ui.Center).Children(func() {
				replacement := uikit.Input(c, &s.replace.input).Label(locales.ReplaceWith()).
					Placeholder(locales.ReplaceWith()).Grow(1).MinWidth(0)
				replacement.Bind(&s.replace.handle)
				bindFindOptionKeys(replacement, s)
				a.actionButton(c, "search.action.replaceAll", locales.ReplaceAll())
			})
		}

		if s.problem != "" {
			uikit.ErrorText(c, searchProblemText(s.problem))
		}

		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
			if s.input == "" {
				uikit.Hint(c, locales.SearchAcrossAssets()).Padding(t.Space(uikit.SpaceM), 0)
				return
			}
			if s.problem != "" {
				return
			}
			if len(s.results) == 0 && !s.running {
				uikit.Hint(c, locales.SearchNoResults()).Padding(t.Space(uikit.SpaceM), 0)
				return
			}
			a.searchResults(c, t)
			if s.limited > 0 {
				uikit.Hint(c, locales.SearchResultsLimited(globalSearchLimit)).
					Padding(t.Space(uikit.SpaceM), 0)
			}
		})
	})
}

func (a *app) searchResults(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	var source searchSource = 255
	resource := ""
	for i, result := range s.results {
		if result.source != source {
			source = result.source
			resource = ""
			ui.Row(c).Height(uikit.HeightRow + t.Space(uikit.SpaceXS)).FillWidth().
				AlignItems(ui.Center).Gap(t.Space(uikit.SpaceS)).Children(func() {
				uikit.Label(c, source.label()).Grow(1)
				uikit.Meta(c, fmt.Sprint(searchCount(s.results, source)))
			})
		}
		if result.resource != resource {
			resource = result.resource
			ui.Row(c).Height(uikit.HeightRow).FillWidth().AlignItems(ui.Center).Children(func() {
				uikit.BodyStrong(c, resource).Grow(1).MinWidth(0).SingleLine()
			})
		}
		a.searchResultRow(c, t, i, result)
	}
}

func searchCount(results []searchResult, source searchSource) int {
	n := 0
	for _, r := range results {
		if r.source == source {
			n++
		}
	}
	return n
}

// searchResultRow is a button-like row: its label names the resource for
// assistive technology and tests, and its one matching line keeps ITS own
// highlighted run rather than a second app-level search.
func (a *app) searchResultRow(c *ui.Context, t *ui.Theme, i int, result searchResult) {
	row := ui.Row(c.Key(fmt.Sprintf("search-result-%d", i))).Height(uikit.HeightRow+t.Space(uikit.SpaceXS)).
		FillWidth().Gap(t.Space(uikit.SpaceM)).Padding(0, t.Space(uikit.SpaceM)).
		AlignItems(ui.Center).Role(ui.RoleButton)
	if i == a.searchAll.current {
		row.Background(t.SurfaceHover)
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	open := func() { a.openSearchResult(result) }
	row.OnClick(open)
	inline := a.searchAll.replace.open && a.searchAll.replace.input != "" && result.replaceable()
	row.Children(func() {
		ui.Box(c).Width(128).Shrink(0).Children(func() {
			uikit.Meta(c, result.field).SingleLine()
		})
		// The line number is a narrow, right-aligned gutter on the match's
		// own row, so a result occupies one row rather than a label and line.
		ui.Row(c).Width(28).Shrink(0).Justify(ui.End).Children(func() {
			if result.line > 0 {
				uikit.Meta(c, strconv.Itoa(result.line))
			}
		})
		a.searchMatchedLine(c, result, open, inline)
		if inline {
			if uikit.Tip(c, uikit.SecondaryButton(c, locales.Replace()).
				Label(locales.ReplaceThisMatch()), locales.ReplaceThisMatch()).KeepFocus().Clicked() {
				a.replaceOneSearchMatch(i)
			}
		}
	})
}

// searchMatchedLine is one matching line. With the Replace field open the
// matching bytes are struck through and the expansion is added after them.
func (a *app) searchMatchedLine(c *ui.Context, result searchResult, open func(), inline bool) ui.Element {
	label := locales.OpenSearchResult(result.resource)
	if inline {
		replacement := a.searchAll.query.expand(result.text,
			result.start, result.end, a.searchAll.replace.input)
		return bulkEditLine(c, result.text, result.start, result.end, replacement, 0, label).OnClick(open)
	}
	start := utf8.RuneCountInString(result.text[:result.start])
	end := utf8.RuneCountInString(result.text[:result.end])
	spans := paletteSpans(result.text, []fuzzy.Match{{Start: start, End: end}},
		paletteHighlightInk(c, true), uikit.Scaled(uikit.TextData))
	return ui.RichText(c, spans...).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextData)).
		Label(label).Grow(1).MinWidth(0).SingleLine().OnClick(open)
}
