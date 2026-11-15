package app

import (
	"bytes"
	"fmt"
	"image"

	"github.com/egoist/mygo/ui"
)

// previewImagePixels bounds a previewed image's canvas. It matches the MCP
// result limit so every image the app draws has the same ceiling. The image
// decoders allocate the canvas from the header alone, so a few hundred bytes
// can ask for gigabytes unless the declared size is checked first.
const previewImagePixels = mcpMaxImagePixels

// imageTooLargeError reports a header whose canvas exceeds the preview limit.
type imageTooLargeError struct {
	width, height int
}

func (e imageTooLargeError) Error() string {
	return fmt.Sprintf("image is %d×%d; previews are limited to %d megapixels", e.width, e.height, previewImagePixels>>20)
}

// decodePreviewImage checks an image's declared size before decoding it. It
// guards against a header that would overflow the pixel count as well as one
// that is merely too large.
func decodePreviewImage(data []byte) (*ui.Bitmap, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	width, height := config.Width, config.Height
	if width <= 0 || height <= 0 || width > previewImagePixels/height || width*height > previewImagePixels {
		return nil, imageTooLargeError{width: width, height: height}
	}
	return ui.DecodeBitmap(data)
}
