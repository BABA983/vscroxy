package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// searchView is the sidebar's Search mode: a query with the shared find
// options, the replace field, the scope chips, the count, and a tree of
// sources, resources and matching lines.
func (a *app) searchView(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	a.syncSearchQuery() // an Alt+C/W/R toggle changes the query too
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Padding(t.Space(uikit.SpaceM), t.Space(uikit.SpaceL)).
		Gap(t.Space(uikit.SpaceS)).Children(func() {
		a.searchInputs(c, t)
		a.searchScopeChips(c, t)
		if s.problem != "" {
			uikit.ErrorText(c, searchProblemText(s.problem))
			return
		}
		a.searchCountLine(c, t)
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

// searchInputs is the query row and, while Replace is open, the replacement
// row. The narrow strip at their start spans both rows, as VS Code's does,
// and carries the disclosure instead of a floating chevron.
func (a *app) searchInputs(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	ui.Row(c.Key("global-search-bar")).AlignItems(ui.Stretch).Gap(t.Space(uikit.SpaceXS)).Children(func() {
		strip := ui.Column(c).Width(16).Shrink(0).FillHeight().AlignItems(ui.Center).
			Padding(t.Space(uikit.SpaceXS), 0).Radius(uikit.RadiusControl)
		if s.replace.open || strip.Hovered() {
			strip.Background(uikit.Colors(t).Muted)
		}
		strip.OnClick(func() {
			s.replace.open = !s.replace.open
			c.Invalidate()
		})
		strip.Children(func() {
			icon := statusMoreIcon
			if s.replace.open {
				icon = chevronIcon
			}
			ui.Icon(c, icon).Size(uikit.IconInline, uikit.IconInline).TextColor(t.TextMuted)
		})
		uikit.Tip(c, strip, locales.ToggleReplaceTip())

		ui.Column(c).Grow(1).MinWidth(0).Gap(t.Space(uikit.SpaceXS)).Children(func() {
			queryField, query := uikit.FindField(c, &s.input, locales.SearchEverywhere(),
				locales.Search(), findToggles(s)...)
			queryField.Grow(1).MinWidth(0)
			query.Bind(&s.handle)
			bindFindOptionKeys(query, s)
			query.OnShortcut(0, ui.KeyEnter, func() { s.step(1) })
			query.OnShortcut(ui.Shift, ui.KeyEnter, func() { s.step(-1) })
			if s.replace.open {
				a.searchReplaceField(c, t)
			}
		})
	})
}

// searchReplaceField is the replacement text with Preserve Case and Replace
// All at its trailing edge, as the mockup places them.
func (a *app) searchReplaceField(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll.replace
	field := ui.Row(c).Height(uikit.HeightControl).AlignItems(ui.Center).Gap(t.Space(uikit.SpaceXS)).
		Padding(0, t.Space(uikit.SpaceXS)).Radius(uikit.RadiusControl)
	var input ui.Element
	field.Children(func() {
		input = ui.TextInputBase(c, &s.input).Height(uikit.HeightControl).FontSize(uikit.Scaled(uikit.TextSmall)).
			Padding(0, t.Space(uikit.SpaceM)).Grow(1).MinWidth(0).FocusRing(false).
			Label(locales.ReplaceWith()).Placeholder(locales.ReplaceWith())
		input.Bind(&s.handle)
		bindFindOptionKeys(input, &a.searchAll)
		preserve := ui.ToggleBase(c, &s.preserve).Label(locales.PreserveCase()).KeepFocus().
			KeyShortcuts(ui.Alt, ui.KeyP).Size(uikit.HeightControlSmall, uikit.HeightControlSmall).
			Padding(0).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).Radius(uikit.RadiusInner)
		if s.preserve {
			preserve.Background(uikit.Colors(t).Accent).Border(uikit.HairlineWidth, t.TextMuted)
		}
		preserve.Children(func() {
			ink := t.TextMuted
			if s.preserve {
				ink = t.Text
			}
			ui.Text(c, locales.PreserveCaseBadge()).Font(uikit.FontMono).FontSize(uikit.Scaled(uikit.TextMeta)).TextColor(ink)
		})
		uikit.TipKeys(c, preserve, locales.PreserveCase(), "Alt", "P")
		input.OnShortcut(ui.Alt, ui.KeyP, func() {
			s.preserve = !s.preserve
			c.Invalidate()
		})
		replaceAll := uikit.RowIconButton(c, locales.ReplaceAll(), locales.ReplaceAll(), lucide.Replace()).
			Disabled(a.replaceableSearchCount() == 0)
		if replaceAll.Clicked() {
			a.openSearchReplace()
		}
	})
	uikit.FieldFace(c, field, input.Focused())
}

// searchScopeChips filters which sources the query scans. All are on until a
// chip is switched off; the engine skips the source it no longer searches.
func (a *app) searchScopeChips(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	ui.Row(c).PaddingStart(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceXS)).Wrap().Children(func() {
		for _, source := range []searchSource{searchCollections, searchEnvironments, searchFlows} {
			on := s.searches(source)
			chip := uikit.FilterChip(c, searchSourceName(source), on).
				Label(locales.SearchInSource(searchSourceName(source)))
			if chip.Clicked() {
				a.toggleSearchScope(source)
			}
		}
	})
}

func (a *app) toggleSearchScope(source searchSource) {
	s := &a.searchAll
	if s.scopeOff == nil {
		s.scopeOff = map[searchSource]bool{}
	}
	s.scopeOff[source] = !s.scopeOff[source]
	if !s.scopeOff[source] {
		delete(s.scopeOff, source)
	}
	a.invalidate()
}

// searchCountLine is "N results in M resources", or the replaceable count
// while Replace is open, with Collapse All and Clear at its end.
func (a *app) searchCountLine(c *ui.Context, t *ui.Theme) {
	s := &a.searchAll
	if s.input == "" {
		return
	}
	line := locales.SearchResultsInResources(len(s.results), searchResourceCount(s.results))
	if s.replace.open {
		line = locales.ReplaceSearchResultsInResources(fmt.Sprint(a.replaceableSearchCount()),
			fmt.Sprint(searchReplaceableResources(s.results)))
	}
	ui.Row(c).PaddingStart(t.Space(uikit.SpaceM)).Gap(t.Space(uikit.SpaceXS)).AlignItems(ui.Center).Children(func() {
		uikit.Tip(c, uikit.Meta(c, line).FontFeatures("tnum").Grow(1).SingleLine(), line)
		if s.running {
			uikit.Spinner(c, locales.SearchRunning()).Shrink(0)
		}
		if uikit.Tip(c, uikit.RowIconButton(c, locales.CollapseAll(), locales.CollapseAll(), lucide.ChevronsDownUp()).Size(18, 18),
			locales.CollapseAll()).Clicked() {
			a.collapseAllSearch()
		}
		if uikit.Tip(c, uikit.RowIconButton(c, locales.Clear(), locales.Clear(), closeIcon).Size(18, 18), locales.Clear()).Clicked() {
			a.clearSearch()
		}
	})
}

func (a *app) collapseAllSearch() {
	s := &a.searchAll
	s.collapsedSources = map[searchSource]bool{}
	s.collapsedResources = map[string]bool{}
	for _, result := range s.results {
		s.collapsedSources[result.source] = true
		s.collapsedResources[searchResultKey(result.source, result.resource)] = true
	}
	a.invalidate()
}

func (a *app) clearSearch() {
	s := &a.searchAll
	s.input, s.replace.input = "", ""
	a.setSearchQuery("")
	s.results, s.limited, s.current = nil, 0, 0
	a.invalidate()
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
		return locales.SearchScopeVariables()
	default:
		return locales.Traffic()
	}
}

