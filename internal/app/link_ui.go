package app

import (
	"slices"

	"github.com/baba983/vscroxy/internal/uikit"
	"github.com/baba983/vscroxy/locales"
	"github.com/egoist/mygo/ui"
)

// linkPrompt is one link awaiting a decision. It is never filled from a link's
// label: the raw destination is what the dialog shows and what Open passes to
// the system.
type linkPrompt struct {
	open   bool
	raw    string
	target linkTarget
	trust  bool
}

// openCapturedLink is the entry point for every URL a captured body or an
// imported description put in front of the person. A previous approval for
// this exact scheme, host and port is the only way past the dialog.
func (a *app) openCapturedLink(c *ui.Context, raw string) {
	target := classifyLink(raw)
	if target.trusted(a.trustedLinkHosts) {
		c.OpenURL(raw)
		return
	}
	a.linkPrompt = linkPrompt{open: true, raw: raw, target: target}
}

// linkModal shows the whole destination before anything opens it. Blocked
// schemes get an explanation instead of an Open button; unusual destinations
// get a stronger warning and never offer a trust shortcut.
func (a *app) linkModal(c *ui.Context) {
	p := &a.linkPrompt
	if !p.open {
		return
	}
	title := locales.ExternalLinkConfirmationTitle()
	blocked := p.target.block != linkBlockNone
	if blocked {
		title = locales.BlockedLinkTitle()
	}
	uikit.Modal(c, &p.open, title, func() {
		uikit.DialogColumn(c, uikit.DialogMedium).Children(func() {
			uikit.DialogTitle(c, title)
			ui.Scroll(c).MinHeight(0).MaxHeight(130).Children(func() {
				uikit.Mono(c, p.raw).Selectable().Grow(1)
			})
			if blocked {
				uikit.Callout(c, uikit.CalloutDanger, blockedLinkMessage(p.target.block), nil)
			} else if p.target.risk == 0 {
				uikit.Prose(c, locales.ExternalLinkFromContent())
			} else {
				uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkUnusual(), nil)
				if p.target.risk&linkRiskUserinfo != 0 {
					uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkUserinfoWarning(), nil)
				}
				if p.target.risk&linkRiskScheme != 0 {
					uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkSchemeWarning(), nil)
				}
				if p.target.risk&linkRiskAddress != 0 {
					uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkAddressWarning(), nil)
				}
				if p.target.risk&linkRiskNoHost != 0 {
					uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkNoHostWarning(), nil)
				}
				if p.target.risk&linkRiskMalformedHost != 0 {
					uikit.Callout(c, uikit.CalloutDanger, locales.ExternalLinkNumericHostWarning(), nil)
				}
			}
			if p.target.trustKey != "" {
				uikit.Checkbox(c, &p.trust, locales.TrustThisHost())
			}
			uikit.DialogFooter(c).Children(func() {
				if !blocked && uikit.PrimaryButton(c, locales.OpenExternalLink()).AutoFocus().Clicked() {
					if p.trust && p.target.trustKey != "" && !slices.Contains(a.trustedLinkHosts, p.target.trustKey) {
						a.trustedLinkHosts = append(a.trustedLinkHosts, p.target.trustKey)
						a.persistSettings()
						a.done(locales.TrustedLinkHosts())
					}
					target := p.raw
					p.open = false
					c.OpenURL(target)
				}
				if uikit.SecondaryButton(c, locales.CopyLink()).Clicked() {
					c.WriteClipboard(p.raw)
					a.done(locales.LinkCopied())
				}
				if uikit.SecondaryButton(c, locales.Cancel()).Clicked() {
					p.open = false
				}
			})
		})
	})
}

func blockedLinkMessage(block linkBlock) string {
	switch block {
	case linkBlockScript:
		return locales.BlockedScriptLink()
	case linkBlockFile:
		return locales.BlockedFileLink()
	case linkBlockData:
		return locales.BlockedDataLink()
	default:
		return ""
	}
}
