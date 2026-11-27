package app

import (
	"fmt"
	"strconv"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// searchView is the sidebar's Search mode: a query with the shared find
// options, the replace field, the count, and a tree of sources, resources and
// matching lines. It is the sidebar's width, so it keeps one control row.
func (a *app) searchView(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	a.syncSearchQuery() // an Alt+C/W/R toggle changes the query too
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).
		Gap(t.Space(uikit.SpaceS)).Children(func() {
		replaceIcon := lucide.ChevronRight()
		if s.replace.open {
			replaceIcon = lucide.ChevronDown()
		}
		ui.Row(c.Key("global-search-bar")).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
			if uikit.Tip(c, uikit.RowIconButton(c, locales.ToggleReplace(), locales.ToggleReplaceTip(), replaceIcon),
				locales.ToggleReplaceTip()).KeepFocus().Clicked() {
				s.replace.open = !s.replace.open
			}
			queryField, query := uikit.FindField(c, &s.input, locales.SearchEverywhere(),
				locales.Search(), findToggles(s)...)
			queryField.Grow(1).MinWidth(0)
			query.Bind(&s.handle)
			bindFindOptionKeys(query, s)
			query.OnShortcut(0, ui.KeyEnter, func() { s.step(1) })
			query.OnShortcut(ui.Shift, ui.KeyEnter, func() { s.step(-1) })
		})

		if s.replace.open {
			ui.Row(c.Key("global-search-replace-bar")).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
				replacement := uikit.Input(c, &s.replace.input).Label(locales.ReplaceWith()).
					Placeholder(locales.ReplaceWith()).Grow(1).MinWidth(0)
				replacement.Bind(&s.replace.handle)
				bindFindOptionKeys(replacement, s)
				replaceAll := uikit.RowIconButton(c, locales.ReplaceAll(), locales.ReplaceAll(), lucide.Replace()).
					Disabled(a.replaceableSearchCount() == 0)
				if uikit.Tip(c, replaceAll, locales.ReplaceAll()).KeepFocus().Clicked() {
					a.openSearchReplace()
				}
			})
		}

		if s.problem != "" {
			uikit.ErrorText(c, searchProblemText(s.problem))
			return
		}
		if s.input != "" {
			ui.Row(c).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
				count := uikit.Meta(c, locales.SearchResultsInResources(len(s.results), searchResourceCount(s.results))).
					FontFeatures("tnum").Grow(1).SingleLine()
				count.TextColor(t.TextMuted)
				if s.running {
					uikit.Spinner(c, locales.SearchRunning()).Shrink(0)
				}
			})
		}

		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, t.Space(uikit.SpaceXS)).Children(func() {
			if s.input == "" {
				ui.Column(c).Gap(t.Space(uikit.SpaceXS)).Padding(t.Space(uikit.SpaceM), 0).Children(func() {
					uikit.Hint(c, locales.SearchTips())
					uikit.Hint(c, locales.SearchTipScope())
					uikit.Hint(c, locales.SearchTipOptions())
					uikit.Hint(c, locales.SearchTipReplace())
				})
				return
			}
			if len(s.results) == 0 && !s.running {
				uikit.Hint(c, locales.SearchNoResults()).Padding(t.Space(uikit.SpaceM), 0)
				return
			}
			a.searchResults(c)
			if s.limited > 0 {
				uikit.Hint(c, locales.SearchResultsLimited(globalSearchLimit)).
					Padding(t.Space(uikit.SpaceM), 0)
			}
		})
	})
}

// searchResourceCount is how many distinct resources the results name, for
// the count under the query.
func searchResourceCount(results []searchResult) int {
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[strconv.Itoa(int(r.source))+"\x00"+r.resource] = true
	}
	return len(seen)
}

func searchSourceName(source searchSource) string {
	switch source {
	case searchCollections:
		return locales.SearchSourceRequests()
	case searchEnvironments:
		return locales.Variables()
	default:
		return locales.Traffic()
	}
}