// searchResultKey names one resource in one source for its collapse state.
func searchResultKey(source searchSource, resource string) string {
	return strconv.Itoa(int(source)) + "\x00" + resource
}

// searchResults is the source › resource › line tree. Sources and resources
// collapse with their chevrons, and keep that state while the query and
// scopes stay the same; a new query starts open. Expanded groups keep a
// hairline guide under them.
func (a *app) searchResults(c *ui.Context) {
	s := &a.searchAll
	t := c.Theme()
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
			sourceRow := searchTreeRow(c, sourceItem, 0).Label(locales.SearchSourceResults(sourceLabel))
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
				ui.Box(c).FillWidth().Children(func() {
					searchGuide(c, searchRowInset+searchChevronSize/2)
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
						resourceRow := searchTreeRow(c, resourceItem, 1).Label(resource)
						resourceRow.Children(func() {
							branchArrow(c, &resourceOpen)
							a.searchResourceMark(c, s.results[j])
							a.searchResourceLabel(c, s.results[j])
							if s.replace.open && resourceRow.Hovered() {
								indices := a.resourceMatchIndices(source, resource)
								if len(indices) > 0 {
									if uikit.Tip(c, uikit.RowIconButton(c, locales.ReplaceAllInThisResource(),
										locales.ReplaceAllInThisResource(), lucide.Replace()).Size(18, 18), locales.ReplaceAllInThisResource()).Clicked() {
										a.applySearchReplace(indices...)
									}
								}
								if uikit.Tip(c, uikit.RowIconButton(c, locales.DismissResult(),
									locales.DismissResult(), closeIcon).Size(18, 18), locales.DismissResult()).Clicked() {
									a.dismissSearchResource(source, resource)
								}
								return
							}
							if source == searchFlows && s.replace.open {
								uikit.Tip(c, ui.Icon(c, lucide.Lock()).Size(uikit.IconInline, uikit.IconInline).
									TextColor(t.TextMuted).Shrink(0), locales.ReadOnly())
								return
							}
							searchCountBadge(c, resourceEnd-j)
						})
						if resourceRow.Clicked() {
							resourceOpen = !resourceOpen
						}
						if resourceOpen == s.collapsedResources[key] {
							setSearchCollapsed(&s.collapsedResources, key, !resourceOpen)
						}
						resourceItem.Group(func() {
							ui.Box(c).FillWidth().Children(func() {
								searchGuide(c, searchRowInset+searchIndentStep+searchChevronSize/2)
								for at := j; at < resourceEnd; at++ {
									a.searchResultRow(c, tree, at)
								}
							})
						})
						j = resourceEnd
					}
				})
			})
			i = sourceEnd
		}
	})
}

