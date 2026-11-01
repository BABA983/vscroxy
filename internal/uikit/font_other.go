//go:build !darwin

package uikit

// FontUI is the bundled Inter elsewhere. Latin fallbacks precede CJK faces
// so a missing embedded font does not widen ordinary UI labels.
const FontUI = "Inter, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif"