// searchResultKey names one resource in one source for its collapse state.
func searchResultKey(source searchSource, resource string) string {
	return strconv.Itoa(int(source)) + "\x00" + resource
}

// searchResults is the source › resource › line tree. Sources and resources
// collapse with their chevrons, and keep that state while the query stays the
// same; a new query starts open.
func (a *app) searchResults(c *ui.Context) {
	s := &a.searchAll
	tree := ui.TreeBase(c)
	tree.Root.Gap(0).Children(func() {
		for i := 0; i < len(s.results); {
			source := s.results[i].source
			sourceEnd := i
			for sourceEnd < len(s.results) && s.results[sourceEnd].source == source {
				sourceEnd++
			}
			open := !s.collapsedSources[source]
			c.Key(fmt.Sprintf("search-source-%d", source))
			sourceItem := tree.Item(&open)
			sourceLabel := searchSourceName(source)
			sourceRow := treeRow(c, sourceItem, 0).Label(sourceLabel)
			sourceRow.Children(func() {
				branchArrow(c, &open)
				uikit.Label(c, sourceLabel).Grow(1).SingleLine()
				searchCountBadge(c, sourceEnd-i)
			})
			if sourceRow.Clicked() {
				open = !open
			}
			if open == s.collapsedSources[source] {
				setSearchCollapsed(&s.collapsedSources, source, !open)
			}
			sourceItem.Group(func() {
				for j := i; j < sourceEnd; {
					resource := s.results[j].resource
					resourceEnd := j
					for resourceEnd < sourceEnd && s.results[resourceEnd].resource == resource {
						resourceEnd++
					}
					key := searchResultKey(source, resource)
					resourceOpen := !s.collapsedResources[key]
					c.Key("search-resource-" + key)
					resourceItem := tree.Item(&resourceOpen)
					resourceRow := treeRow(c, resourceItem, 1).Label(resource)
					resourceRow.Children(func() {
						branchArrow(c, &resourceOpen)
						uikit.Body(c, resource).Grow(1).MinWidth(0).SingleLine()
						searchCountBadge(c, resourceEnd-j)
					})
					if resourceRow.Clicked() {
						resourceOpen = !resourceOpen
					}
					if resourceOpen == s.collapsedResources[key] {
						setSearchCollapsed(&s.collapsedResources, key, !resourceOpen)
					}
					resourceItem.Group(func() {
						for at := j; at < resourceEnd; at++ {
							a.searchResultRow(c, tree, at)
						}
					})
					j = resourceEnd
				}
			})
			i = sourceEnd
		}
	})
}

// setSearchCollapsed records a tree row's open state, keeping false as the
// default rather than storing every resource the reader never touched.
func setSearchCollapsed[K comparable](state *map[K]bool, key K, collapsed bool) {
	if *state == nil {
		*state = make(map[K]bool)
	}
	if collapsed {
		(*state)[key] = true
	} else {
		delete(*state, key)
	}
}

// searchCountBadge is a resource's or source's muted count at the row's end.
func searchCountBadge(c *ui.Context, count int) {
	t := c.Theme()
	uikit.Meta(c, strconv.Itoa(count)).FontFeatures("tnum").Shrink(0).
		Padding(0, t.Space(uikit.SpaceXS)).Radius(uikit.RadiusInner).
		Background(uikit.Colors(t).Muted)
}