// searchResourceMark is the type mark before a resource's name: the request
// method, a scope badge for a variable, the traffic icon for a flow.
func (a *app) searchResourceMark(c *ui.Context, result searchResult) {
	switch result.source {
	case searchCollections:
		uikit.MethodLabel(c, result.method).FontSize(uikit.Scaled(uikit.TextMeta)).Shrink(0)
	case searchEnvironments:
		letter, label := a.variableScopeBadge(result)
		scopeBadge(c, letter, label)
	default:
		ui.Icon(c, captureIcon).Size(uikit.IconInline, uikit.IconInline).TextColor(c.Theme().TextMuted).Shrink(0)
	}
}

// searchResourceLabel is the name first and the muted place after it. The
// name keeps its width; the place shrinks first, and is left out when less
// than searchDetailMin points would remain for it.
func (a *app) searchResourceLabel(c *ui.Context, result searchResult) {
	name := result.resource
	if result.source == searchFlows {
		if _, rest, ok := strings.Cut(name, " "); ok {
			name = rest
		}
	}
	t := c.Theme()
	rowWidth := a.sidebarWidth - 2*t.Space(uikit.SpaceL) - 2*t.Space(uikit.SpaceXS)
	fixed := 2*searchRowInset + searchIndentStep + searchChevronSize +
		t.Space(uikit.SpaceXS)*3 + 18
	markWidth := float32(14)
	if result.source == searchCollections {
		markWidth = paneTextWidth(c, result.method, uikit.Scaled(uikit.TextMeta), uikit.WeightMedium)
	}
	fixed += markWidth
	available := max(0, rowWidth-fixed)
	nameWidth := min(available, paneTextWidth(c, name, uikit.Scaled(uikit.TextBody), uikit.WeightMedium))
	detailWidth := float32(0)
	if result.location != "" {
		detailWidth = paneTextWidth(c, result.location, uikit.Scaled(uikit.TextMeta), 0)
	}
	gap := t.Space(uikit.SpaceS)
	showDetail := detailWidth > 0 && available-nameWidth-gap >= searchDetailMin
	if showDetail {
		detailWidth = min(detailWidth, available-nameWidth-gap)
	} else {
		detailWidth = 0
	}
	ui.Row(c).FillWidth().Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
		nameBox := ui.Box(c).Width(nameWidth).Shrink(0).Clip()
		nameBox.Children(func() {
			uikit.Body(c, name).SingleLine()
		})
		if showDetail {
			ui.Box(c).Width(gap).Shrink(0)
			detailBox := ui.Box(c).Width(detailWidth).Shrink(0).Clip()
			detailBox.Children(func() {
				uikit.Meta(c, result.location).SingleLine()
			})
		}
	})
}

// resourceMatchIndices lists the results under one resource that Replace All
// in this resource may write.
func (a *app) resourceMatchIndices(source searchSource, resource string) []int {
	var indices []int
	for i, result := range a.searchAll.results {
		if result.source == source && result.resource == resource && result.replaceable() {
			indices = append(indices, i)
		}
	}
	return indices
}

// variableScopeBadge maps a variable result onto the badge of the scope its
// editor writes.
func (a *app) variableScopeBadge(result searchResult) (string, string) {
	switch result.target.valueKind {
	case searchValueEnvInitial, searchValueEnvCurrent:
		return "E", locales.KindPath(locales.Environment(), result.env)
	case searchValueGlobalInitial, searchValueGlobalCurrent:
		return "G", locales.ScopeGlobal()
	case searchValueFolderDef, searchValueFolderCurrent:
		if strings.Contains(result.env, "/") {
			return "F", locales.KindPath(locales.FolderName(), result.env)
		}
		return "C", locales.KindPath(locales.Collection(), result.env)
	}
	if result.variable != "" {
		if result.env == "" {
			return "G", locales.ScopeGlobal()
		}
		return "E", locales.KindPath(locales.Environment(), result.env)
	}
	return "?", result.resource
}

