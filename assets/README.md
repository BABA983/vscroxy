# Icon

`icon.svg` is the app icon's source: a black cat on warm off-white, drawn
full bleed in a 1024 square. `icon-small.svg` is the same cat drawn for 16
and 32 points: head, ears and eyes, closer in and without the tail.

## What ships

- `AppIcon.icon/` is the Icon Composer document macOS 26 reads: a warm fill
  and two layers, the cat and its eyes. `mygo.json` names it as `macos.icon`,
  so `mygo build` compiles it with `actool` into the app's `Assets.car` and
  `.icns` (Xcode 26 or later; elsewhere the build falls back to
  `resources/icon.png`). macOS 26 puts an icon that is not an Icon Composer
  document on a gray plate and gives its artwork an emboss; the layers avoid
  that, and the group carries no shadow or specular highlight so it stays a
  half-flat drawing.
- `resources/icon.png` is the full bleed source in Apple's macOS icon grid: a
  824 square with 185 corners centered on a transparent 1024 canvas. Windows
  and Linux icons come from it, and so does the `.icns` of a build without
  Xcode. macOS does not round a `.icns` itself, so the shape is part of the
  artwork; MyGo only resizes the PNG, and masks nothing twice.
- `internal/app/icon.png` is that PNG embedded in the binary: a run that is
  not a bundle sets it on the Dock as it starts, so `go run` and `mygo dev`
  show the cat rather than the generic executable.

## Making them from the sources

```sh
# 1. The full bleed source at 1024, with Chrome for its clipping and
#    transforms.
chrome --headless --hide-scrollbars --window-size=1024,1024 \
  --default-background-color=00000000 \
  --screenshot=/tmp/icon-art.png "file://$PWD/assets/icon.svg"

# 2. Put it in the grid: 824 square, 185 corners, 100 transparent on each side.
magick /tmp/icon-art.png -resize 824x824 /tmp/icon-art-824.png
magick -size 824x824 xc:black -fill white \
  -draw "roundrectangle 0,0,823,823,185,185" /tmp/icon-mask.png
magick /tmp/icon-art-824.png /tmp/icon-mask.png -alpha off \
  -compose CopyOpacity -composite /tmp/icon-rounded.png
magick -size 1024x1024 xc:none /tmp/icon-rounded.png -geometry +100+100 \
  -composite resources/icon.png

# 3. The app's own copy, for runs that are not a bundle.
cp resources/icon.png internal/app/icon.png

# 4. Check the Icon Composer document compiles, and how it renders.
xcrun actool assets/AppIcon.icon --compile /tmp/icon --platform macosx \
  --target-device mac --minimum-deployment-target 12.0 --app-icon AppIcon \
  --output-partial-info-plist /tmp/icon/partial.plist
"$(xcode-select -p)/../Applications/Icon Composer.app/Contents/Executables/ictool" \
  assets/AppIcon.icon --export-image --output-file /tmp/icon-1024.png \
  --platform macOS --rendition Default --width 1024 --height 1024 --scale 1
```

## The small icon

`icon-small.svg` cannot be wired into `AppIcon.icon`: the format has no slot
for a size, so macOS 26 renders the document's layers at 16 and 32 as they
are. It is kept as the drawing for a per-size route — a hand-built `.icns`
that `macos.icon` can name instead (at the cost of macOS 26's layered
treatment), or an asset catalog with size slots if MyGo gains one.
