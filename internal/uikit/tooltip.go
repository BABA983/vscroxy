package uikit

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// tipBreakRun is how many characters a tip lets run before it breaks them.
const tipBreakRun = 32

// Tip gives e a tip of the kit's look: the canvas on the popover shadow, a
// hairline edge, RadiusPanel, 6 by 10 of padding and the small text role at
// the normal ink. It is the one tip the app draws; nothing styles its own.
// A URL or another long run breaks after / ? & = - _ . and every 32
// characters, so a tip never runs off the window.
//
// The timing is MyGo's TooltipBase: the pointer rests for TooltipDelay (120ms)
// before the tip appears, and within two thirds of it
// after a tip goes the next shows at once, so moving along a row of controls
// does not wait again. Keyboard focus shows the tip at once; Escape hides it;
// it goes above e, or below where there is no room. e is described to
// assistive technology.
func Tip(c *ui.Context, e ui.Element, text string) ui.Element {
	return TipKeys(c, e, text)
}

// TipKeys is Tip with the shortcut as quiet text after the sentence:
// TipKeys(c, b, "Hide sidebar", "⌘", "B") reads "⌘B".
func TipKeys(c *ui.Context, e ui.Element, text string, keys ...string) ui.Element {
	if text == "" && len(keys) == 0 {
		return e
	}
	t := c.Theme()
	ui.TooltipBase(c, e, func(tip ui.Element) {
		tipFace(c, tip)
		tip.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(t.Space(SpaceS)).Children(func() {
				if text != "" {
					ui.Text(c, breakTip(text)).FontSize(Scaled(TextSmall)).LineHeight(LineSmall).TextColor(t.Text)
				}
				if len(keys) > 0 {
					ui.Text(c, shortcutLabel(keys)).FontSize(Scaled(TextMeta)).FontWeight(WeightMedium).
						TextColor(t.TextMuted).SingleLine()
				}
			})
		})
	})
	e.Description(tipDescription(text, keys))
	return e
}

// shortcutLabel joins modifiers without a gap and named keys with one, so
// "⌘"+"B" is ⌘B and "Ctrl"+"S" is Ctrl S.
func shortcutLabel(keys []string) string {
	var label strings.Builder
	for i, key := range keys {
		if i > 0 && utf8.RuneCountInString(keys[i-1]) > 1 {
			label.WriteByte(' ')
		}
		label.WriteString(key)
	}
	return label.String()
}

// tipDescription is what assistive technology reads for a tip: the sentence,
// then the keys it names.
func tipDescription(text string, keys []string) string {
	if len(keys) == 0 {
		return text
	}
	return text + " (" + shortcutLabel(keys) + ")"
}

// tipFace is the one surface a tip and a hover card share: a square popover
// face, 2×4 inset, 450-point wrap and three-layer shadow. The tip's
// own short words do not need a corner or a border as a card would.
func tipFace(c *ui.Context, tip ui.Element) {
	t, p := c.Theme(), Colors(c.Theme())
	tip.Background(p.Background).TextColor(t.Text).Radius(0).MaxWidth(Scaled(TipMaxWidth)).
		Padding(TipPaddingY, TipPaddingX)
	ShadowPopover.Apply(tip, t.Dark)
}

// cardFace is the rich hover card: a 320-point, 6-point-corner panel
// with the same shadow as the small tip and 10/10/8 inset.
func cardFace(c *ui.Context, tip ui.Element) {
	t, p := c.Theme(), Colors(c.Theme())
	tip.Background(p.Background).TextColor(t.Text).Radius(RadiusControl).
		Width(Scaled(CardTipWidth)).MaxWidth(Scaled(TipMaxWidth)).
		Padding(Scaled(CardTipPaddingY), Scaled(CardTipPaddingX), Scaled(CardTipPaddingBottom), Scaled(CardTipPaddingX))
	ShadowPopover.Apply(tip, t.Dark)
}

// TipSurface gives a tip built by the caller the kit's one face: a square
// popover panel. It is for a tip whose content is more than text and
// key/value rows, as a field's list of variables; the timing, placement and
// dismissal are still TooltipBase's.
func TipSurface(c *ui.Context, tip ui.Element) {
	tipFace(c, tip)
}

// HoverCard is the rich tip of a row: a title, one line of facts under it,
// and named values. It is for what a row cannot show in its width, as the
// full URL, method and status of a captured request.
type HoverCard struct {
	Title string
	Meta  string
	Rows  []HoverCardRow
	// description is what assistive technology reads, built when the card
	// is made (NewHoverCard). A card made as a literal builds it per use.
	description string
}

// HoverCardRow is one named value of a HoverCard.
type HoverCardRow struct {
	Key   string
	Value string
}

// NewHoverCard builds a card and the description assistive technology reads
// from it, once. A table builds a card for a row and gives it to each of the
// row's cells; building that description per cell allocated a string for
// every key and value on every frame.
func NewHoverCard(title, meta string, rows []HoverCardRow) HoverCard {
	card := HoverCard{Title: title, Meta: meta, Rows: rows}
	card.description = card.describe()
	return card
}

// describe reads the card as one sentence for assistive technology.
func (card HoverCard) describe() string {
	parts := make([]string, 0, 2+len(card.Rows))
	if card.Title != "" {
		parts = append(parts, card.Title)
	}
	if card.Meta != "" {
		parts = append(parts, card.Meta)
	}
	for _, row := range card.Rows {
		if row.Key != "" || row.Value != "" {
			parts = append(parts, strings.TrimSpace(row.Key+": "+row.Value))
		}
	}
	return strings.Join(parts, ". ")
}

