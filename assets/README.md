# Icon

`icon.svg` is the app icon's source: a black cat on warm off-white, drawn
full bleed in a 1024 square. It is not shipped; `resources/icon.png` is.

The PNG follows Apple's macOS icon grid rather than the full bleed source:
a 1024 canvas, a 824 square with 185 corners centered in it, transparent
around. macOS does not round a `.icns` itself, and macOS 26 puts an icon
that is not already in the grid on a gray rounded plate, so the rounded
square is part of the artwork. MyGo's `mygo build` only resizes the PNG
into the `.icns` and the other platforms' icons; it adds no mask.

Regenerate both PNGs from the source after changing it:

```sh
# 1. Render the full bleed source at 1024, with Chrome for the clipping and
#    transforms in the artwork.
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

# 3. The app's own copy, embedded for runs that are not a bundle (go run,
#    mygo dev), where the Dock would show the generic executable instead.
cp resources/icon.png internal/app/icon.png
```

`resources/icon-dev.png` would be the development app's icon, which `mygo
dev` shows in place of the app's to tell them apart in the Dock. The
development run already sets the icon at runtime, so there is none.