// searchTreeRow is the search tree's compact row metrics: a 20-point row
// with 4-point gaps and a shorter indent, so a 220-point sidebar still has
// room for a resource's name and its place.
// The search tree's geometry. Every row fills the sidebar's width, so its
// hover and selection wash starts at one edge; indentation happens inside the
// row, and a group's guide sits under its parent's chevron.
const (
	searchRowInset    = 4
	searchIndentStep  = 16
	searchChevronSize = 16
	searchDetailMin   = 40
)

func searchTreeRow(c *ui.Context, item ui.TreeItemParts, depth int) ui.Element {
	t := c.Theme()
	row := item.Row.FillWidth().Height(20).Padding(0, searchRowInset).
		PaddingStart(searchRowInset + float32(depth)*searchIndentStep).
		Gap(t.Space(uikit.SpaceXS)).Radius(uikit.RadiusControl).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	return row
}

// searchGuide draws one group's vertical guide inside the children's indent
// area. x is the centre of the parent's chevron, so the line runs exactly
// under it from the first child to the last.
func searchGuide(c *ui.Context, x float32) {
	ui.Box(c).Absolute().Left(x).Top(0).Bottom(0).Width(uikit.HairlineWidth).
		Background(c.Theme().Border).PassThrough()
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
// prefix, the highlighted line cut with an ellipsis, and its actions while
// hovered or chosen. The current match takes the sidebar's selection look.
func (a *app) searchResultRow(c *ui.Context, tree ui.TreeParts, at int) {
	s := &a.searchAll
	result := s.results[at]
	c.Key(fmt.Sprintf("search-match-%d", at))
	item := tree.Item(nil)
	row := searchTreeRow(c, item, 2).Label(locales.OpenSearchResult(result.resource))
	if at == s.current {
		uikit.Selected(c, row, !a.windowBehind)
	}
	activate := func() { a.openSearchResult(result) }
	row.OnClick(activate)
	inline := s.replace.open && s.replace.input != "" && result.replaceable()
	row.Children(func() {
		uikit.Meta(c, searchFieldPrefix(result.field)).Shrink(0).SingleLine()
		a.searchMatchedLine(c, result, inline)
		if s.replace.open && (row.Hovered() || at == s.current) {
			if result.replaceable() {
				if uikit.Tip(c, uikit.RowIconButton(c, locales.Replace(), locales.ReplaceThisMatch(), lucide.Replace()).Size(18, 18),
					locales.ReplaceThisMatch()).Clicked() {
					a.replaceOneSearchMatch(at)
				}
			}
			if uikit.Tip(c, uikit.RowIconButton(c, locales.DismissResult(), locales.DismissResult(), closeIcon).Size(18, 18),
				locales.DismissResult()).Clicked() {
				a.dismissSearchMatch(at)
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

// searchMatchedLine is one matching line. The match takes a translucent
// warning wash; with Replace open it is struck through on the danger wash
// and followed by the replacement on the success wash.
func (a *app) searchMatchedLine(c *ui.Context, result searchResult, inline bool) ui.Element {
	t := c.Theme()
	p := uikit.Colors(t)
	lead, match, tail := searchLineParts(result.text, result.start, result.end)
	size := uikit.Scaled(uikit.TextMeta)
	line := ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Label(locales.OpenSearchResult(result.resource))
	line.Children(func() {
		if lead != "" {
			searchLeadRun(c, lead, size)
		}
		if inline {
			ui.RichText(c, ui.Span{Text: match, Size: size, Strikethrough: true, Color: p.Faint,
				Background: p.Danger.Alpha(0.18), Radius: 2}).
				Font(uikit.FontMono).FontSize(size).SingleLine().Shrink(0)
			replacement := a.searchAll.query.expand(result.text, result.start, result.end, a.searchAll.replace.input)
			if a.searchAll.replace.preserve {
				replacement = preserveCase(result.match(), replacement)
			}
			ui.RichText(c, ui.Span{Text: replacement, Size: size, Color: t.Text,
				Background: p.Success.Alpha(0.22), Radius: 2}).
				Font(uikit.FontMono).FontSize(size).SingleLine().Shrink(0)
		} else {
			ui.RichText(c, ui.Span{Text: match, Size: size, Color: t.Text,
				Background: p.Warning.Alpha(0.30), Radius: 2}).
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
