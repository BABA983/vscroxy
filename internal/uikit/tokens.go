// Package uikit is vscroxy's design system: the tokens, themes and
// components every screen is built from. It depends only on MyGo; screens
// name roles and variants here instead of colors and sizes.
package uikit

// Shared metrics. Container geometry stays with each screen; these describe
// controls, not a new layout for the shell or inspector.
const (
	TextMeta      = 11
	TextBody      = 13
	TextTitle     = 15
	HeightCompact = 24
	HeightControl = 28
	HeightLarge   = 32
	HeightTabs    = 32
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
	// monospace readers into proportional text or widen ordinary UI labels.
	FontUI   = "Inter, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif"
	FontMono = "JetBrains Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace"
)
