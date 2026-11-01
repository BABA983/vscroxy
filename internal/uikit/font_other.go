//go:build !darwin

package uikit

// Elsewhere the fonts are the bundled Geist and Geist Mono. Latin fallbacks
// precede CJK faces, so a missing embedded font neither widens ordinary
// labels nor turns monospace readers proportional.
const (
	FontUI   = "Geist, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif"
	FontMono = "Geist Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace"
)
