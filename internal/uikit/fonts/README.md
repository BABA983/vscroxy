# Embedded app fonts

Only the five upright, static TrueType weights used by vscroxy are included.
All files are unmodified upstream binaries: no subsetting, conversion, variable
fonts, italics, or extra weights. The TTF payload is 680,588 bytes (665 KiB);
the original OFL license, shared by both families, adds 4,383 bytes and is
embedded in the executable with the fonts.

`registerFonts` registers all weights once under the family names `Geist`
and `Geist Mono`. Windows and Linux use them through these lists
(`font_other.go`); macOS uses its system fonts, SF Pro and SF Mono
(`font_darwin.go`):

```text
Geist, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif
Geist Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace
```

The upstream 1.9.0 archive also ships `GeistSerif-Regular` and the Heart,
Square and Triangle display variants. They are not bundled: Geist Serif has
one weight and is a display face, and the app's hierarchy is sans for
interface text plus mono for data. SF Pro and SF Mono are not bundled either;
Apple's font documentation asks apps not to ship fonts that the system
provides, so macOS reads them through `system-ui` and `monospace` at runtime.

Embedded asset read failures panic as build invariant violations. Native font
registration errors (for example, an unwritable Linux font cache) are logged,
not fatal; the family lists let the text engine use installed fallback fonts.
Platform Latin fallbacks precede the CJK faces so an unavailable embedded font
does not make ASCII readers proportional or make UI labels use a CJK face.
If none of those Latin fallbacks are installed either, the native engine uses
the remaining CJK and generic families.

MyGo v0.7.3 has `ui.Theme.Font`, but no `ui.Theme.FontMono` field. Apply the
second family list through the uikit `FontMono` token and an element's `.Font(...)` (or
`ui.Font.Family` for custom drawing). The native text engine chooses the
registered face by weight: Geist 400/500/600 and Geist Mono 400/700.
Chinese glyphs remain supplied by installed platform fonts.

## Geist and Geist Mono 1.9.0

- Upstream: <https://github.com/vercel/geist-font>
- Release: <https://github.com/vercel/geist-font/releases/tag/v1.9.0>
- Tag commit: `72f67167dd304a842ef8899da57fa25c5cf21756`
- Download: <https://github.com/vercel/geist-font/releases/download/v1.9.0/geist-font-v1.9.0.zip>
- Archive SHA-256: `1f2dd9ea90e68d0358a1d234a5f156807b2c3e0e295d78d87980a6dbad1fa58c`
- Files: `geist-font/Geist/ttf/Geist-Regular.ttf`,
  `geist-font/Geist/ttf/Geist-Medium.ttf`, `geist-font/Geist/ttf/Geist-SemiBold.ttf`,
  `geist-font/GeistMono/ttf/GeistMono-Regular.ttf` and
  `geist-font/GeistMono/ttf/GeistMono-Bold.ttf`; the archive's
  `geist-font/OFL.txt`, which covers both families, is preserved as `Geist-OFL.txt`.
- Copyright 2024 The Geist Project Authors.
- License: SIL Open Font License 1.1; see [Geist-OFL.txt](Geist-OFL.txt).

## File SHA-256 checksums

```text
6bfbc2254860844f82844999b63c2977b08781daecdda9870ad0feef3bc1b3f5  Geist-Regular.ttf
4cc4316402cf88b65bb7a8cfef007388778371530f187e062f02dc922324dd70  Geist-Medium.ttf
75b3483f0cb023a01c6ca667f8e3d61d7a7772e81ec38f0a52c2fd6752ac1b24  Geist-SemiBold.ttf
bbc6e86aec043518fe50a981f452a027b195cf95a9b3bfe21b88560dc7240023  GeistMono-Regular.ttf
4dfd7f9b146984efb6f7ccb9f84938faa37d854bdeee56cd9836c8f578e54b93  GeistMono-Bold.ttf
c683bfbcc7e087f5d37a54ef628f10387c451a83ddc459b151403a164ac46c90  Geist-OFL.txt
```
