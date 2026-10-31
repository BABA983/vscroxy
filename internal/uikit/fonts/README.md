# Embedded app fonts

Only the five upright, static TrueType weights used by vscroxy are included.
All files are unmodified upstream binaries: no subsetting, conversion, variable
fonts, italics, or extra weights. The TTF payload is 1,800,412 bytes (1.72 MiB);
the two original OFL licenses add 8,779 bytes. Both licenses are embedded in the
executable with the fonts.

`registerAppFonts()` registers all weights once under the family names `Inter`
and `JetBrains Mono`. Use these lists for the theme and monospaced elements:

```text
Inter, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, sans-serif
JetBrains Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC, monospace
```

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
registered face by weight: Inter 400/500/600 and JetBrains Mono 400/700.
Chinese glyphs remain supplied by installed platform fonts.

## Inter 4.1

- Upstream: <https://github.com/rsms/inter>
- Release: <https://github.com/rsms/inter/releases/tag/v4.1>
- Tag commit: `e3a3d4c57d5ecc01453a575621882a384c1995a3`
- Download: <https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip>
- Archive SHA-256: `9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e`
- Files: `extras/ttf/Inter-Regular.ttf`, `extras/ttf/Inter-Medium.ttf`,
  `extras/ttf/Inter-SemiBold.ttf`; the archive's `LICENSE.txt` is preserved as
  `Inter-OFL.txt`.
- Copyright (c) 2016 The Inter Project Authors.
- License: SIL Open Font License 1.1; see [Inter-OFL.txt](Inter-OFL.txt).

## JetBrains Mono 2.304

- Upstream: <https://github.com/JetBrains/JetBrainsMono>
- Release: <https://github.com/JetBrains/JetBrainsMono/releases/tag/v2.304>
- Tag commit: `cd5227bd1f61dff3bbd6c814ceaf7ffd95e947d9`
- Files: `fonts/ttf/JetBrainsMono-Regular.ttf` and
  `fonts/ttf/JetBrainsMono-Bold.ttf` at that commit, downloaded from
  `https://raw.githubusercontent.com/JetBrains/JetBrainsMono/cd5227bd1f61dff3bbd6c814ceaf7ffd95e947d9/`.
- The same commit's `OFL.txt` is preserved as `JetBrainsMono-OFL.txt`.
- Copyright 2020 The JetBrains Mono Project Authors.
- License: SIL Open Font License 1.1; see
  [JetBrainsMono-OFL.txt](JetBrainsMono-OFL.txt).

## File SHA-256 checksums

```text
40d692fce188e4471e2b3cba937be967878f631ad3ebbbdcd587687c7ebe0c82  Inter-Regular.ttf
97ad806f526e41546d46365bb3a393145f75b7b1568913db74549ad8b8dba872  Inter-Medium.ttf
78a843fade9d4612a5567302fb595b56976eb5fcebf4fea5a5912d638bafcde3  Inter-SemiBold.ttf
262481e844521b326f5ecd053e59b98c8b2da78c8ee1bdbb6e8174305e54935a  Inter-OFL.txt
a0bf60ef0f83c5ed4d7a75d45838548b1f6873372dfac88f71804491898d138f  JetBrainsMono-Regular.ttf
5590990c82e097397517f275f430af4546e1c45cff408bde4255dad142479dcb  JetBrainsMono-Bold.ttf
30f0c136e3c88e422d0791acd97238870f9054a9729bc34cf2ff0d4ed8cac4ad  JetBrainsMono-OFL.txt
```