// searchResultRow is one matching line: its field name as a short muted
// prefix, the highlighted line cut with an ellipsis, and the line number when
// the result has one. The current match takes the sidebar's selection look.
func (a *app) searchResultRow(c *ui.Context, tree ui.TreeParts, at int) {
	s := &a.searchAll
	result := s.results[at]
	c.Key(fmt.Sprintf("search-match-%d", at))
	item := tree.Item(nil)
	row := treeRow(c, item, 2).Label(locales.OpenSearchResult(result.resource))
	if at == s.current {
		uikit.Selected(c, row, !a.windowBehind)
	}
	activate := func() { a.openSearchResult(result) }
	row.OnClick(activate)
	inline := s.replace.open && s.replace.input != "" && result.replaceable()
	row.Children(func() {
		uikit.Meta(c, searchFieldPrefix(result.field)).Shrink(0).SingleLine()
		a.searchMatchedLine(c, result, inline)
		if inline && row.Hovered() {
			if uikit.Tip(c, uikit.RowIconButton(c, locales.Replace(), locales.ReplaceThisMatch(), lucide.Replace()),
				locales.ReplaceThisMatch()).KeepFocus().Clicked() {
				a.replaceOneSearchMatch(at)
			}
		}
	})
}

// searchMatchLead is how much of the text before a match the row keeps. The
// match must be visible in a 220-point sidebar, so a long lead is replaced by
// an ellipsis and its last few characters rather than hiding the reason the
// row is here at the truncated end.
const searchMatchLead = 10

// searchLineParts splits a line around its match for the row: a short lead,
// the match itself, and the rest. The lead and match keep their width; the
// tail yields and truncates, so the highlighted match is never what the row
// cuts off.
func searchLineParts(text string, start, end int) (lead, match, tail string) {
	if start < 0 || end < start || end > len(text) {
		return "", text, ""
	}
	prefix := []rune(text[:start])
	if len(prefix) > searchMatchLead {
		lead = "…" + string(prefix[len(prefix)-searchMatchLead:])
	} else {
		lead = string(prefix)
	}
	return lead, text[start:end], text[end:]
}

// searchLeadRun is one non-wrapping run of the match line at the same small
// monospace size as the field prefix.
func searchLeadRun(c *ui.Context, text string, size float32) ui.Element {
	return ui.RichText(c, ui.Span{Text: text, Size: size}).Font(uikit.FontMono).
		FontSize(size).SingleLine().Shrink(0)
}

// searchFieldPrefix shortens the field names that would otherwise leave too
// little of a 220-point row for the match itself. The request/response
// distinction stays; only the word "body" is dropped.
func searchFieldPrefix(field string) string {
	switch field {
	case locales.ResponseBody():
		return locales.Response()
	case locales.RequestBody():
		return locales.Request()
	default:
		return field
	}
}

// searchMatchedLine is one matching line. The lead and match are measured
// before the tail, so a narrow sidebar truncates the context after the match
// rather than the match itself. With Replace open, the matching bytes are
// struck through and the expansion is added after them.
func (a *app) searchMatchedLine(c *ui.Context, result searchResult, inline bool) ui.Element {
	t := c.Theme()
	lead, match, tail := searchLineParts(result.text, result.start, result.end)
	size := uikit.Scaled(uikit.TextMeta)
	line := ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Label(locales.OpenSearchResult(result.resource))
	line.Children(func() {
		if lead != "" {
			searchLeadRun(c, lead, size)
		}
		if inline {
			lit := uikit.Colors(t)
			ui.RichText(c, ui.Span{Text: match, Size: size, Strikethrough: true, Color: lit.Faint}).
				Font(uikit.FontMono).FontSize(size).SingleLine().Shrink(0)
			replacement := a.searchAll.query.expand(result.text,
				result.start, result.end, a.searchAll.replace.input)
			ui.RichText(c, ui.Span{Text: replacement, Size: size, Color: lit.DiffAdded}).
				Font(uikit.FontMono).FontSize(size).SingleLine().Shrink(0)
		} else {
			ui.RichText(c, ui.Span{Text: match, Size: size,
				Color: paletteHighlightInk(c, true), Weight: paletteMatchWeight}).
				Font(uikit.FontMono).FontSize(size).SingleLine().Shrink(0)
		}
		if tail != "" {
			ui.RichText(c, ui.Span{Text: tail, Size: size}).Font(uikit.FontMono).
				FontSize(size).SingleLine().Grow(1).MinWidth(0)
		} else {
			ui.Spacer(c)
		}
	})
	return line
}
