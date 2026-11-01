// Package uikit is vscroxy's design system: the tokens, themes and
// components every screen is built from. It depends only on MyGo; screens
// name roles and variants here instead of colors and sizes.
package uikit

// Shared metrics. Container geometry stays with each screen; these describe
// controls, not a new layout for the shell or inspector.
const (
	TextMeta  = 11
	TextBody  = 13
	TextTitle = 15
	// Desktop density, as in Proxyman and Xcode: controls and rows are 24,
	// the URL bar and Send 28.
	HeightCompact = 20
	HeightControl = 24
	HeightLarge   = 28
	HeightTabs    = 28 // a row of tabs or a pane header
	HeightBar     = 44 // title bar and the Capture toolbar
	// macOS-sized corners. Something nested in a padded container takes the
	// container's radius less the padding, so the corners stay concentric.
	RadiusControl = 6
	RadiusPanel   = 10
	RadiusDialog  = 14
	IconInline    = 14
	IconLarge     = 16
	DialogSmall   = 480
	DialogMedium  = 640
	DialogLarge   = 840
	// Latin fallbacks precede CJK faces: missing embedded fonts must not turn
	// monospace readers into proportional text. FontUI is per platform.
	FontMono = "JetBrains Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace"
)