// CardTip gives e a rich tip: a title, one line of meta and key/value rows on
// a 320-point card. It waits longer than a one-line tip and is
// described to assistive technology by its text, since MyGo's tooltip itself
// is not read. TooltipBase's placement keeps it clear of the window's edges.
func CardTip(c *ui.Context, e ui.Element, card HoverCard) ui.Element {
	return cardTip(c, e, card, nil, nil)
}

// CardTips is CardTip's rich-tip timing shared by the cells of a table or the
// rows of a list. Build one per view and give each cell tips.Tip: the delay
// otherwise copies the whole theme for every cell on every frame.
type CardTips struct{ theme ui.Theme }

// NewCardTips captures the frame's theme with the rich tip's longer delay.
func NewCardTips(c *ui.Context) *CardTips {
	theme := *c.Theme()
	theme.TooltipDelay = cardTipDelay
	return &CardTips{theme: theme}
}

// Tip is CardTip with the timing the CardTips was built with.
func (s *CardTips) Tip(c *ui.Context, e ui.Element, card HoverCard) ui.Element {
	return cardTip(c, e, card, nil, &s.theme)
}

// CardTipBeside is CardTip opened beside e: the card's top edge is level with
// e's and it starts a step to e's right. A sidebar row uses it, so its card
// lies beside the sidebar rather than over the rows above.
func CardTipBeside(c *ui.Context, e ui.Element, card HoverCard) ui.Element {
	return cardTip(c, e, card, func(tip ui.Element) {
		tip.AttachTo(e, ui.AnchorTopRight, ui.AnchorTopLeft).MarginStart(c.Theme().Space(SpaceS))
	}, nil)
}

// A rich card appears much later than a one-line tip: about 650ms after the
// pointer settles. The theme is scoped to the card's construction,
// which is when MyGo reads TooltipDelay.
const cardTipDelay = 650 * time.Millisecond

// cardTip is the body of CardTip and CardTipBeside; place, when set, positions
// the card inside TooltipBase. shared, when set, is the frame's theme already
// carrying the rich-tip delay, so that a table's cells do not copy it each.
func cardTip(c *ui.Context, e ui.Element, card HoverCard, place func(tip ui.Element), shared *ui.Theme) ui.Element {
	if card.Title == "" && card.Meta == "" && len(card.Rows) == 0 {
		return e
	}
	t := c.Theme()
	cardTheme := shared
	if cardTheme == nil {
		local := *t
		local.TooltipDelay = cardTipDelay
		cardTheme = &local
	}
	c.SetTheme(cardTheme)
	ui.TooltipBase(c, e, func(tip ui.Element) {
		cardFace(c, tip)
		if place != nil {
			place(tip)
		}
		tip.Children(func() {
			ui.Column(c).Children(func() {
				head := ui.Column(c).Gap(t.Space(SpaceHair)).Padding(0, 0, Scaled(6), 0)
				head.Children(func() {
					if card.Title != "" {
						ui.Text(c, breakTip(card.Title)).FontSize(Scaled(TextBody)).FontWeight(WeightMedium).
							LineHeight(LineBody).TextColor(t.Text)
					}
					if card.Meta != "" {
						Meta(c, card.Meta).SingleLine()
					}
				})
				if len(card.Rows) > 0 {
					ui.Divider(c)
					ui.Column(c).Gap(t.Space(SpaceHair)).Padding(Scaled(6), 0, 0, 0).Children(func() {
						for _, row := range card.Rows {
							if row.Key == "" && row.Value == "" {
								continue
							}
							ui.Row(c).FillWidth().Gap(t.Space(SpaceM)).AlignItems(ui.Start).Children(func() {
								Meta(c, row.Key).Width(Scaled(CardKeyWidth)).Shrink(0).Padding(0, 0, HairlineWidth, 0)
								if strings.Contains(row.Value, "://") {
									Mono(c, breakTip(row.Value)).TextColor(t.Text).Grow(1).MinWidth(0)
								} else {
									Small(c, breakTip(row.Value)).TextColor(t.Text).Grow(1).MinWidth(0)
								}
							})
						}
					})
				}
			})
		})
	})
	c.SetTheme(t)
	if card.description == "" {
		card.description = card.describe()
	}
	e.Description(card.description)
	return e
}

// breakTip gives a tip's text places to wrap only where a word cannot fit:
// a URL, or a run longer than the limit. Prose keeps its own spaces and
// punctuation, so a sentence is not cut at every dash.
func breakTip(s string) string {
	if strings.Contains(s, "://") {
		return breakLong(s)
	}
	run := 0
	for _, r := range s {
		switch r {
		case ' ', '\n', '\t':
			run = 0
			continue
		}
		run++
		if run > tipBreakRun {
			return breakLong(s)
		}
	}
	return s
}

// breakLong gives a long string places to wrap. A URL gets a zero-width
// space after the characters that already divide it; only a run with none of
// them is broken after 32 characters.
func breakLong(s string) string {
	const limit = tipBreakRun
	var b strings.Builder
	run := 0
	for _, r := range s {
		b.WriteRune(r)
		if r == '\u200b' {
			run = 0
			continue
		}
		run++
		if strings.ContainsRune("/?&=-_.", r) || run >= limit {
			b.WriteRune('\u200b')
			run = 0
		}
	}
	return strings.TrimSuffix(b.String(), "\u200b")
}
